package nfs_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/go-git/go-billy/v6"
	nfs "github.com/treeverse/go-nfs"
	"github.com/treeverse/go-nfs/file"
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
	target, _, _ := serveServerAndMount(t, &nfs.Server{Handler: handler})
	return target
}

// serveServerAndMount also returns what srv.Serve returns.
func serveServerAndMount(t *testing.T, srv *nfs.Server) (*nfsc.Target, *nfsc.Mount, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	if srv.Context != nil {
		// The caller closes the listener on shutdown, as Everest does.
		context.AfterFunc(srv.Context, func() { _ = listener.Close() })
	}
	served := make(chan error, 1)
	go func() {
		served <- srv.Serve(listener)
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

	mounter := &nfsc.Mount{Client: c}
	target, err := mounter.Mount("/", rpc.AuthNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Skip Unmount once the server has stopped: the client would retry it for 10s.
		if srv.Context == nil || srv.Context.Err() == nil {
			_ = mounter.Unmount()
		}
	})

	return target, mounter, served
}

type umountRecorder struct {
	nfs.Handler
	dirpath string
}

func (h *umountRecorder) Umount(_ context.Context, _ net.Conn, req nfs.MountRequest) error {
	h.dirpath = string(req.Dirpath)
	return nil
}

func TestUmount(t *testing.T) {
	mem := memfs.New()
	f, err := mem.Create("/test")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	recorder := &umountRecorder{Handler: helpers.NewCachingHandler(helpers.NewNullAuthHandler(mem), testCacheLimit)}
	_, mounter, _ := serveServerAndMount(t, &nfs.Server{Handler: recorder})
	if err := mounter.Unmount(); err != nil {
		t.Fatal(err)
	}
	if recorder.dirpath != "/" {
		t.Errorf("Umount(%q), want \"/\"", recorder.dirpath)
	}
}

// cancelOnUmount cancels the server's context from Umount, as a server that exits on unmount does.
type cancelOnUmount struct {
	nfs.Handler
	cancel context.CancelFunc
}

func (h *cancelOnUmount) Umount(context.Context, net.Conn, nfs.MountRequest) error {
	h.cancel()
	return nil
}

func TestUmountCancellingServerContext(t *testing.T) {
	mem := memfs.New()
	f, err := mem.Create("/test")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := &cancelOnUmount{Handler: helpers.NewCachingHandler(helpers.NewNullAuthHandler(mem), testCacheLimit), cancel: cancel}
	_, mounter, served := serveServerAndMount(t, &nfs.Server{Handler: handler, Context: ctx})
	if err := mounter.Unmount(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after its context ended")
	}
}

// failingWriteConn fails every Write and counts the calls.
type failingWriteConn struct {
	net.Conn
	writes atomic.Int32
}

func (c *failingWriteConn) Write([]byte) (int, error) {
	c.writes.Add(1)
	return 0, errors.New("write failed")
}

// oneConnListener hands out the conns sent on conns, then net.ErrClosed once closed.
type oneConnListener struct {
	conns chan net.Conn
	// Serve closes its listener too (server.go defer l.Close()), so Close runs twice.
	once sync.Once
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	c, ok := <-l.conns
	if !ok {
		return nil, net.ErrClosed
	}
	return c, nil
}

func (l *oneConnListener) Close() error {
	l.once.Do(func() { close(l.conns) })
	return nil
}

func (l *oneConnListener) Addr() net.Addr { return &net.TCPAddr{} }

func TestServeReturnsAfterReplyWriteFails(t *testing.T) {
	client, server := net.Pipe()
	conn := &failingWriteConn{Conn: server}
	listener := &oneConnListener{conns: make(chan net.Conn, 1)}
	listener.conns <- conn
	served := make(chan error, 1)
	go func() { served <- (&nfs.Server{}).Serve(listener) }()

	// Three RPC CALLs for an unknown program; each gets a PROC_UNAVAIL reply.
	// Write errors are fine: the server may close the conn once a reply fails.
	for xid := uint32(1); xid <= 3; xid++ {
		var frame [11]uint32
		frame[0] = 1<<31 | 40
		frame[1], frame[3], frame[4], frame[5] = xid, 2, 99999, 1
		_ = binary.Write(client, binary.BigEndian, frame)
	}
	_ = client.Close()
	_ = listener.Close()

	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after a reply write failed")
	}
	if n := conn.writes.Load(); n != 1 {
		t.Errorf("Write called %d times, want 1", n)
	}
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
	readErr  error // if non-nil, the iterator reports this from Err() and yields no entries
	opened   bool  // set once OpenDir is called
}

