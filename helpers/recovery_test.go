package helpers

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"

	"github.com/go-git/go-billy/v6"
	"github.com/go-git/go-billy/v6/memfs"
	"github.com/treeverse/go-nfs"
)

type panicHandler struct {
	NullAuthHandler
}

func (h *panicHandler) Mount(ctx context.Context, conn net.Conn, req nfs.MountRequest) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	panic("mount panic")
}

func TestRecoverPanics(t *testing.T) {
	fs := memfs.New()
	handler := &panicHandler{NullAuthHandler{fs}}

	var panicked any
	wrapped := RecoverPanics(handler, func(r any) {
		panicked = r
	})

	_, _, _ = wrapped.Mount(context.Background(), nil, nfs.MountRequest{})
	if panicked != "mount panic" {
		t.Errorf("expected panic 'mount panic', got %v", panicked)
	}
}

type panicFS struct {
	billy.Filesystem
}

func (fs *panicFS) Stat(path string) (os.FileInfo, error) {
	panic("stat panic")
}

func TestRecoverPanicsFS(t *testing.T) {
	fs := &panicFS{memfs.New()}
	handler := NewNullAuthHandler(fs)

	var panicked any
	wrapped := RecoverPanics(handler, func(r any) {
		panicked = r
	})

	_, wrappedFS, _ := wrapped.Mount(context.Background(), nil, nfs.MountRequest{})
	_, _ = wrappedFS.Stat("foo")

	if panicked != "stat panic" {
		t.Errorf("expected panic 'stat panic', got %v", panicked)
	}
}

// panicOpsFS panics in filesystem operations. A recovered panic here must
// surface as ErrRecoveredPanic rather than a zero-value "success" (e.g. Open
// returning a nil file with a nil error, which the caller would dereference).
type panicOpsFS struct {
	billy.Filesystem
}

func (fs *panicOpsFS) Open(filename string) (billy.File, error)  { panic("open panic") }
func (fs *panicOpsFS) Stat(filename string) (os.FileInfo, error) { panic("stat panic") }
func (fs *panicOpsFS) Remove(filename string) error              { panic("remove panic") }

func TestRecoverPanicsFilesystemError(t *testing.T) {
	tests := []struct {
		name      string
		wantPanic string
		op        func(billy.Filesystem) error
	}{
		{
			name:      "Open",
			wantPanic: "open panic",
			op: func(fs billy.Filesystem) error {
				_, err := fs.Open("foo")
				return err
			},
		},
		{
			name:      "Stat",
			wantPanic: "stat panic",
			op: func(fs billy.Filesystem) error {
				_, err := fs.Stat("foo")
				return err
			},
		},
		{
			name:      "Remove",
			wantPanic: "remove panic",
			op: func(fs billy.Filesystem) error {
				return fs.Remove("foo")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := NewNullAuthHandler(&panicOpsFS{memfs.New()})

			var panicked any
			wrapped := RecoverPanics(handler, func(r any) {
				panicked = r
			})

			_, wrappedFS, _ := wrapped.Mount(context.Background(), nil, nfs.MountRequest{})
			err := tc.op(wrappedFS)

			if panicked != tc.wantPanic {
				t.Errorf("expected panic %q, got %v", tc.wantPanic, panicked)
			}
			if !errors.Is(err, ErrRecoveredPanic) {
				t.Errorf("expected ErrRecoveredPanic, got %v", err)
			}
		})
	}
}

// panicDirIterator panics in the streaming-listing methods go-nfs invokes
// directly on the iterator returned by OpenDir.
type panicDirIterator struct {
	nfs.DirIterator
}

func (it *panicDirIterator) Next() bool { panic("next panic") }
func (it *panicDirIterator) Close()     { panic("close panic") }

// dirIteratorHandler implements nfs.DirIteratorHandler, handing back an iterator
// whose methods panic.
type dirIteratorHandler struct {
	nfs.Handler
}

func (h *dirIteratorHandler) OpenDir(ctx context.Context, path string, cookie, verifier uint64) (nfs.DirIterator, error) {
	return &panicDirIterator{}, nil
}

func TestRecoverPanicsDirIterator(t *testing.T) {
	handler := &dirIteratorHandler{NewNullAuthHandler(memfs.New())}

	var panicked any
	wrapped := RecoverPanics(handler, func(r any) {
		panicked = r
	})

	dih, ok := wrapped.(nfs.DirIteratorHandler)
	if !ok {
		t.Fatal("wrapped handler must implement nfs.DirIteratorHandler")
	}
	it, err := dih.OpenDir(context.Background(), "/", 0, 0)
	if err != nil {
		t.Fatalf("OpenDir: %v", err)
	}

	if it.Next() {
		t.Error("Next() should return false after recovering a panic")
	}
	if panicked != "next panic" {
		t.Errorf("expected panic 'next panic', got %v", panicked)
	}
}

// panicFile panics in the file operations go-nfs invokes directly on the file
// returned by Open/OpenFile (ReadAt for reads, WriteAt for writes).
type panicFile struct {
	billy.File
}

func (f *panicFile) ReadAt(p []byte, off int64) (int, error)  { panic("readat panic") }
func (f *panicFile) WriteAt(p []byte, off int64) (int, error) { panic("writeat panic") }

// panicFileFS returns a panicFile from Open, so the wrapped filesystem hands
// back a file whose operations panic.
type panicFileFS struct {
	billy.Filesystem
}

func (fs *panicFileFS) Open(filename string) (billy.File, error) {
	return &panicFile{}, nil
}

func TestRecoverPanicsFile(t *testing.T) {
	tests := []struct {
		name      string
		wantPanic string
		op        func(billy.File) error
	}{
		{
			name:      "ReadAt",
			wantPanic: "readat panic",
			op: func(f billy.File) error {
				_, err := f.ReadAt(make([]byte, 4), 0)
				return err
			},
		},
		{
			name:      "WriteAt",
			wantPanic: "writeat panic",
			op: func(f billy.File) error {
				_, err := f.WriteAt(make([]byte, 4), 0)
				return err
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := &panicFileFS{memfs.New()}
			handler := NewNullAuthHandler(fs)

			var panicked any
			wrapped := RecoverPanics(handler, func(r any) {
				panicked = r
			})

			_, wrappedFS, _ := wrapped.Mount(context.Background(), nil, nfs.MountRequest{})
			f, err := wrappedFS.Open("foo")
			if err != nil {
				t.Fatalf("Open: %v", err)
			}

			err = tc.op(f)
			if panicked != tc.wantPanic {
				t.Errorf("expected panic %q, got %v", tc.wantPanic, panicked)
			}
			if !errors.Is(err, ErrRecoveredPanic) {
				t.Errorf("expected ErrRecoveredPanic, got %v", err)
			}
		})
	}
}
