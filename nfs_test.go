package nfs_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math/rand"
	"net"
	"os"
	"reflect"
	"sort"
	"sync"
	"syscall"
	"testing"

	"github.com/go-git/go-billy/v6"
	nfs "github.com/treeverse/go-nfs"
	"github.com/treeverse/go-nfs/helpers"
	"github.com/treeverse/go-nfs/helpers/memfs"

	nfsc "github.com/willscott/go-nfs-client/nfs"
	rpc "github.com/willscott/go-nfs-client/nfs/rpc"
	"github.com/willscott/go-nfs-client/nfs/util"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

// testCacheLimit is an arbitrary handle-cache size, large enough for these tests.
const testCacheLimit = 1024

type OpenArgs struct {
	File string
	Flag int
	Perm os.FileMode
}

func (o *OpenArgs) String() string {
	return fmt.Sprintf("\"%s\"; %05xd %s", o.File, o.Flag, o.Perm)
}

// NewTrackingFS wraps fs to detect file handle leaks.
func NewTrackingFS(fs billy.Filesystem) *trackingFS {
	return &trackingFS{Filesystem: fs, open: make(map[int64]OpenArgs)}
}

// trackingFS wraps a Filesystem to detect file handle leaks.
type trackingFS struct {
	billy.Filesystem
	mu   sync.Mutex
	open map[int64]OpenArgs
}

func (t *trackingFS) ListOpened() []OpenArgs {
	t.mu.Lock()
	defer t.mu.Unlock()
	ret := make([]OpenArgs, 0, len(t.open))
	for _, o := range t.open {
		ret = append(ret, o)
	}
	return ret
}

func (t *trackingFS) Create(filename string) (billy.File, error) {
	return t.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
}

func (t *trackingFS) Open(filename string) (billy.File, error) {
	return t.OpenFile(filename, os.O_RDONLY, 0)
}

func (t *trackingFS) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	open, err := t.Filesystem.OpenFile(filename, flag, perm)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	id := rand.Int63()
	t.open[id] = OpenArgs{filename, flag, perm}
	closer := func() {
		delete(t.open, id)
	}
	open = &trackingFile{
		File:    open,
		onClose: closer,
	}
	return open, err
}

type trackingFile struct {
	billy.File
	onClose func()
}

func (f *trackingFile) Close() error {
	f.onClose()
	return f.File.Close()
}

// serveAndMount starts an NFS server backed by handler and mounts it, registering cleanup for both.
// Test helper shared across handler-level tests.
func serveAndMount(t *testing.T, handler nfs.Handler) *nfsc.Target {
	t.Helper()

	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = nfs.Serve(listener, handler)
	}()

	// rpc.DialTCP binds a random local source port and, for non-privileged
	// clients, does not retry when that port is already in use. Retry here so
	// tests that mount several times in quick succession don't flake on a
	// transient EADDRINUSE; a fresh random port almost always succeeds.
	var c *rpc.Client
	for attempt := 0; attempt < 10; attempt++ {
		c, err = rpc.DialTCP(listener.Addr().Network(), listener.Addr().(*net.TCPAddr).String(), false)
		if err == nil || !errors.Is(err, syscall.EADDRINUSE) {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })

	var mounter nfsc.Mount
	mounter.Client = c
	target, err := mounter.Mount("/", rpc.AuthNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mounter.Unmount() })

	return target
}