func (h *fakeDirIteratorHandler) OpenDir(_ context.Context, _ string, _, _ uint64) (nfs.DirIterator, error) {
	h.opened = true
	if h.openErr != nil {
		return nil, h.openErr
	}
	return &fakeDirIterator{hasEntry: h.hasEntry, readErr: h.readErr}, nil
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

// newReadErrorFakeDirIteratorHandler makes the iterator fail its read with readErr,
// yielding no entries.
func newReadErrorFakeDirIteratorHandler(mem billy.Filesystem, readErr error) *fakeDirIteratorHandler {
	return &fakeDirIteratorHandler{
		Handler: helpers.NewCachingHandler(helpers.NewNullAuthHandler(mem), testCacheLimit),
		readErr: readErr,
	}
}

// newErrorFakeDirIteratorHandler makes OpenDir fail with openErr.
func newErrorFakeDirIteratorHandler(mem billy.Filesystem, openErr error) *fakeDirIteratorHandler {
	return &fakeDirIteratorHandler{
		Handler: helpers.NewCachingHandler(helpers.NewNullAuthHandler(mem), testCacheLimit),
		openErr: openErr,
	}
}

// fakeDirIterator simulates a directory with either zero or one entry,
// optionally failing its read.
type fakeDirIterator struct {
	hasEntry bool
	served   bool
	readErr  error
}

func (it *fakeDirIterator) Handle() []byte {
	return []byte("fake-handle")
}

func (it *fakeDirIterator) Next() bool {
	if it.readErr != nil {
		return false
	}
	ok := it.hasEntry && !it.served
	it.served = true
	return ok
}

func (it *fakeDirIterator) FileInfo() fs.FileInfo { return nil }
func (it *fakeDirIterator) Cookie() uint64        { return 0 }
func (it *fakeDirIterator) Verifier() uint64      { return 0 }
func (it *fakeDirIterator) Close()                {}
func (it *fakeDirIterator) Err() error            { return it.readErr }

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
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected NFS3ERR_NOENT, got: %v", err)
		}
	})

	t.Run("IteratorReadFails", func(t *testing.T) {
		mem := memfs.New()
		if err := mem.MkdirAll("/dir", 0755); err != nil {
			t.Fatal(err)
		}

		fakeHandler := newReadErrorFakeDirIteratorHandler(mem, errors.New("backend read failed"))
		target := serveAndMount(t, fakeHandler)

		if err := target.RmDir("/dir"); err == nil {
			t.Fatal("expected RmDir to fail")
		}
	})
}

