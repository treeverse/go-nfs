package helpers

import (
	"context"
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