func TestNFS(t *testing.T) {
	if testing.Verbose() {
		util.DefaultLogger.SetDebug(true)
	}

	// make an empty in-memory server.
	mem := NewTrackingFS(memfs.New())

	defer func() {
		if opened := mem.ListOpened(); len(opened) > 0 {
			t.Errorf("Unclosed files: %v", opened)
		}
	}()

	// File needs to exist in the root for memfs to acknowledge the root exists.
	r, _ := mem.Create("/test")
	r.Close()

	handler := helpers.NewNullAuthHandler(mem)
	cacheHelper := helpers.NewCachingHandler(handler, testCacheLimit)
	target := serveAndMount(t, cacheHelper)

	_, err := target.FSInfo()
	if err != nil {
		t.Fatal(err)
	}

	// Validate sample file creation
	_, err = target.Create("/helloworld.txt", 0666)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := mem.Stat("/helloworld.txt"); err != nil {
		t.Fatal(err)
	} else {
		if info.Size() != 0 || info.Mode().Perm() != 0666 {
			t.Fatal("incorrect creation.")
		}
	}

	// Validate writing to a file.
	f, err := target.OpenFile("/helloworld.txt", 0666)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b := []byte("hello world")
	_, err = f.Write(b)
	if err != nil {
		t.Fatal(err)
	}

	mf, err := target.Open("/helloworld.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer mf.Close()
	buf := make([]byte, len(b))
	if _, err = mf.Read(buf[:]); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, b) {
		t.Fatal("written does not match expected")
	}

	// RMDIR on a non-directory must fail with NFS3ERR_NOTDIR, without removing it
	if err := target.RmDir("/helloworld.txt"); err == nil {
		t.Fatal("expected RmDir on a file to fail")
	} else if !nfsc.IsNotDirError(err) {
		t.Fatalf("expected NFS3ERR_NOTDIR, got: %v", err)
	}
	if _, err := mem.Stat("/helloworld.txt"); err != nil {
		t.Fatal("file removed by failed RmDir:", err)
	}

	// for test nfs.ReadDirPlus in case of many files
	dirF1, err := mem.ReadDir("/")
	if err != nil {
		t.Fatal(err)
	}
	shouldBeNames := []string{}
	for _, f := range dirF1 {
		shouldBeNames = append(shouldBeNames, f.Name())
	}
	for i := 0; i < 2000; i++ {
		fName := fmt.Sprintf("f-%04d.txt", i)
		shouldBeNames = append(shouldBeNames, fName)
		f, err := mem.Create(fName)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}

	manyEntitiesPlus, err := target.ReadDirPlus("/")
	if err != nil {
		t.Fatal(err)
	}
	actualBeNamesPlus := []string{}
	for _, e := range manyEntitiesPlus {
		actualBeNamesPlus = append(actualBeNamesPlus, e.Name())
	}

	as := sort.StringSlice(shouldBeNames)
	bs := sort.StringSlice(actualBeNamesPlus)
	as.Sort()
	bs.Sort()
	if !reflect.DeepEqual(as, bs) {
		t.Fatal("nfs.ReadDirPlus error")
	}

	// for test nfs.ReadDir in case of many files
	manyEntities, err := readDir(target, "/")
	if err != nil {
		t.Fatal(err)
	}
	actualBeNames := []string{}
	for _, e := range manyEntities {
		actualBeNames = append(actualBeNames, e.FileName)
	}

	as2 := sort.StringSlice(shouldBeNames)
	bs2 := sort.StringSlice(actualBeNames)
	as2.Sort()
	bs2.Sort()
	if !reflect.DeepEqual(as2, bs2) {
		t.Logf("should be %v\n", as2)
		t.Logf("actual be %v\n", bs2)
		t.Fatal("nfs.ReadDir error")
	}

	// confirm rename works as expected
	oldFA, _, err := target.Lookup("/f-0010.txt", false)
	if err != nil {
		t.Fatal(err)
	}

	if err := target.Rename("/f-0010.txt", "/g-0010.txt"); err != nil {
		t.Fatal(err)
	}
	new, _, err := target.Lookup("/g-0010.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if new.Sys() != oldFA.Sys() {
		t.Fatal("rename failed to update")
	}
	_, _, err = target.Lookup("/f-0010.txt", false)
	if err == nil {
		t.Fatal("old handle should be invalid")
	}

	// for test nfs.ReadDirPlus in case of empty directory
	_, err = target.Mkdir("/empty", 0755)
	if err != nil {
		t.Fatal(err)
	}

	emptyEntitiesPlus, err := target.ReadDirPlus("/empty")
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyEntitiesPlus) != 0 {
		t.Fatal("nfs.ReadDirPlus error reading empty dir")
	}

	// for test nfs.ReadDir in case of empty directory
	emptyEntities, err := readDir(target, "/empty")
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyEntities) != 0 {
		t.Fatal("nfs.ReadDir error reading empty dir")
	}

	// REMOVE on a directory must fail with NFS3ERR_ISDIR, without removing it
	var nfsErr *nfsc.Error
	if err := target.Remove("/empty"); err == nil {
		t.Fatal("expected Remove on a directory to fail")
	} else if !errors.As(err, &nfsErr) || nfsErr.ErrorNum != nfsc.NFS3ErrIsDir {
		t.Fatalf("expected NFS3ERR_ISDIR, got: %v", err)
	}
	if _, err := mem.Stat("/empty"); err != nil {
		t.Fatal("directory removed by failed Remove:", err)
	}

	// RMDIR on a non-empty directory must fail with NFS3ERR_NOTEMPTY, without removing it
	if _, err := target.Mkdir("/nonempty", 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Create("/nonempty/file.txt", 0666); err != nil {
		t.Fatal(err)
	}
	if err := target.RmDir("/nonempty"); err == nil {
		t.Fatal("expected RmDir on a non-empty directory to fail")
	} else if !nfsc.IsNotEmptyError(err) {
		t.Fatalf("expected NFS3ERR_NOTEMPTY, got: %v", err)
	}
	if _, err := mem.Stat("/nonempty/file.txt"); err != nil {
		t.Fatal("directory contents removed by failed RmDir:", err)
	}

	// RMDIR and REMOVE must judge a symlink by its own type, not by what it points to:
	// RMDIR on a symlink-to-directory must fail with NFS3ERR_NOTDIR,
	// and REMOVE on it must succeed, removing only the link.
	if err := target.Symlink("/empty", "/link-to-empty"); err != nil {
		t.Fatal(err)
	}
	if err := target.RmDir("/link-to-empty"); err == nil {
		t.Fatal("expected RmDir on a symlink to fail")
	} else if !nfsc.IsNotDirError(err) {
		t.Fatalf("expected NFS3ERR_NOTDIR, got: %v", err)
	}
	if _, err := mem.Lstat("/link-to-empty"); err != nil {
		t.Fatal("symlink removed by failed RmDir:", err)
	}
	if err := target.Remove("/link-to-empty"); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.Lstat("/link-to-empty"); err == nil {
		t.Fatal("Remove did not remove the symlink")
	}
	if _, err := mem.Stat("/empty"); err != nil {
		t.Fatal("Remove of symlink affected its target:", err)
	}
}

