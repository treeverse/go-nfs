package billyfs_test

import (
	"context"
	"errors"
	iofs "io/fs"
	"net"
	"path"
	"sort"
	"strings"
	"testing"

	billy "github.com/go-git/go-billy/v6"
	nfs "github.com/treeverse/go-nfs"
	"github.com/treeverse/go-nfs/billyfs"
	"github.com/treeverse/go-nfs/fsapi"
	"github.com/treeverse/go-nfs/helpers"
	"github.com/treeverse/go-nfs/helpers/memfs"
)

const testCacheLimit = 1024

// splitPath turns a path billyfs joined back into the components a Handler
// keeps handles under.
func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// dirIteratorHandler gives a Handler a DirIteratorHandler, by streaming a
// listing it reads whole.  billyfs takes a different path through ReadDir for a
// Handler that has one, and the two must agree about what they report.
type dirIteratorHandler struct {
	nfs.Handler
	fs billy.Filesystem
}

func (h *dirIteratorHandler) OpenDir(ctx context.Context, p string, cookie, verifier uint64) (nfs.DirIterator, error) {
	entries, err := h.fs.ReadDir(p)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return &sliceIterator{
		handler: h.Handler,
		fs:      h.fs,
		dir:     splitPath(p),
		entries: entries,
		index:   -1,
	}, nil
}

type sliceIterator struct {
	handler nfs.Handler
	fs      billy.Filesystem
	dir     []string
	entries []iofs.FileInfo
	index   int
}

func (s *sliceIterator) Next() bool {
	s.index++
	return s.index < len(s.entries)
}

func (s *sliceIterator) FileInfo() iofs.FileInfo { return s.entries[s.index] }

func (s *sliceIterator) Handle() []byte {
	entry := make([]string, 0, len(s.dir)+1)
	entry = append(entry, s.dir...)
	entry = append(entry, s.entries[s.index].Name())
	return s.handler.ToHandle(s.fs, entry)
}

func (s *sliceIterator) Cookie() uint64   { return uint64(s.index) }
func (s *sliceIterator) Verifier() uint64 { return 1 }
func (s *sliceIterator) Err() error       { return nil }
func (s *sliceIterator) Close()           {}

// noCacheHandler is a Handler that keeps no listings.  billyfs then computes
// the verifier itself from the directory as it is now, rather than handing back
// the listing a cookie was issued against, which is what makes a stale cookie
// visible.  It forwards every Handler method and deliberately does not expose
// CachingHandler, so the methods are spelled out instead of embedded.
type noCacheHandler struct {
	inner nfs.Handler
}

func (h *noCacheHandler) Mount(ctx context.Context, conn net.Conn, req nfs.MountRequest) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	return h.inner.Mount(ctx, conn, req)
}

func (h *noCacheHandler) Change(fs billy.Filesystem) billy.Change { return h.inner.Change(fs) }

func (h *noCacheHandler) FSStat(ctx context.Context, fs billy.Filesystem, s *nfs.FSStat) error {
	return h.inner.FSStat(ctx, fs, s)
}

func (h *noCacheHandler) ToHandle(fs billy.Filesystem, path []string) []byte {
	return h.inner.ToHandle(fs, path)
}

func (h *noCacheHandler) FromHandle(fh []byte) (billy.Filesystem, []string, error) {
	return h.inner.FromHandle(fh)
}

func (h *noCacheHandler) InvalidateHandle(fs billy.Filesystem, fh []byte) error {
	return h.inner.InvalidateHandle(fs, fh)
}

func (h *noCacheHandler) HandleLimit() int { return h.inner.HandleLimit() }