// TestDirIteratorReadError proves READDIR and READDIRPLUS fail on a DirIterator
// read error rather than return a short listing marked EOF.
func TestDirIteratorReadError(t *testing.T) {
	readErr := errors.New("backend read failed")

	tests := []struct {
		name string
		op   func(*nfsc.Target) error
	}{
		{
			name: "ReadDir",
			op: func(target *nfsc.Target) error {
				_, err := readDir(target, "/dir")
				return err
			},
		},
		{
			name: "ReadDirPlus",
			op: func(target *nfsc.Target) error {
				_, err := target.ReadDirPlus("/dir")
				return err
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mem := memfs.New()
			if err := mem.MkdirAll("/dir", 0755); err != nil {
				t.Fatal(err)
			}

			fakeHandler := newReadErrorFakeDirIteratorHandler(mem, readErr)
			target := serveAndMount(t, fakeHandler)

			if err := tc.op(target); err == nil {
				t.Error("expected the iterator read error to surface")
			}
		})
	}
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

// erofsRenameFS is writable but fails every rename with EROFS.
type erofsRenameFS struct {
	billy.Filesystem
}

func (fs *erofsRenameFS) Rename(oldpath, newpath string) error {
	return &os.PathError{Op: "rename", Path: oldpath, Err: syscall.EROFS}
}

// TestOperationErrorKeepsItsStatus renames on a filesystem that reports itself
// writable but fails the rename with EROFS, and expects NFS3ERR_ROFS.
func TestOperationErrorKeepsItsStatus(t *testing.T) {
	mem := memfs.New()
	f, err := mem.Create("/from.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	handler := helpers.NewCachingHandler(helpers.NewNullAuthHandler(&erofsRenameFS{mem}), testCacheLimit)
	target := serveAndMount(t, handler)

	err = target.Rename("/from.txt", "/to.txt")
	if err == nil {
		t.Fatal("expected rename to fail")
	}
	nfsErr, ok := err.(*nfsc.Error)
	if !ok {
		t.Fatalf("expected *nfsc.Error, got %T: %v", err, err)
	}
	if nfsErr.ErrorNum != uint32(nfs.NFSStatusROFS) {
		t.Errorf("rename on read-only target reported %v (%d), want NFS3ERR_ROFS (%d)",
			nfsErr.ErrorString, nfsErr.ErrorNum, nfs.NFSStatusROFS)
	}
}

// TestRenameThroughRecoverPanics renames within one directory and across two,
// through the wrappers installed in production.  The filesystem reports an
// fsid, which is what lets onRename recognise it as one filesystem: the
// fallback for a filesystem reporting none compares the values RecoverPanics
// returns, and it hands out a fresh wrapper per call.
func TestRenameThroughRecoverPanics(t *testing.T) {
	mem := memfs.New()
	f, err := mem.Create("/from.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := mem.MkdirAll("/dir", 0755); err != nil {
		t.Fatal(err)
	}

	handler := helpers.NewCachingHandler(helpers.NewNullAuthHandler(&fixedFSID{mem, 1}), testCacheLimit)
	wrapped := helpers.RecoverPanics(handler, func(r any) { t.Errorf("unexpected panic: %v", r) })
	target := serveAndMount(t, wrapped)

	// Within one directory, as `mv from.txt to.txt` and Linux silly-rename do.
	if err := target.Rename("/from.txt", "/to.txt"); err != nil {
		t.Fatalf("same-directory rename: %v", err)
	}
	if _, err := mem.Stat("/to.txt"); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}

	// Across directories of the same filesystem.
	if err := target.Rename("/to.txt", "/dir/to.txt"); err != nil {
		t.Fatalf("cross-directory rename: %v", err)
	}
	if _, err := mem.Stat("/dir/to.txt"); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}
}

// fixedFSID reports one fsid for every entry, as a single file system does.
type fixedFSID struct {
	billy.Filesystem
	fsid uint64
}

type fsidInfo struct {
	os.FileInfo
	info file.FileInfo
}

func (i fsidInfo) Sys() any { return &i.info }

func (fs *fixedFSID) Stat(path string) (os.FileInfo, error) {
	info, err := fs.Filesystem.Stat(path)
	if err != nil {
		return nil, err
	}
	return fsidInfo{info, file.FileInfo{Nlink: 1, FSID: fs.fsid}}, nil
}

// twoExports resolves handles under "b" to a second file system, as a server
// exporting two file systems does.
type twoExports struct {
	nfs.Handler
	b billy.Filesystem
}

func (h *twoExports) FromHandle(fh []byte) (billy.Filesystem, []string, error) {
	fs, path, err := h.Handler.FromHandle(fh)
	if err == nil && len(path) > 0 && path[0] == "b" {
		fs = h.b
	}
	return fs, path, err
}

