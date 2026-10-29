package billyfs

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"syscall"

	billy "github.com/go-git/go-billy/v6"
	"github.com/treeverse/go-nfs/file"
	"github.com/treeverse/go-nfs/fsapi"
)

// FS presents one billy.Filesystem, and the Handler that handed it out, as an
// fsapi.FS.
type FS struct {
	handler Handler
	billy   billy.Filesystem
}

var _ fsapi.FS = (*FS)(nil)

// New returns the fsapi.FS serving bfs through handler.
func New(handler Handler, bfs billy.Filesystem) fsapi.FS {
	return adapt(handler, bfs)
}

// Mounter adapts a Handler to fsapi.Mounter.
type Mounter struct {
	handler Handler
}

var _ fsapi.Mounter = (*Mounter)(nil)

// NewMounter returns the fsapi.Mounter serving handler.
func NewMounter(handler Handler) *Mounter {
	return &Mounter{handler: handler}
}

// Mount asks the handler for a filesystem, then wraps it and takes the handle
// of its root - which under billy is the empty path.
func (m *Mounter) Mount(ctx context.Context, conn net.Conn, req fsapi.MountRequest) (fsapi.MountStatus, fsapi.FS, fsapi.Handle, []fsapi.AuthFlavor) {
	status, bfs, flavors := m.handler.Mount(ctx, conn, req)
	if status != fsapi.MountStatusOk {
		return status, nil, "", flavors
	}
	// The root of a billy filesystem is the empty path.
	root := fsapi.Handle(m.handler.ToHandle(bfs, []string{}))
	return status, adapt(m.handler, bfs), root, flavors
}

// Umount passes the notice on to a handler that wants it, and is otherwise a
// no-op.
func (m *Mounter) Umount(ctx context.Context, conn net.Conn, req fsapi.MountRequest) error {
	if h, ok := m.handler.(UmountHandler); ok {
		return h.Umount(ctx, conn, req)
	}
	return nil
}

// resolve turns a handle back into the filesystem and path it names.
func (a *FS) resolve(h fsapi.Handle) (billy.Filesystem, []string, error) {
	bfs, path, err := a.handler.FromHandle([]byte(h))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", fsapi.ErrStale, err)
	}
	return bfs, path, nil
}

// issue returns a handle for a path, minting one if the handler has not seen it
// before.
func (a *FS) issue(bfs billy.Filesystem, path []string) fsapi.Handle {
	return fsapi.Handle(a.handler.ToHandle(bfs, path))
}

// HandleLimit implements fsapi.HandleKeeper: a Handler's handles are its own to
// keep, and a cache of them holds only so many, so the server is told what the
// Handler reports rather than left to assume they all survive.
func (a *FS) HandleLimit() int {
	return a.handler.HandleLimit()
}

// writable reports whether bfs accepts changes.
func writable(bfs billy.Filesystem) bool {
	return billy.CapabilityCheck(bfs, billy.WriteCapability)
}

// checkWritable fails if bfs accepts no changes, so that each operation that
// changes something can start with one line rather than the capability dance.
func checkWritable(bfs billy.Filesystem) error {
	if !writable(bfs) {
		return fsapi.ErrReadOnlyFS
	}
	return nil
}

// join builds the path of name within dir.
func join(bfs billy.Filesystem, dir []string, name string) (full string, path []string) {
	path = make([]string, 0, len(dir)+1)
	path = append(path, dir...)
	path = append(path, name)
	return bfs.Join(path...), path
}

// translate maps an error from billy, or from the os underneath it, onto the
// fsapi sentinel saying the same thing.  An error it does not recognize is
// returned unchanged: the protocol has its own errno mapping to fall back on.
func translate(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, os.ErrNotExist): // includes ENOENT
		return fmt.Errorf("%w: %v", fsapi.ErrNotFound, err)
	// ENOTEMPTY is also os.ErrExist, so it comes first.
	case errors.Is(err, syscall.ENOTEMPTY):
		return fmt.Errorf("%w: %v", fsapi.ErrNotEmpty, err)
	case errors.Is(err, os.ErrExist): // includes EEXIST
		return fmt.Errorf("%w: %v", fsapi.ErrExists, err)
	// Fun fact: POSIX EPERM is "operation not permitted", which is something
	// completely different from EACCES - and NFS has a status for each.  EPERM
	// is also os.ErrPermission, so it comes first.
	case errors.Is(err, syscall.EPERM):
		return fmt.Errorf("%w: %v", fsapi.ErrPerm, err)
	case errors.Is(err, os.ErrPermission): // includes EACCES
		return fmt.Errorf("%w: %v", fsapi.ErrAccess, err)
	case errors.Is(err, syscall.ENOTDIR):
		return fmt.Errorf("%w: %v", fsapi.ErrNotDir, err)
	case errors.Is(err, syscall.EISDIR):
		return fmt.Errorf("%w: %v", fsapi.ErrIsDir, err)
	case errors.Is(err, billy.ErrReadOnly), errors.Is(err, syscall.EROFS):
		return fmt.Errorf("%w: %v", fsapi.ErrReadOnlyFS, err)
	// ErrUnsupported includes ENOSYS, ENOTSUP and EOPNOTSUPP.
	case errors.Is(err, billy.ErrNotSupported), errors.Is(err, errors.ErrUnsupported):
		return fmt.Errorf("%w: %v", fsapi.ErrNotSupported, err)
	case errors.Is(err, ErrStaleCookie):
		return fmt.Errorf("%w: %v", fsapi.ErrStaleCookie, err)
	default:
		return err
	}
}