// A cookie into a listing read whole is an index into it, so it means something
// else once the set of names changes.  Resuming on one then skips or repeats
// entries silently, which is what the verifier is there to catch: the listing
// must be refused rather than resumed.
func TestReadDirStaleCookie(t *testing.T) {
	ctx := context.Background()
	mem := memfs.New()
	for _, name := range []string{"b.txt", "c.txt", "d.txt"} {
		f, err := mem.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}

	handler := &noCacheHandler{helpers.NewCachingHandler(helpers.NewNullAuthHandler(mem), testCacheLimit)}
	fsys := billyfs.New(handler, mem)
	root := fsapi.Handle(handler.ToHandle(mem, []string{}))

	// Read one entry, keeping the cookie that resumes after it and the
	// verifier the listing reported.
	it, err := fsys.ReadDir(ctx, root, 0, 0)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if !it.Next(ctx) {
		t.Fatal("listing is empty")
	}
	cookie, verifier := it.Entry().Cookie, it.Verifier()
	if err := it.Close(); err != nil {
		t.Errorf("closing iterator: %v", err)
	}

	// The directory has not changed, so the listing resumes.
	resumed, err := fsys.ReadDir(ctx, root, cookie, verifier)
	if err != nil {
		t.Fatalf("resuming an unchanged listing: %v", err)
	}
	if err := resumed.Close(); err != nil {
		t.Errorf("closing iterator: %v", err)
	}

	// Adding a name ahead of the others moves every entry the cookie could
	// name, so the same cookie must now be refused.
	f, err := mem.Create("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	stale, err := fsys.ReadDir(ctx, root, cookie, verifier)
	if !errors.Is(err, fsapi.ErrStaleCookie) {
		t.Errorf("resuming a changed listing: got %v, want %v", err, fsapi.ErrStaleCookie)
		if err == nil {
			if err := stale.Close(); err != nil {
				t.Errorf("closing iterator: %v", err)
			}
		}
	}
}

// listedEntry is what a listing reported about one entry, kept so that the
// filesystem can be asked about it again once the listing is closed.
type listedEntry struct {
	handle fsapi.Handle
	fileID uint64
}

// listOne returns what a listing of dir says about the entry called name.
func listOne(ctx context.Context, t *testing.T, fsys fsapi.FS, dir fsapi.Handle, name string) listedEntry {
	t.Helper()
	it, err := fsys.ReadDir(ctx, dir, 0, 0)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var (
		found bool
		entry listedEntry
	)
	for it.Next(ctx) {
		e := it.Entry()
		if e.Name != name {
			continue
		}
		found, entry = true, listedEntry{handle: e.Handle, fileID: e.Attrs.Fileid}
	}
	if err := it.Err(); err != nil {
		t.Errorf("iterating: %v", err)
	}
	if err := it.Close(); err != nil {
		t.Errorf("closing iterator: %v", err)
	}
	if !found {
		t.Fatalf("%s: missing from the listing", name)
	}
	return entry
}

// A fileid identifies an object within its filesystem, so two files may not
// share one however alike their names are, and a listing must report the same
// fileid that a stat of the same object does.  billyfs has two listing paths -
// one for a Handler that streams entries, one for a Handler whose listing it
// reads whole - and this holds for both.
func TestReadDirFileIDs(t *testing.T) {
	for _, tc := range []struct {
		name string
		wrap func(nfs.Handler, billy.Filesystem) nfs.Handler
	}{
		{
			name: "listing read whole",
			wrap: func(h nfs.Handler, _ billy.Filesystem) nfs.Handler { return h },
		},
		{
			name: "streamed listing",
			wrap: func(h nfs.Handler, bfs billy.Filesystem) nfs.Handler {
				return &dirIteratorHandler{h, bfs}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			mem := memfs.New()
			// The same basename under two directories: nothing but the
			// directory tells the two files apart.
			for _, dir := range []string{"a", "b"} {
				if err := mem.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				f, err := mem.Create(path.Join(dir, "x.txt"))
				if err != nil {
					t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			}

			handler := tc.wrap(helpers.NewCachingHandler(helpers.NewNullAuthHandler(mem), testCacheLimit), mem)
			fsys := billyfs.New(handler, mem)
			root := fsapi.Handle(handler.ToHandle(mem, []string{}))

			listed := make(map[string]listedEntry, 2)
			for _, dir := range []string{"a", "b"} {
				dirHandle, _, _, err := fsys.Lookup(ctx, root, dir)
				if err != nil {
					t.Fatalf("lookup %s: %v", dir, err)
				}
				listed[dir] = listOne(ctx, t, fsys, dirHandle, "x.txt")
			}

			if listed["a"].fileID == listed["b"].fileID {
				t.Errorf("a/x.txt and b/x.txt share fileid %d", listed["a"].fileID)
			}

			// Asked once the listing is closed, so that nothing here
			// depends on a filesystem tolerating a stat mid-listing.
			for _, dir := range []string{"a", "b"} {
				attrs, err := fsys.GetAttr(ctx, listed[dir].handle)
				if err != nil {
					t.Fatalf("getattr %s/x.txt: %v", dir, err)
				}
				if got, want := listed[dir].fileID, attrs.Fileid; got != want {
					t.Errorf("%s/x.txt fileid: listing says %d, getattr says %d", dir, got, want)
				}
			}
		})
	}
}