// RFC 1813 3.3.14 requires NFS3ERR_XDEV when the fsid of the two directories
// differs, and a rename within one fsid to proceed.
func TestRenameAcrossFSIDs(t *testing.T) {
	a, b := memfs.New(), memfs.New()
	for _, dir := range []string{"/a", "/b"} {
		if err := a.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.MkdirAll("/b", 0755); err != nil {
		t.Fatal(err)
	}
	f, err := a.Create("/a/from.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	caching := helpers.NewCachingHandler(helpers.NewNullAuthHandler(&fixedFSID{a, 1}), testCacheLimit)
	target := serveAndMount(t, &twoExports{caching, &fixedFSID{b, 2}})

	var nfsErr *nfsc.Error
	if err := target.Rename("/a/from.txt", "/b/from.txt"); err == nil {
		t.Fatal("expected a rename across fsids to fail")
	} else if !errors.As(err, &nfsErr) || nfsErr.ErrorNum != nfsc.NFS3ErrXDev {
		t.Fatalf("expected NFS3ERR_XDEV, got: %v", err)
	}
	if _, err := a.Stat("/a/from.txt"); err != nil {
		t.Fatalf("source lost after a rejected rename: %v", err)
	}

	if err := target.Rename("/a/from.txt", "/a/to.txt"); err != nil {
		t.Fatalf("rename within one fsid: %v", err)
	}
	if _, err := a.Stat("/a/to.txt"); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}
}

// TestReadDirPlus_Ordering verifies that ReadDirPlus with a DirIteratorHandler does not
// interleave calls to ToHandle or (L)Stat with directory iteration.
//
// If it did happen then ReadDirPlus could attempt a recursive read lock on the data structures
// of its Filesystem.  That would deadlock if another thread attempted to take a write lock.
func TestReadDirPlus_Ordering(t *testing.T) {
	fs := memfs.New()

	if err := fs.MkdirAll("/a", 0755); err != nil {
		t.Fatal(err)
	}
	f, err := fs.Create("/abc")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fs.MkdirAll("/b", 0755); err != nil {
		t.Fatal(err)
	}

	handler := newInterleavedOperationsBlockingHandler(t, fs)
	target := serveAndMount(t, handler)

	entities, err := target.ReadDirPlus("")
	if err != nil {
		t.Fatal(err)
	}
	// entities are already complete, just verify that we got the expected entries.
	names := make([]string, 0, len(entities))
	for _, entity := range entities {
		names = append(names, entity.FileName)
	}
	sort.StringSlice(names).Sort()

	if !reflect.DeepEqual(names, []string{"a", "abc", "b"}) {
		t.Errorf("Got filenames %+v != [a abc b]", names)
	}
}

// TestReadDir_Ordering verifies that ReadDir with a DirIteratorHandler does not interleave
// calls to (L)Stat (or ToHandle, for that matter) with directory iteration.
//
// If it did happen then ReadDir could attempt a recursive read lock on the data structures
// of its Filesystem.  That would deadlock if another thread attempted to take a write lock.
func TestReadDir_Ordering(t *testing.T) {
	fs := memfs.New()

	if err := fs.MkdirAll("/a", 0755); err != nil {
		t.Fatal(err)
	}
	f, err := fs.Create("/abc")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fs.MkdirAll("/b", 0755); err != nil {
		t.Fatal(err)
	}

	handler := newInterleavedOperationsBlockingHandler(t, fs)
	target := serveAndMount(t, handler)

	entities, err := readDir(target, "")
	if err != nil {
		t.Fatal(err)
	}
	// entities are already complete, just verify that we got the expected entries.
	names := make([]string, 0, len(entities))
	for _, entity := range entities {
		names = append(names, entity.FileName)
	}
	sort.StringSlice(names).Sort()

	if !reflect.DeepEqual(names, []string{"a", "abc", "b"}) {
		t.Errorf("Got filenames %+v != [a abc b]", names)
	}
}

// interleavedOperationsBlockingHandler wraps a Handler to fail on _any_ Stat or ToHandle calls
// while a directory iterator is open.  It is intended to verify single-thread correctness of
// ReadDir and ReadDirPlus and is not thread-safe.
type interleavedOperationsBlockingHandler struct {
	nfs.Handler
	T             *testing.T
	fs            billy.Filesystem
	iteratorDepth int
	err           error
}

func newInterleavedOperationsBlockingHandler(t *testing.T, fs billy.Filesystem) nfs.Handler {
	handler := helpers.NewNullAuthHandler(fs)
	handler = helpers.NewCachingHandler(handler, testCacheLimit)
	handler = &interleavedOperationsBlockingHandler{
		Handler: handler,
		fs:      fs,
		T:       t,
	}
	return handler
}

func (h *interleavedOperationsBlockingHandler) Mount(ctx context.Context, conn net.Conn, req nfs.MountRequest) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	status, fs, flavours := h.Handler.Mount(ctx, conn, req)
	return status, &depthTrackingFS{fs, h}, flavours
}

func (h *interleavedOperationsBlockingHandler) OpenDir(ctx context.Context, path string, _, _ uint64) (nfs.DirIterator, error) {
	// Fake a DirIterator on top of a vanilla filesystem.
	entries, err := h.fs.(billy.Dir).ReadDir(path)
	if err != nil {
		return nil, err
	}
	h.iteratorDepth++

	return &depthTrackingIterator{h, entries, -1}, nil
}

func (h *interleavedOperationsBlockingHandler) ToHandle(fs billy.Filesystem, path []string) []byte {
	h.checkRecursiveLock("ToHandle", filepath.Join(path...))
	handle := h.Handler.ToHandle(fs, path)
	return handle
}

func (h *interleavedOperationsBlockingHandler) IteratorClosed() {
	h.iteratorDepth--
}

func (h *interleavedOperationsBlockingHandler) checkRecursiveLock(op string, msgs ...string) {
	if h.iteratorDepth > 0 {
		h.T.Errorf("Interleaved %s operation may cause recursive locks (%v)", op, msgs)
	}
}

