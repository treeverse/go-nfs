package protocol

import (
	"context"
	"testing"

	"github.com/treeverse/go-nfs/fsapi"
)

// keepsEverything is a filesystem whose handles live as long as the mount, and
// which therefore implements no fsapi.HandleKeeper.
type keepsEverything struct {
	fsapi.FS
}

// keepsSome is a filesystem that holds only so many handles at once.
type keepsSome struct {
	fsapi.FS
	limit int
}

func (k keepsSome) HandleLimit() int { return k.limit }

// parentWithoutAttrs reports the parent of a directory but not its attributes,
// which LookupParent allows: that return is a post_op_attr.  Everything else it
// embeds is nil, so any other call panics - which is what proves "." asks the
// filesystem nothing.
type parentWithoutAttrs struct {
	fsapi.FS
	parent fsapi.Handle
	fileid uint64
}

func (p parentWithoutAttrs) LookupParent(context.Context, fsapi.Handle) (fsapi.Handle, *fsapi.Attrs, error) {
	return p.parent, nil, nil
}

func (p parentWithoutAttrs) GetAttr(_ context.Context, h fsapi.Handle) (fsapi.Attrs, error) {
	if h != p.parent {
		return fsapi.Attrs{}, fsapi.ErrStale
	}
	return fsapi.Attrs{Fileid: p.fileid}, nil
}

// ".." needs a fileid, and a filesystem may report the parent without its
// attributes.  The server asks for them rather than send the zero fileid RFC
// 1813 3.3.16 tells it to avoid.
func TestDirPrefixEntryParentReportsNoAttrs(t *testing.T) {
	const parentFileid = 4242
	fs := parentWithoutAttrs{parent: "the-parent", fileid: parentFileid}

	fileID, cookie, attrs := dirPrefixEntry(context.Background(), fs, "the-dir", nil, "..")
	if got, want := fileID, uint64(parentFileid); got != want {
		t.Errorf("fileid: got %d, want %d", got, want)
	}
	if got, want := cookie, uint64(dotDotCookie); got != want {
		t.Errorf("cookie: got %d, want %d", got, want)
	}
	if attrs == nil {
		t.Error("got no attributes to report with the entry, want the parent's")
	}
}

// "." is the directory being listed, whose attributes the handler has already
// read.  It reports those, and asks the filesystem nothing: the embedded
// fsapi.FS here is nil, so a call would panic.
func TestDirPrefixEntryDotAsksNothing(t *testing.T) {
	const dirFileid = 99
	dirAttrs := fsapi.Attrs{Fileid: dirFileid}

	fileID, cookie, attrs := dirPrefixEntry(context.Background(), keepsEverything{}, "the-dir", &dirAttrs, ".")
	if got, want := fileID, uint64(dirFileid); got != want {
		t.Errorf("fileid: got %d, want %d", got, want)
	}
	if got, want := cookie, uint64(dotCookie); got != want {
		t.Errorf("cookie: got %d, want %d", got, want)
	}
	if attrs != &dirAttrs {
		t.Error("got attributes other than the ones it was handed")
	}
}

// A listing is bounded by what the filesystem can keep, so that the handles in
// one reply are all still alive when the client reads it.
func TestMaxListEntries(t *testing.T) {
	for _, tc := range []struct {
		name string
		fs   fsapi.FS
		want int
	}{
		{"a filesystem that keeps every handle is unbounded", keepsEverything{}, 0},
		{"half the limit, leaving the rest for handles the client holds", keepsSome{limit: 1024}, 512},
		{"an odd limit rounds down", keepsSome{limit: 9}, 4},
		{"a limit of two still reports one entry", keepsSome{limit: 2}, 1},
		{"a limit too small to halve bounds at one, not at none", keepsSome{limit: 1}, 1},
		{"a limit of zero is still a limit", keepsSome{limit: 0}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := maxListEntries(tc.fs); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}
