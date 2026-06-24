package helpers

import (
	"context"
	"net"
	"os"

	"github.com/go-git/go-billy/v6"
	"github.com/treeverse/go-nfs"
)

// OnPanic is a function called when a panic is recovered.
type OnPanic func(any)

// RecoverPanics wraps a handler to recover from panics in its method calls.
// It also wraps any billy.Filesystem returned by the handler's Mount or FromHandle methods
// to ensure panics in the filesystem implementation are caught.
// If h also implements nfs.DirIteratorHandler, the returned handler will too.
func RecoverPanics(h nfs.Handler, onPanic OnPanic) nfs.Handler {
	base := &recoveryHandler{handler: h, onPanic: onPanic}
	if _, ok := h.(nfs.DirIteratorHandler); ok {
		return &recoveryHandlerWithDirIterator{recoveryHandler: base}
	}
	return base
}

// recoveryHandler wraps an nfs.Handler with panic recovery.
// It uses explicit delegation (not embedding) so the compiler forces
// us to implement any new methods added to the interface.
type recoveryHandler struct {
	handler nfs.Handler
	onPanic OnPanic
}

// recoveryHandlerWithDirIterator extends recoveryHandler for handlers that also
// implement nfs.DirIteratorHandler. It is only returned when the wrapped handler
// actually supports streaming directory listings, so the NFS server will correctly
// detect (or not detect) the optional interface.
type recoveryHandlerWithDirIterator struct {
	*recoveryHandler
}

func (h *recoveryHandler) recover() {
	if r := recover(); r != nil {
		h.onPanic(r)
	}
}

func (h *recoveryHandler) Mount(ctx context.Context, conn net.Conn, req nfs.MountRequest) (status nfs.MountStatus, hndl billy.Filesystem, auths []nfs.AuthFlavor) {
	defer h.recover()
	status, hndl, auths = h.handler.Mount(ctx, conn, req)
	if hndl != nil {
		hndl = &recoveryFilesystem{Filesystem: hndl, onPanic: h.onPanic}
	}
	return
}

func (h *recoveryHandler) Change(fs billy.Filesystem) billy.Change {
	defer h.recover()
	// If fs is our wrapped fs, unwrap it for the underlying handler
	if rf, ok := fs.(*recoveryFilesystem); ok {
		fs = rf.Filesystem
	}
	return h.handler.Change(fs)
}

func (h *recoveryHandler) FSStat(ctx context.Context, fs billy.Filesystem, stat *nfs.FSStat) error {
	defer h.recover()
	if rf, ok := fs.(*recoveryFilesystem); ok {
		fs = rf.Filesystem
	}
	return h.handler.FSStat(ctx, fs, stat)
}

func (h *recoveryHandler) ToHandle(fs billy.Filesystem, path []string) []byte {
	defer h.recover()
	if rf, ok := fs.(*recoveryFilesystem); ok {
		fs = rf.Filesystem
	}
	return h.handler.ToHandle(fs, path)
}

func (h *recoveryHandler) FromHandle(fh []byte) (billy.Filesystem, []string, error) {
	defer h.recover()
	fs, path, err := h.handler.FromHandle(fh)
	if fs != nil {
		fs = &recoveryFilesystem{Filesystem: fs, onPanic: h.onPanic}
	}
	return fs, path, err
}

func (h *recoveryHandler) InvalidateHandle(fs billy.Filesystem, fh []byte) error {
	defer h.recover()
	if rf, ok := fs.(*recoveryFilesystem); ok {
		fs = rf.Filesystem
	}
	return h.handler.InvalidateHandle(fs, fh)
}

func (h *recoveryHandler) HandleLimit() int {
	defer h.recover()
	return h.handler.HandleLimit()
}

// OpenDir implements nfs.DirIteratorHandler for handlers that support streaming
// directory listings. This method only exists on recoveryHandlerWithDirIterator,
// so the NFS server's optional-interface detection works correctly.
func (h *recoveryHandlerWithDirIterator) OpenDir(ctx context.Context, path string, cookie, verifier uint64) (nfs.DirIterator, error) {
	defer h.recover()
	// h.handler is a DirIteratorHandler by construction.
	return h.handler.(nfs.DirIteratorHandler).OpenDir(ctx, path, cookie, verifier)
}

// recoveryFilesystem wraps a billy.Filesystem with panic recovery.
// It uses explicit delegation (not embedding) so the compiler forces
// us to implement any new methods added to the interface.
type recoveryFilesystem struct {
	Filesystem billy.Filesystem
	onPanic    OnPanic
}

func (fs *recoveryFilesystem) recover() {
	if r := recover(); r != nil {
		fs.onPanic(r)
	}
}

func (fs *recoveryFilesystem) Create(filename string) (billy.File, error) {
	defer fs.recover()
	return fs.Filesystem.Create(filename)
}

func (fs *recoveryFilesystem) Open(filename string) (billy.File, error) {
	defer fs.recover()
	return fs.Filesystem.Open(filename)
}

func (fs *recoveryFilesystem) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	defer fs.recover()
	return fs.Filesystem.OpenFile(filename, flag, perm)
}

func (fs *recoveryFilesystem) Stat(filename string) (os.FileInfo, error) {
	defer fs.recover()
	return fs.Filesystem.Stat(filename)
}

func (fs *recoveryFilesystem) Rename(oldpath, newpath string) error {
	defer fs.recover()
	return fs.Filesystem.Rename(oldpath, newpath)
}

func (fs *recoveryFilesystem) Remove(filename string) error {
	defer fs.recover()
	return fs.Filesystem.Remove(filename)
}

func (fs *recoveryFilesystem) Join(elem ...string) string {
	defer fs.recover()
	return fs.Filesystem.Join(elem...)
}

func (fs *recoveryFilesystem) TempFile(dir, prefix string) (billy.File, error) {
	defer fs.recover()
	return fs.Filesystem.TempFile(dir, prefix)
}

func (fs *recoveryFilesystem) ReadDir(path string) ([]os.FileInfo, error) {
	defer fs.recover()
	return fs.Filesystem.ReadDir(path)
}

func (fs *recoveryFilesystem) MkdirAll(filename string, perm os.FileMode) error {
	defer fs.recover()
	return fs.Filesystem.MkdirAll(filename, perm)
}

func (fs *recoveryFilesystem) Lstat(filename string) (os.FileInfo, error) {
	defer fs.recover()
	return fs.Filesystem.Lstat(filename)
}

func (fs *recoveryFilesystem) Symlink(target, link string) error {
	defer fs.recover()
	return fs.Filesystem.Symlink(target, link)
}

func (fs *recoveryFilesystem) Readlink(link string) (string, error) {
	defer fs.recover()
	return fs.Filesystem.Readlink(link)
}

func (fs *recoveryFilesystem) Chroot(path string) (billy.Filesystem, error) {
	defer fs.recover()
	bfs, err := fs.Filesystem.Chroot(path)
	if bfs != nil {
		bfs = &recoveryFilesystem{Filesystem: bfs, onPanic: fs.onPanic}
	}
	return bfs, err
}

func (fs *recoveryFilesystem) Root() string {
	defer fs.recover()
	return fs.Filesystem.Root()
}