type depthTrackingIterator struct {
	handler *interleavedOperationsBlockingHandler
	entries []fs.FileInfo
	index   int
}

func (di *depthTrackingIterator) Close() {
	di.handler.IteratorClosed()
}

func (di *depthTrackingIterator) Next() bool {
	di.index++
	return di.index < len(di.entries)
}

func (di *depthTrackingIterator) Err() error {
	return nil
}

func (di *depthTrackingIterator) Handle() []byte {
	ret := make([]byte, 8)
	binary.BigEndian.PutUint64(ret, uint64(di.index))
	return ret
}

func (di *depthTrackingIterator) FileInfo() fs.FileInfo {
	return di.entries[di.index]
}

func (di *depthTrackingIterator) Cookie() uint64 {
	return uint64(di.index)
}

func (di *depthTrackingIterator) Verifier() uint64 {
	return 17
}

type depthTrackingFS struct {
	billy.Filesystem
	handler *interleavedOperationsBlockingHandler
}

func (dfs *depthTrackingFS) Stat(filename string) (fs.FileInfo, error) {
	dfs.handler.checkRecursiveLock("Stat", filename)
	return dfs.Filesystem.Stat(filename)
}

func (dfs *depthTrackingFS) Lstat(filename string) (fs.FileInfo, error) {
	dfs.handler.checkRecursiveLock("Lstat", filename)
	return dfs.Filesystem.Lstat(filename)
}

