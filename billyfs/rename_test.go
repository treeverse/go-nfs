package billyfs_test

import (
	"context"
	"errors"
	"testing"

	billy "github.com/go-git/go-billy/v6"
	"github.com/treeverse/go-nfs/billyfs"
	"github.com/treeverse/go-nfs/fsapi"
	"github.com/treeverse/go-nfs/helpers"
	"github.com/treeverse/go-nfs/helpers/memfs"
)

// A rename between two filesystems is a move the filesystem cannot make, and
// NFS has a status that says exactly that.  billy reports no fsid, so the two
// directories are told apart by comparing the filesystems themselves, and that
// fallback has to give the same answer a mismatched fsid would.
func TestRenameAcrossFilesystems(t *testing.T) {
	ctx := context.Background()
	from, to := memfs.New(), memfs.New()
	// Both need an entry: a memfs with none has no root to stat, and the
	// rename reads the attributes of both directories before it compares
	// them.
	for _, dir := range []struct {
		fs   billy.Filesystem
		name string
	}{{from, "x.txt"}, {to, "z.txt"}} {
		f, err := dir.fs.Create(dir.name)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}

	handler := helpers.NewCachingHandler(helpers.NewNullAuthHandler(from), testCacheLimit)
	fsys := billyfs.New(handler, from)
	fromRoot := fsapi.Handle(handler.ToHandle(from, []string{}))
	toRoot := fsapi.Handle(handler.ToHandle(to, []string{}))

	if _, _, err := fsys.Rename(ctx, fromRoot, "x.txt", toRoot, "y.txt"); !errors.Is(err, fsapi.ErrXDev) {
		t.Errorf("got %v, want %v", err, fsapi.ErrXDev)
	}
}
