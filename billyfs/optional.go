package billyfs

import (
	"context"

	billy "github.com/go-git/go-billy/v6"
	"github.com/treeverse/go-nfs/fsapi"
)

// unixFS is an FS whose Handler returns a UnixChange, and so can also make hard
// links and the objects that are neither files nor directories.
type unixFS struct {
	*FS
}

var (
	_ fsapi.HandleKeeper = (*FS)(nil)
	_ fsapi.HandleKeeper = (*unixFS)(nil)
	_ fsapi.Symlinker    = (*FS)(nil)
	_ fsapi.Linker       = (*unixFS)(nil)
	_ fsapi.SpecialFiler = (*unixFS)(nil)
)

// adapt returns the fsapi.FS for bfs, implementing the optional interfaces its
// Handler can actually serve.  Whether there are hard links and device nodes is
// knowable - it is whether Change returns a UnixChange - so it decides the type
// here, and FSINFO reports it without having to guess.
//
// Symbolic links are not knowable: every billy.Filesystem has Symlink and
// Readlink, and one that does not mean them says so only when called.  So FS
// always implements Symlinker and such a filesystem fails SYMLINK with
// ErrNotSupported, which the client sees as NFS3ERR_NOTSUPP.
func adapt(handler Handler, bfs billy.Filesystem) fsapi.FS {
	base := &FS{handler: handler, billy: bfs}
	if _, ok := handler.Change(bfs).(UnixChange); ok {
		return &unixFS{base}
	}
	return base
}

// unixChange returns the Handler's changer as a UnixChange.
func (a *FS) unixChange(bfs billy.Filesystem) (UnixChange, error) {
	changer, ok := a.handler.Change(bfs).(UnixChange)
	if !ok {
		return nil, fsapi.ErrNotSupported
	}
	return changer, nil
}

func (a *FS) Symlink(ctx context.Context, dir fsapi.Handle, name, target string, change fsapi.AttrChange) (fsapi.Handle, *fsapi.Attrs, fsapi.Wcc, error) {
	bfs, dirPath, pre, err := a.prepareCreate(dir, name)
	if err != nil {
		return "", nil, fsapi.Wcc{}, err
	}
	full, path := join(bfs, dirPath, name)
	if err := bfs.Symlink(target, full); err != nil {
		return "", nil, fsapi.Wcc{Pre: pre}, translate(err)
	}
	return a.finishCreate(bfs, dirPath, path, pre, change)
}

func (a *FS) Readlink(ctx context.Context, h fsapi.Handle) (string, *fsapi.Attrs, error) {
	bfs, path, err := a.resolve(h)
	if err != nil {
		return "", nil, err
	}
	attrs, err := statAttrs(bfs, path)
	if err != nil {
		return "", nil, err
	}
	if attrs.Type != fsapi.TypeSymlink {
		return "", &attrs, fsapi.ErrInval
	}
	target, err := bfs.Readlink(bfs.Join(path...))
	if err != nil {
		return "", &attrs, translate(err)
	}
	return target, &attrs, nil
}

func (u *unixFS) Link(ctx context.Context, h fsapi.Handle, dir fsapi.Handle, name string) (*fsapi.Attrs, fsapi.Wcc, error) {
	bfs, targetPath, err := u.resolve(h)
	if err != nil {
		return nil, fsapi.Wcc{}, err
	}
	linkBfs, dirPath, pre, err := u.prepareCreate(dir, name)
	if err != nil {
		return nil, fsapi.Wcc{}, err
	}
	changer, err := u.unixChange(linkBfs)
	if err != nil {
		return nil, fsapi.Wcc{Pre: pre}, err
	}
	full, path := join(linkBfs, dirPath, name)
	if err := changer.Link(bfs.Join(targetPath...), full); err != nil {
		return nil, fsapi.Wcc{Pre: pre}, translate(err)
	}
	return optAttrs(linkBfs, path), wccFor(linkBfs, dirPath, pre), nil
}

