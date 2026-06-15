package helpers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/go-git/go-billy/v6"
	"github.com/treeverse/go-nfs"
)

// OnPanic is a function called when a panic is recovered.
type OnPanic func(any)

// ErrRecoveredPanic is returned by a billy.File operation that recovered from a
// panic, so a panicking read or write surfaces as an error to the caller
// instead of an unwound stack.
var ErrRecoveredPanic = errors.New("recovered panic")

// RecoverPanics wraps a handler to recover from panics in its method calls.
// It also wraps any billy.Filesystem returned by the handler's Mount or FromHandle
// methods, and any billy.File those filesystems return, so panics in the
// filesystem or file implementation are caught rather than crashing the server.
func RecoverPanics(h nfs.Handler, onPanic OnPanic) nfs.Handler {
	return &recoveryHandler{
		handler: h,
		onPanic: onPanic,
	}
}

// recoveryHandler wraps an nfs.Handler with panic recovery.
// It uses explicit delegation (not embedding) so the compiler forces
// us to implement any new methods added to the interface.
type recoveryHandler struct {
	handler nfs.Handler
	onPanic OnPanic
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

func (fs *recoveryFilesystem) wrapFile(f billy.File) billy.File {
	if f == nil {
		return nil
	}
	return &recoveryFile{File: f, onPanic: fs.onPanic}
}

func (fs *recoveryFilesystem) Create(filename string) (billy.File, error) {
	defer fs.recover()
	f, err := fs.Filesystem.Create(filename)
	return fs.wrapFile(f), err
}

func (fs *recoveryFilesystem) Open(filename string) (billy.File, error) {
	defer fs.recover()
	f, err := fs.Filesystem.Open(filename)
	return fs.wrapFile(f), err
}

func (fs *recoveryFilesystem) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	defer fs.recover()
	f, err := fs.Filesystem.OpenFile(filename, flag, perm)
	return fs.wrapFile(f), err
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
	f, err := fs.Filesystem.TempFile(dir, prefix)
	return fs.wrapFile(f), err
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

// recoveryFile wraps a billy.File with panic recovery.  go-nfs calls ReadAt and
// WriteAt directly on the file returned by Open/OpenFile/Create, so wrapping the
// filesystem alone leaves those calls unprotected.  It uses explicit delegation
// (not embedding) so the compiler forces us to implement any new methods added
// to the interface.
type recoveryFile struct {
	File    billy.File
	onPanic OnPanic
}

func (f *recoveryFile) recover() {
	if r := recover(); r != nil {
		f.onPanic(r)
	}
}

func (f *recoveryFile) recoverErr(err *error) {
	if r := recover(); r != nil {
		f.onPanic(r)
		*err = fmt.Errorf("%w: %v", ErrRecoveredPanic, r)
	}
}

func (f *recoveryFile) Name() string {
	defer f.recover()
	return f.File.Name()
}

func (f *recoveryFile) Read(p []byte) (n int, err error) {
	defer f.recoverErr(&err)
	return f.File.Read(p)
}

func (f *recoveryFile) Write(p []byte) (n int, err error) {
	defer f.recoverErr(&err)
	return f.File.Write(p)
}

func (f *recoveryFile) ReadAt(p []byte, off int64) (n int, err error) {
	defer f.recoverErr(&err)
	return f.File.ReadAt(p, off)
}

func (f *recoveryFile) WriteAt(p []byte, off int64) (n int, err error) {
	defer f.recoverErr(&err)
	return f.File.WriteAt(p, off)
}

func (f *recoveryFile) Seek(offset int64, whence int) (ret int64, err error) {
	defer f.recoverErr(&err)
	return f.File.Seek(offset, whence)
}

func (f *recoveryFile) Stat() (info os.FileInfo, err error) {
	defer f.recoverErr(&err)
	return f.File.Stat()
}

func (f *recoveryFile) Lock() (err error) {
	defer f.recoverErr(&err)
	return f.File.Lock()
}

func (f *recoveryFile) Unlock() (err error) {
	defer f.recoverErr(&err)
	return f.File.Unlock()
}

func (f *recoveryFile) Truncate(size int64) (err error) {
	defer f.recoverErr(&err)
	return f.File.Truncate(size)
}

func (f *recoveryFile) Close() (err error) {
	defer f.recoverErr(&err)
	return f.File.Close()
}
