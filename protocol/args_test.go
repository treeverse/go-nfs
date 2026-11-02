package protocol

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/treeverse/go-nfs/fsapi"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

// recordsArgs is a filesystem that records the arguments of ACCESS and
// SETATTR.  Everything else it embeds is nil, so any other call panics.
type recordsArgs struct {
	fsapi.FS
	handle fsapi.Handle
	want   fsapi.AccessMask
	change fsapi.AttrChange
	guard  *time.Time
}

func (r *recordsArgs) Access(_ context.Context, h fsapi.Handle, want fsapi.AccessMask) (fsapi.AccessMask, *fsapi.Attrs, error) {
	r.handle, r.want = h, want
	return want, nil, nil
}

func (r *recordsArgs) SetAttr(_ context.Context, h fsapi.Handle, change fsapi.AttrChange, guard *time.Time) (fsapi.Wcc, error) {
	r.handle, r.change, r.guard = h, change, guard
	return fsapi.Wcc{}, nil
}

// forEachHandleSize runs f as a subtest for a handle of every size a filesystem
// may return, so its wire form needs every amount of XDR padding.
func forEachHandleSize(t *testing.T, f func(t *testing.T, size int)) {
	for size := 0; size <= fsapi.MaxHandleSize; size++ {
		t.Run(fmt.Sprintf("handle_%d_bytes_wire_%d_bytes", size, size+exportIDSize), func(t *testing.T) {
			f(t, size)
		})
	}
}

// newArgsRequest returns a response for a request whose body is the wire form
// of h within a fresh export of fs, followed by rest.
func newArgsRequest(t *testing.T, fs fsapi.FS, h fsapi.Handle, rest ...any) *response {
	t.Helper()
	s := &Server{}
	id, err := s.exports.idFor(fs)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := encodeHandle(id, h)
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	if err := xdr.Write(&body, wire); err != nil {
		t.Fatal(err)
	}
	for _, v := range rest {
		if err := xdr.Write(&body, v); err != nil {
			t.Fatal(err)
		}
	}
	return &response{
		conn:     &conn{Server: s},
		writer:   &bytes.Buffer{},
		errorFmt: basicErrorFormatter,
		req:      &request{Body: &body},
	}
}

// The arguments after a file handle reach the filesystem as the client sent
// them, whatever padding the handle needs.
func TestAccessArgsAfterHandle(t *testing.T) {
	const mask = fsapi.AccessMask(0x3f)
	forEachHandleSize(t, func(t *testing.T, size int) {
		fs := &recordsArgs{}
		h := fsapi.Handle(strings.Repeat("h", size))
		w := newArgsRequest(t, fs, h, uint32(mask))

		if err := onAccess(context.Background(), w); err != nil {
			t.Fatal(err)
		}
		if fs.handle != h {
			t.Errorf("handle: got %q, want %q", fs.handle, h)
		}
		if fs.want != mask {
			t.Errorf("mask: got %#x, want %#x", fs.want, mask)
		}
	})
}

func TestSetAttrArgsAfterHandle(t *testing.T) {
	const (
		perm     = uint32(0o640)
		gid      = uint32(1234)
		fileSize = uint64(1) << 33
	)
	mtime := FileTime{Seconds: 1700000000, Nseconds: 5}
	guard := FileTime{Seconds: 1600000000, Nseconds: 7}
	forEachHandleSize(t, func(t *testing.T, size int) {
		fs := &recordsArgs{}
		h := fsapi.Handle(strings.Repeat("h", size))
		w := newArgsRequest(t, fs, h,
			// sattr3: mode, no uid, gid, size, atime untouched, mtime given.
			uint32(1), perm,
			uint32(0),
			uint32(1), gid,
			uint32(1), fileSize,
			uint32(0),
			uint32(2), mtime,
			// sattrguard3
			uint32(1), guard,
		)

		if err := onSetAttr(context.Background(), w); err != nil {
			t.Fatal(err)
		}
		if fs.handle != h {
			t.Errorf("handle: got %q, want %q", fs.handle, h)
		}
		got := fs.change
		if got.Perm == nil || *got.Perm != perm {
			t.Errorf("perm: got %v, want %o", got.Perm, perm)
		}
		if got.UID != nil {
			t.Errorf("uid: got %d, want none", *got.UID)
		}
		if got.GID == nil || *got.GID != gid {
			t.Errorf("gid: got %v, want %d", got.GID, gid)
		}
		if got.Size == nil || *got.Size != fileSize {
			t.Errorf("size: got %v, want %d", got.Size, fileSize)
		}
		if got.Atime != nil {
			t.Errorf("atime: got %v, want none", *got.Atime)
		}
		if got.Mtime == nil || !got.Mtime.Equal(*mtime.Native()) {
			t.Errorf("mtime: got %v, want %v", got.Mtime, mtime.Native())
		}
		if fs.guard == nil || !fs.guard.Equal(*guard.Native()) {
			t.Errorf("guard: got %v, want %v", fs.guard, guard.Native())
		}
	})
}

// recordsLink is recordsArgs that also records the arguments of LINK.
type recordsLink struct {
	recordsArgs
	dir  fsapi.Handle
	name string
}

func (r *recordsLink) Link(_ context.Context, h, dir fsapi.Handle, name string) (*fsapi.Attrs, fsapi.Wcc, error) {
	r.handle, r.dir, r.name = h, dir, name
	return nil, fsapi.Wcc{}, nil
}

func TestLinkArgsAfterHandle(t *testing.T) {
	const name = "new-name"
	forEachHandleSize(t, func(t *testing.T, size int) {
		fs := &recordsLink{}
		h := fsapi.Handle(strings.Repeat("h", size))
		dir := fsapi.Handle(strings.Repeat("d", size))
		// Both handles are in the export newArgsRequest creates, id 0.
		dirWire, err := encodeHandle(0, dir)
		if err != nil {
			t.Fatal(err)
		}
		w := newArgsRequest(t, fs, h, DirOpArg{Handle: dirWire, Filename: []byte(name)})

		if err := onLink(context.Background(), w); err != nil {
			t.Fatal(err)
		}
		if fs.handle != h {
			t.Errorf("handle: got %q, want %q", fs.handle, h)
		}
		if fs.dir != dir {
			t.Errorf("dir: got %q, want %q", fs.dir, dir)
		}
		if fs.name != name {
			t.Errorf("name: got %q, want %q", fs.name, name)
		}
	})
}

// Each opaque leaves the reader at the next one, whatever its length.
func TestReadOpaqueWithPadding(t *testing.T) {
	var want []string
	for size := range 36 {
		want = append(want, strings.Repeat("x", size))
	}
	var body bytes.Buffer
	for _, s := range want {
		if err := xdr.Write(&body, []byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range want {
		got, err := readOpaqueWithPadding(&body)
		if err != nil {
			t.Fatalf("reading %q: %v", s, err)
		}
		if string(got) != s {
			t.Errorf("got %q, want %q", got, s)
		}
	}
	if body.Len() != 0 {
		t.Errorf("%d bytes left over, want none", body.Len())
	}
}