func (u *unixFS) Mknod(ctx context.Context, dir fsapi.Handle, name string, typ fsapi.FileType, major, minor uint32, change fsapi.AttrChange) (fsapi.Handle, *fsapi.Attrs, fsapi.Wcc, error) {
	return u.makeSpecial(dir, name, change, func(changer UnixChange, full string, mode uint32) error {
		return changer.Mknod(full, mode, major, minor)
	})
}

func (u *unixFS) Mkfifo(ctx context.Context, dir fsapi.Handle, name string, change fsapi.AttrChange) (fsapi.Handle, *fsapi.Attrs, fsapi.Wcc, error) {
	return u.makeSpecial(dir, name, change, func(changer UnixChange, full string, mode uint32) error {
		return changer.Mkfifo(full, mode)
	})
}

func (u *unixFS) Socket(ctx context.Context, dir fsapi.Handle, name string, change fsapi.AttrChange) (fsapi.Handle, *fsapi.Attrs, fsapi.Wcc, error) {
	return u.makeSpecial(dir, name, change, func(changer UnixChange, full string, mode uint32) error {
		return changer.Socket(full)
	})
}

// makeSpecial is the shared body of Mknod, Mkfifo and Socket: they differ only
// in the one call that creates the thing.
func (u *unixFS) makeSpecial(dir fsapi.Handle, name string, change fsapi.AttrChange, create func(UnixChange, string, uint32) error) (fsapi.Handle, *fsapi.Attrs, fsapi.Wcc, error) {
	bfs, dirPath, pre, err := u.prepareCreate(dir, name)
	if err != nil {
		return "", nil, fsapi.Wcc{}, err
	}
	changer, err := u.unixChange(bfs)
	if err != nil {
		return "", nil, fsapi.Wcc{Pre: pre}, err
	}
	// A special file with no mode of its own gets the one its directory
	// has, which is what mknod(2) callers get from their umask.
	mode := uint32(0o644)
	if parent, err := dirAttrs(bfs, dirPath); err == nil {
		mode = parent.Perm
	}
	if change.Perm != nil {
		mode = *change.Perm
	}

	full, path := join(bfs, dirPath, name)
	if err := create(changer, full, mode); err != nil {
		return "", nil, fsapi.Wcc{Pre: pre}, translate(err)
	}
	return u.finishCreate(bfs, dirPath, path, pre, change)
}

// prepareCreate makes the checks every creation shares: the filesystem accepts
// changes, the parent is a directory, and the name is free.  It returns what
// the parent looked like beforehand, for the Wcc.
func (a *FS) prepareCreate(dir fsapi.Handle, name string) (billy.Filesystem, []string, *fsapi.CacheAttrs, error) {
	bfs, dirPath, err := a.resolve(dir)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := checkWritable(bfs); err != nil {
		return nil, nil, nil, err
	}
	parent, err := dirAttrs(bfs, dirPath)
	if err != nil {
		return nil, nil, nil, err
	}
	if !parent.IsDir() {
		return nil, nil, nil, fsapi.ErrNotDir
	}
	pre := parent.Cache()

	full, _ := join(bfs, dirPath, name)
	if _, err := bfs.Lstat(full); err == nil {
		return nil, nil, &pre, fsapi.ErrExists
	}
	return bfs, dirPath, &pre, nil
}

// finishCreate applies the requested attributes to a newly created object and
// reports it.
func (a *FS) finishCreate(bfs billy.Filesystem, dirPath, path []string, pre *fsapi.CacheAttrs, change fsapi.AttrChange) (fsapi.Handle, *fsapi.Attrs, fsapi.Wcc, error) {
	current, err := statAttrs(bfs, path)
	if err != nil {
		return "", nil, fsapi.Wcc{Pre: pre}, err
	}
	if err := a.applyChange(bfs, path, current, change); err != nil {
		return "", nil, fsapi.Wcc{Pre: pre}, err
	}
	return a.issue(bfs, path), optAttrs(bfs, path), wccFor(bfs, dirPath, pre), nil
}