// modeBits converts a Go FileMode into the raw permission bits fsapi carries.
// Go keeps setuid, setgid and sticky in bits of its own, well away from where
// st_mode has them, so they are moved back rather than masked off.
func modeBits(mode os.FileMode) uint32 {
	bits := uint32(mode.Perm())
	if mode&os.ModeSetuid != 0 {
		bits |= syscall.S_ISUID
	}
	if mode&os.ModeSetgid != 0 {
		bits |= syscall.S_ISGID
	}
	if mode&os.ModeSticky != 0 {
		bits |= syscall.S_ISVTX
	}
	return bits
}

// goMode is modeBits backwards, for handing a mode to billy.
func goMode(bits uint32) os.FileMode {
	mode := os.FileMode(bits) & os.ModePerm
	if bits&syscall.S_ISUID != 0 {
		mode |= os.ModeSetuid
	}
	if bits&syscall.S_ISGID != 0 {
		mode |= os.ModeSetgid
	}
	if bits&syscall.S_ISVTX != 0 {
		mode |= os.ModeSticky
	}
	return mode
}

// fileType reports which kind of object mode describes.
func fileType(mode os.FileMode) fsapi.FileType {
	switch {
	case mode.IsDir():
		return fsapi.TypeDir
	case mode&os.ModeSymlink != 0:
		return fsapi.TypeSymlink
	case mode&os.ModeCharDevice != 0:
		return fsapi.TypeChar
	case mode&os.ModeDevice != 0:
		return fsapi.TypeBlock
	case mode&os.ModeSocket != 0:
		return fsapi.TypeSocket
	case mode&os.ModeNamedPipe != 0:
		return fsapi.TypeFIFO
	default:
		return fsapi.TypeRegular
	}
}

// toAttrs converts a FileInfo into fsapi.Attrs.
//
// path is the object's path from the root of bfs, in components.  It is used
// only to invent a fileid for a filesystem that reports none - but that fileid
// is what tells two objects apart, so it must be the whole path and not just
// the name: every x.txt in the tree would otherwise be one object.  Taking the
// components rather than a string is what stops a caller passing a bare name,
// which is how that went wrong once already.
func toAttrs(bfs billy.Filesystem, path []string, info os.FileInfo) fsapi.Attrs {
	fullPath := bfs.Join(path...)
	mode := info.Mode()
	attrs := fsapi.Attrs{
		Type:  fileType(mode),
		Perm:  modeBits(mode),
		Nlink: 1,
		Size:  uint64(info.Size()),
		Used:  uint64(info.Size()),
	}
	if sys := file.GetInfo(info); sys != nil {
		attrs.Nlink = sys.Nlink
		attrs.UID = sys.UID
		attrs.GID = sys.GID
		attrs.SpecData = [2]uint32{sys.Major, sys.Minor}
		attrs.Fileid = sys.Fileid
		attrs.FSID = sys.FSID
	} else {
		hasher := fnv.New64()
		_, _ = hasher.Write([]byte(fullPath))
		attrs.Fileid = hasher.Sum64()
	}
	// billy reports one timestamp, so it stands for all three.
	t := info.ModTime()
	attrs.Atime, attrs.Mtime, attrs.Ctime = t, t, t
	return attrs
}

// statAttrs returns the attributes of a path, without following a symlink: NFS
// asks about the link itself.
func statAttrs(bfs billy.Filesystem, path []string) (fsapi.Attrs, error) {
	info, err := bfs.Lstat(bfs.Join(path...))
	if err != nil {
		return fsapi.Attrs{}, translate(err)
	}
	return toAttrs(bfs, path, info), nil
}

// dirAttrs returns the attributes of a directory operand.  It follows symlinks,
// where statAttrs does not: a handle is expected to name the directory itself,
// and the billy-based handlers used Stat for every parent they checked.  The
// difference is visible to a billy.Filesystem that answers Stat and Lstat
// differently.
func dirAttrs(bfs billy.Filesystem, path []string) (fsapi.Attrs, error) {
	info, err := bfs.Stat(bfs.Join(path...))
	if err != nil {
		return fsapi.Attrs{}, translate(err)
	}
	return toAttrs(bfs, path, info), nil
}

// optAttrs is statAttrs for somewhere attributes are optional: it reports
// nothing rather than failing the operation that asked.
func optAttrs(bfs billy.Filesystem, path []string) *fsapi.Attrs {
	attrs, err := statAttrs(bfs, path)
	if err != nil {
		return nil
	}
	return &attrs
}

// wccFor returns the Wcc spanning a change to path, given what it looked like
// before.  pre may be nil for a change with nothing to report from before.
func wccFor(bfs billy.Filesystem, path []string, pre *fsapi.CacheAttrs) fsapi.Wcc {
	return fsapi.Wcc{Pre: pre, Post: optAttrs(bfs, path)}
}