// fakeDirIteratorHandler wraps a Handler with a DirIteratorHandler that hands out a fakeDirIterator
// instead of ever touching the real filesystem, so tests can prove onRemoveObj prefers it over fs.ReadDir.
type fakeDirIteratorHandler struct {
	nfs.Handler
	hasEntry bool  // whether the iterated directory has any entries
	openErr  error // if non-nil, OpenDir fails with this instead of iterating
	opened   bool  // set once OpenDir is called
}

func (h *fakeDirIteratorHandler) OpenDir(_ context.Context, _ string, _, _ uint64) (nfs.DirIterator, error) {
	h.opened = true
	if h.openErr != nil {
		return nil, h.openErr
	}
	return &fakeDirIterator{hasEntry: h.hasEntry}, nil
}

// newEmptyFakeDirIteratorHandler and newNonEmptyFakeDirIteratorHandler ignore
// the requested path; their answer applies to any directory.
func newEmptyFakeDirIteratorHandler(mem billy.Filesystem) *fakeDirIteratorHandler {
	return &fakeDirIteratorHandler{
		Handler: helpers.NewCachingHandler(helpers.NewNullAuthHandler(mem), testCacheLimit),
	}
}

func newNonEmptyFakeDirIteratorHandler(mem billy.Filesystem) *fakeDirIteratorHandler {
	return &fakeDirIteratorHandler{
		Handler:  helpers.NewCachingHandler(helpers.NewNullAuthHandler(mem), testCacheLimit),
		hasEntry: true,
	}
}

// newErrorFakeDirIteratorHandler makes OpenDir fail with openErr.
func newErrorFakeDirIteratorHandler(mem billy.Filesystem, openErr error) *fakeDirIteratorHandler {
	return &fakeDirIteratorHandler{
		Handler: helpers.NewCachingHandler(helpers.NewNullAuthHandler(mem), testCacheLimit),
		openErr: openErr,
	}
}

// fakeDirIterator simulates a directory with either zero or one entry.
type fakeDirIterator struct {
	hasEntry bool
	served   bool
}

func (it *fakeDirIterator) Next() bool {
	ok := it.hasEntry && !it.served
	it.served = true
	return ok
}

func (it *fakeDirIterator) FileInfo() fs.FileInfo { return nil }
func (it *fakeDirIterator) Cookie() uint64        { return 0 }
func (it *fakeDirIterator) Verifier() uint64      { return 0 }
func (it *fakeDirIterator) Close()                {}