// readDirOnce issues exactly one READDIR, where readDir above loops to the end
// of the directory.  It reports the filesystem's own entries, leaving out the
// "." and ".." the server supplies itself, and whether the server said the
// listing was finished.
func readDirOnce(target *nfsc.Target, dir string, count uint32) ([]string, bool, error) {
	_, fh, err := target.Lookup(dir)
	if err != nil {
		return nil, false, err
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

	res, err := target.Call(&readDirArgs{
		Header: rpc.Header{
			Rpcvers: 2,
			Vers:    nfsc.Nfs3Vers,
			Prog:    nfsc.Nfs3Prog,
			Proc:    uint32(nfs.NFSProcedureReadDir),
			Cred:    rpc.AuthNull,
			Verf:    rpc.AuthNull,
		},
		Handle: fh,
		Count:  count,
	})
	if err != nil {
		return nil, false, err
	}

	status, err := xdr.ReadUint32(res)
	if err != nil {
		return nil, false, err
	}
	if err := nfsc.NFS3Error(status); err != nil {
		return nil, false, err
	}
	if err := xdr.Read(res, new(readDirListOK)); err != nil {
		return nil, false, err
	}

	var names []string
	for {
		var item readDirList
		if err := xdr.Read(res, &item); err != nil {
			return nil, false, err
		}
		if !item.IsSet {
			break
		}
		if item.Entry.FileName == "." || item.Entry.FileName == ".." {
			continue
		}
		names = append(names, item.Entry.FileName)
	}

	var eof bool
	if err := xdr.Read(res, &eof); err != nil {
		return nil, false, err
	}
	return names, eof, nil
}

// readDirFileIDs lists a directory and returns the fileid reported for each
// entry, keeping "." and "..", which the other readers here drop.
func readDirFileIDs(target *nfsc.Target, dir string) (map[string]uint64, error) {
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

	res, err := target.Call(&readDirArgs{
		Header: nfsHeader(nfs.NFSProcedureReadDir),
		Handle: fh,
		Count:  4096,
	})
	if err != nil {
		return nil, err
	}

	status, err := xdr.ReadUint32(res)
	if err != nil {
		return nil, err
	}
	if err := nfsc.NFS3Error(status); err != nil {
		return nil, err
	}
	if err := xdr.Read(res, new(readDirListOK)); err != nil {
		return nil, err
	}

	ids := make(map[string]uint64)
	for {
		var item readDirList
		if err := xdr.Read(res, &item); err != nil {
			return nil, err
		}
		if !item.IsSet {
			break
		}
		ids[item.Entry.FileName] = item.Entry.FileId
	}
	return ids, nil
}

// "." and ".." are in every listing, so they are the likeliest place to send a
// fileid of zero - which RFC 1813 3.3.16 tells a server to avoid, because UNIX
// clients give that value a meaning of its own.  READDIRPLUS reports the same
// two entries from the same place, which TestDirPrefixEntry* covers directly.
func TestListingDotEntriesHaveFileIDs(t *testing.T) {
	mem := memfs.New()
	if err := mem.MkdirAll("/sub", 0o755); err != nil {
		t.Fatal(err)
	}
	target := serveAndMount(t, helpers.NewCachingHandler(helpers.NewNullAuthHandler(mem), 1024))

	ids, err := readDirFileIDs(target, "/sub")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, name := range []string{".", ".."} {
		id, ok := ids[name]
		if !ok {
			t.Errorf("%s: missing from the listing", name)
			continue
		}
		if id == 0 {
			t.Errorf("%s: got fileid 0, want one the client can use", name)
		}
	}
}

// callStatus issues one raw request and returns the NFS status of its reply,
// for a procedure the client library does not expose.
func callStatus(target *nfsc.Target, args any) (uint32, error) {
	res, err := target.Call(args)
	if err != nil {
		return 0, err
	}
	return xdr.ReadUint32(res)
}

// nfsHeader is the RPC header every one of these requests carries.
func nfsHeader(proc nfs.NFSProcedure) rpc.Header {
	return rpc.Header{
		Rpcvers: 2,
		Vers:    nfsc.Nfs3Vers,
		Prog:    nfsc.Nfs3Prog,
		Proc:    uint32(proc),
		Cred:    rpc.AuthNull,
		Verf:    rpc.AuthNull,
	}
}

// A handle the filesystem no longer knows is answered NFS3ERR_STALE, including
// by a procedure whose only use of it is a best-effort read of its attributes.
// The server places a handle by its export alone, so a dead one reaches the
// procedure intact; replying NFS3_OK would tell the client the handle still
// works and leave it using it.  RFC 1813 lists NFS3ERR_STALE for each of these:
// 3.3.21 for COMMIT, 3.3.20 for PATHCONF, 3.3.3 for LOOKUP.
func TestStaleHandleIsRefused(t *testing.T) {
	mem := memfs.New()
	f, err := mem.Create("gone.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	target := serveAndMount(t, helpers.NewCachingHandler(helpers.NewNullAuthHandler(mem), 1024))

	_, fh, err := target.Lookup("/gone.txt")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	// Removed through the server, which is what drops the handle.  Removing
	// it underneath would leave the handle resolvable and the failure would
	// be ENOENT from the stat, not a stale handle.
	if err := target.Remove("/gone.txt"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	type commitArgs struct {
		rpc.Header
		Handle []byte
		Offset uint64
		Count  uint32
	}
	type handleArgs struct {
		rpc.Header
		Handle []byte
	}
	type lookupArgs struct {
		rpc.Header
		Handle []byte
		Name   string
	}

	for _, tc := range []struct {
		name string
		args any
	}{
		{"commit", &commitArgs{Header: nfsHeader(nfs.NFSProcedureCommit), Handle: fh}},
		{"pathconf", &handleArgs{Header: nfsHeader(nfs.NFSProcedurePathConf), Handle: fh}},
		{"lookup of .", &lookupArgs{Header: nfsHeader(nfs.NFSProcedureLookup), Handle: fh, Name: "."}},
		{"lookup of ..", &lookupArgs{Header: nfsHeader(nfs.NFSProcedureLookup), Handle: fh, Name: ".."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, err := callStatus(target, tc.args)
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if got, want := status, uint32(nfs.NFSStatusStale); got != want {
				t.Errorf("got status %d, want %d", got, want)
			}
		})
	}
}

// One reply may not report more entries than the handler can hold handles for.
// A listing mints a handle for every entry, and a handler keeping only so many
// drops the oldest to make room - so a reply carrying more entries than that
// would reach the client with the handles from its own first entries already
// gone.  The server stops short instead and says the listing has more to come,
// which the client asks for with the cookie of the last entry it was given.
func TestReadDirBoundedByHandleLimit(t *testing.T) {
	const handleLimit = 16
	mem := memfs.New()
	for i := 0; i < handleLimit*4; i++ {
		f, err := mem.Create(fmt.Sprintf("f%03d.txt", i))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	target := serveAndMount(t, helpers.NewCachingHandler(helpers.NewNullAuthHandler(mem), handleLimit))

	// A count large enough that the reply's byte budget cannot be what cuts
	// it short: what is under test is the handle limit, not the buffer.
	names, eof, err := readDirOnce(target, "/", 1<<20)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if want := handleLimit / 2; len(names) > want {
		t.Errorf("entries in one reply: got %d, want at most %d", len(names), want)
	}
	if eof {
		t.Error("got eof on a reply the handle limit cut short, want more to follow")
	}
}