// TestRmdirViaDirIterator proves onRemoveObj uses DirIteratorHandler to check if the target dir is empty.
func TestRmdirViaDirIterator(t *testing.T) {
	t.Run("IteratorReportsNonEmpty", func(t *testing.T) {
		mem := memfs.New()
		if err := mem.MkdirAll("/dir", 0755); err != nil {
			t.Fatal(err)
		}

		fakeHandler := newNonEmptyFakeDirIteratorHandler(mem)
		target := serveAndMount(t, fakeHandler)

		if err := target.RmDir("/dir"); err == nil {
			t.Fatal("expected RmDir to fail")
		} else if !nfsc.IsNotEmptyError(err) {
			t.Fatalf("expected NFS3ERR_NOTEMPTY, got: %v", err)
		}
		if !fakeHandler.opened {
			t.Fatal("expected onRemoveObj to use the DirIteratorHandler")
		}
	})

	t.Run("IteratorReportsEmpty", func(t *testing.T) {
		mem := memfs.New()
		if err := mem.MkdirAll("/dir", 0755); err != nil {
			t.Fatal(err)
		}

		fakeHandler := newEmptyFakeDirIteratorHandler(mem)
		target := serveAndMount(t, fakeHandler)

		if err := target.RmDir("/dir"); err != nil {
			t.Fatalf("expected RmDir to succeed, got: %v", err)
		}
		if !fakeHandler.opened {
			t.Fatal("expected onRemoveObj to use the DirIteratorHandler")
		}
	})

	// A %w-wrapped sentinel from OpenDir (as a non-billy backend would return)
	// must be classified by its cause, not collapsed to NFS3ERR_IO.
	t.Run("IteratorOpenErrorMapsToNoEnt", func(t *testing.T) {
		mem := memfs.New()
		if err := mem.MkdirAll("/dir", 0755); err != nil {
			t.Fatal(err)
		}

		wrapped := fmt.Errorf("open dir: %w", os.ErrNotExist)
		fakeHandler := newErrorFakeDirIteratorHandler(mem, wrapped)
		target := serveAndMount(t, fakeHandler)

		err := target.RmDir("/dir")
		if err == nil {
			t.Fatal("expected RmDir to fail")
		}
		// The client maps NFS3ERR_NOENT to os.ErrNotExist.
		// Before the errors.Is fix in onRemoveObj, the wrapped OpenDir error fell through to NFS3ERR_IO,
		// which the client surfaces as a *nfsc.Error instead.
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected NFS3ERR_NOENT, got: %v", err)
		}
	})
}

type readDirEntry struct {
	FileId   uint64
	FileName string
	Cookie   uint64
}

// readDir implementation "appropriated" from go-nfs-client implementation of READDIRPLUS
func readDir(target *nfsc.Target, dir string) ([]*readDirEntry, error) {
	_, fh, err := target.Lookup(dir)
	if err != nil {
		return nil, err
	}

	type readDirArgs struct {
		rpc.Header
		Handle      []byte
		Cookie      uint64
		CookieVerif uint64
		Count       uint32
	}

	type readDirList struct {
		IsSet bool         `xdr:"union"`
		Entry readDirEntry `xdr:"unioncase=1"`
	}

	type readDirListOK struct {
		DirAttrs   nfsc.PostOpAttr
		CookieVerf uint64
	}

	cookie := uint64(0)
	cookieVerf := uint64(0)
	eof := false

	var entries []*readDirEntry
	for !eof {
		res, err := target.Call(&readDirArgs{
			Header: rpc.Header{
				Rpcvers: 2,
				Vers:    nfsc.Nfs3Vers,
				Prog:    nfsc.Nfs3Prog,
				Proc:    uint32(nfs.NFSProcedureReadDir),
				Cred:    rpc.AuthNull,
				Verf:    rpc.AuthNull,
			},
			Handle:      fh,
			Cookie:      cookie,
			CookieVerif: cookieVerf,
			Count:       4096,
		})
		if err != nil {
			return nil, err
		}

		status, err := xdr.ReadUint32(res)
		if err != nil {
			return nil, err
		}

		if err = nfsc.NFS3Error(status); err != nil {
			return nil, err
		}

		dirListOK := new(readDirListOK)
		if err = xdr.Read(res, dirListOK); err != nil {
			return nil, err
		}

		for {
			var item readDirList
			if err = xdr.Read(res, &item); err != nil {
				return nil, err
			}

			if !item.IsSet {
				break
			}

			cookie = item.Entry.Cookie
			if item.Entry.FileName == "." || item.Entry.FileName == ".." {
				continue
			}
			entries = append(entries, &item.Entry)
		}

		if err = xdr.Read(res, &eof); err != nil {
			return nil, err
		}

		cookieVerf = dirListOK.CookieVerf
	}

	return entries, nil
}
