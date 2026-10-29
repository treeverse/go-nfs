package billyfs

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"reflect"
	"time"

	billy "github.com/go-git/go-billy/v6"
	"github.com/treeverse/go-nfs/fsapi"
)

func (a *FS) GetAttr(ctx context.Context, h fsapi.Handle) (fsapi.Attrs, error) {
	bfs, path, err := a.resolve(h)
	if err != nil {
		return fsapi.Attrs{}, err
	}
	return statAttrs(bfs, path)
}

func (a *FS) SetAttr(ctx context.Context, h fsapi.Handle, change fsapi.AttrChange, guard *time.Time) (fsapi.Wcc, error) {
	bfs, path, err := a.resolve(h)
	if err != nil {
		return fsapi.Wcc{}, err
	}
	current, err := statAttrs(bfs, path)
	if err != nil {
		return fsapi.Wcc{}, err
	}
	if guard != nil && !guard.Equal(current.Ctime) {
		return fsapi.Wcc{}, fsapi.ErrNotSync
	}
	if err := checkWritable(bfs); err != nil {
		return fsapi.Wcc{}, err
	}

	pre := current.Cache()
	if err := a.applyChange(bfs, path, current, change); err != nil {
		return fsapi.Wcc{Pre: &pre, Post: optAttrs(bfs, path)}, err
	}
	return wccFor(bfs, path, &pre), nil
}

// applyChange is the old SetFileAttributes.Apply: billy spreads one sattr3
// across a changer, an open file and a truncate, and this is where that is put
// back together.
func (a *FS) applyChange(bfs billy.Filesystem, path []string, current fsapi.Attrs, change fsapi.AttrChange) error {
	if change.Empty() {
		return nil
	}
	full := bfs.Join(path...)
	changer := a.handler.Change(bfs)

	if change.Perm != nil && *change.Perm != current.Perm {
		if changer == nil {
			return fsapi.ErrNotSupported
		}
		if err := changer.Chmod(full, goMode(*change.Perm)); err != nil {
			return translate(err)
		}
	}

	if change.UID != nil || change.GID != nil {
		uid, gid := current.UID, current.GID
		if change.UID != nil {
			uid = *change.UID
		}
		if change.GID != nil {
			gid = *change.GID
		}
		if uid != current.UID || gid != current.GID {
			if changer == nil {
				return fsapi.ErrNotSupported
			}
			if err := changer.Lchown(full, int(uid), int(gid)); err != nil {
				return translate(err)
			}
		}
	}

	if change.Size != nil {
		if current.Type == fsapi.TypeSymlink {
			return fsapi.ErrInval
		}
		if *change.Size > math.MaxInt64 {
			return fsapi.ErrInval
		}
		fh, err := bfs.OpenFile(full, os.O_WRONLY, 0)
		if err != nil {
			return translate(err)
		}
		if err := fh.Truncate(int64(*change.Size)); err != nil {
			_ = fh.Close()
			return translate(err)
		}
		if err := fh.Close(); err != nil {
			return translate(err)
		}
	}

	if change.Atime != nil || change.Mtime != nil {
		atime, mtime := current.Atime, current.Mtime
		if change.Atime != nil {
			atime = *change.Atime
		}
		if change.Mtime != nil {
			mtime = *change.Mtime
		}
		if !atime.Equal(current.Atime) || !mtime.Equal(current.Mtime) {
			if changer == nil {
				return fsapi.ErrNotSupported
			}
			if err := changer.Chtimes(full, atime, mtime); err != nil {
				return translate(err)
			}
		}
	}
	return nil
}

func (a *FS) Lookup(ctx context.Context, dir fsapi.Handle, name string) (fsapi.Handle, *fsapi.Attrs, *fsapi.Attrs, error) {
	bfs, dirPath, err := a.resolve(dir)
	if err != nil {
		return "", nil, nil, err
	}
	dirAttrs, err := statAttrs(bfs, dirPath)
	if err != nil {
		return "", nil, nil, err
	}
	if !dirAttrs.IsDir() {
		return "", nil, nil, fsapi.ErrNotDir
	}

	full, path := join(bfs, dirPath, name)
	info, err := bfs.Lstat(full)
	if err != nil {
		return "", nil, &dirAttrs, translate(err)
	}
	attrs := toAttrs(bfs, path, info)
	return a.issue(bfs, path), &attrs, &dirAttrs, nil
}

func (a *FS) LookupParent(ctx context.Context, dir fsapi.Handle) (fsapi.Handle, *fsapi.Attrs, error) {
	bfs, path, err := a.resolve(dir)
	if err != nil {
		return "", nil, err
	}
	// The root is its own parent, which is what a client expects of ".." at
	// the top of a mount.
	parent := path
	if len(path) > 0 {
		parent = path[:len(path)-1]
	}
	return a.issue(bfs, parent), optAttrs(bfs, parent), nil
}

func (a *FS) Access(ctx context.Context, h fsapi.Handle, want fsapi.AccessMask) (fsapi.AccessMask, *fsapi.Attrs, error) {
	bfs, path, err := a.resolve(h)
	if err != nil {
		return 0, nil, err
	}
	attrs, err := statAttrs(bfs, path)
	if err != nil {
		return 0, nil, err
	}
	// billy says only whether the filesystem as a whole is writable, so
	// everything asked for is granted except changes to a read-only one.
	granted := want
	if !writable(bfs) {
		granted &^= fsapi.AccessWrite
	}
	return granted, &attrs, nil
}

func (a *FS) Read(ctx context.Context, h fsapi.Handle, offset uint64, buf []byte) (int, bool, *fsapi.Attrs, error) {
	bfs, path, err := a.resolve(h)
	if err != nil {
		return 0, false, nil, err
	}
	full := bfs.Join(path...)
	fh, err := bfs.Open(full)
	if err != nil {
		return 0, false, nil, translate(err)
	}
	defer func() { _ = fh.Close() }()

	if offset > math.MaxInt64 {
		return 0, false, nil, fsapi.ErrInval
	}
	n, err := fh.ReadAt(buf, int64(offset))
	eof := errors.Is(err, io.EOF)
	if err != nil && !eof {
		return 0, false, nil, translate(err)
	}
	return n, eof, optAttrs(bfs, path), nil
}

// Write writes through, as fsapi requires: billy offers no way to write
// without committing, so the bytes are as durable as the backing store makes
// them by the time this returns.
func (a *FS) Write(ctx context.Context, h fsapi.Handle, offset uint64, data []byte) (int, fsapi.Wcc, error) {
	bfs, path, err := a.resolve(h)
	if err != nil {
		return 0, fsapi.Wcc{}, err
	}
	if err := checkWritable(bfs); err != nil {
		return 0, fsapi.Wcc{}, err
	}
	current, err := statAttrs(bfs, path)
	if err != nil {
		return 0, fsapi.Wcc{}, err
	}
	if current.Type != fsapi.TypeRegular {
		return 0, fsapi.Wcc{}, fsapi.ErrInval
	}
	if offset > math.MaxInt64 {
		return 0, fsapi.Wcc{}, fsapi.ErrInval
	}
	pre := current.Cache()

	full := bfs.Join(path...)
	fh, err := bfs.OpenFile(full, os.O_RDWR, goMode(current.Perm))
	if err != nil {
		return 0, fsapi.Wcc{Pre: &pre}, translate(err)
	}
	n, err := fh.WriteAt(data, int64(offset))
	if err != nil {
		_ = fh.Close()
		return n, fsapi.Wcc{Pre: &pre}, translate(err)
	}
	if err := fh.Close(); err != nil {
		return n, fsapi.Wcc{Pre: &pre}, translate(err)
	}
	return n, wccFor(bfs, path, &pre), nil
}

func (a *FS) Create(ctx context.Context, dir fsapi.Handle, name string, how fsapi.CreateHow, change fsapi.AttrChange) (fsapi.Handle, *fsapi.Attrs, fsapi.Wcc, error) {
	bfs, dirPath, err := a.resolve(dir)
	if err != nil {
		return "", nil, fsapi.Wcc{}, err
	}
	if err := checkWritable(bfs); err != nil {
		return "", nil, fsapi.Wcc{}, err
	}
	if how.Mode == fsapi.CreateExclusive {
		// There is nowhere under billy to keep a create verifier.
		return "", nil, fsapi.Wcc{}, fsapi.ErrNotSupported
	}

	parent, err := dirAttrs(bfs, dirPath)
	if err != nil {
		return "", nil, fsapi.Wcc{}, err
	}
	if !parent.IsDir() {
		return "", nil, fsapi.Wcc{}, fsapi.ErrNotDir
	}
	pre := parent.Cache()

	full, path := join(bfs, dirPath, name)
	existed := false
	if info, err := bfs.Lstat(full); err == nil {
		if info.IsDir() {
			return "", nil, fsapi.Wcc{Pre: &pre}, fsapi.ErrIsDir
		}
		if how.Mode == fsapi.CreateGuarded {
			return "", nil, fsapi.Wcc{Pre: &pre}, fsapi.ErrExists
		}
		existed = true
	}

	fh, err := bfs.Create(full)
	if err != nil {
		return "", nil, fsapi.Wcc{Pre: &pre}, translate(err)
	}
	if err := fh.Close(); err != nil {
		return "", nil, fsapi.Wcc{Pre: &pre}, translate(err)
	}

	if existed {
		// A file that was already there keeps its mode, ownership and
		// times: only the size carries over.  RFC 1813 3.3.8 does not say
		// what UNCHECKED does to a file that exists; RFC 7530 16.16
		// specifies it for NFSv4 - "the attributes specified by
		// createattrs are not used, except that when a size of zero is
		// specified, the existing file is truncated" - and Linux's nfsd
		// does the same for NFSv3 in nfsd3_create_file().
		//
		// Size is copied rather than forced to zero because a client
		// sends a mode on every create but a size only under O_TRUNC,
		// and then only zero.  An open(O_WRONLY|O_CREAT|O_APPEND) of an
		// existing file therefore arrives with a mode that must not be
		// applied and no size at all, so "did not ask" has to stay
		// distinct from "asked for zero".
		change = fsapi.AttrChange{Size: change.Size}
	}
	current, err := statAttrs(bfs, path)
	if err != nil {
		return "", nil, fsapi.Wcc{Pre: &pre}, err
	}
	if err := a.applyChange(bfs, path, current, change); err != nil {
		return "", nil, fsapi.Wcc{Pre: &pre}, err
	}

	return a.issue(bfs, path), optAttrs(bfs, path), wccFor(bfs, dirPath, &pre), nil
}

func (a *FS) Mkdir(ctx context.Context, dir fsapi.Handle, name string, change fsapi.AttrChange) (fsapi.Handle, *fsapi.Attrs, fsapi.Wcc, error) {
	bfs, dirPath, err := a.resolve(dir)
	if err != nil {
		return "", nil, fsapi.Wcc{}, err
	}
	if err := checkWritable(bfs); err != nil {
		return "", nil, fsapi.Wcc{}, err
	}
	parent, err := dirAttrs(bfs, dirPath)
	if err != nil {
		return "", nil, fsapi.Wcc{}, err
	}
	if !parent.IsDir() {
		return "", nil, fsapi.Wcc{}, fsapi.ErrNotDir
	}
	pre := parent.Cache()

	full, path := join(bfs, dirPath, name)
	if _, err := bfs.Lstat(full); err == nil {
		return "", nil, fsapi.Wcc{Pre: &pre}, fsapi.ErrExists
	}

	mode := os.FileMode(0o755)
	if change.Perm != nil {
		mode = goMode(*change.Perm)
		change.Perm = nil
	}
	// MkdirAll, not Mkdir: billy has no single-component mkdir.  The parent
	// was checked above, so there is nothing left for it to create.
	if err := bfs.MkdirAll(full, mode); err != nil {
		return "", nil, fsapi.Wcc{Pre: &pre}, translate(err)
	}

	current, err := statAttrs(bfs, path)
	if err != nil {
		return "", nil, fsapi.Wcc{Pre: &pre}, err
	}
	if err := a.applyChange(bfs, path, current, change); err != nil {
		return "", nil, fsapi.Wcc{Pre: &pre}, err
	}
	return a.issue(bfs, path), optAttrs(bfs, path), wccFor(bfs, dirPath, &pre), nil
}

func (a *FS) Remove(ctx context.Context, dir fsapi.Handle, name string) (fsapi.Wcc, error) {
	return a.remove(ctx, dir, name, false)
}

func (a *FS) RemoveDir(ctx context.Context, dir fsapi.Handle, name string) (fsapi.Wcc, error) {
	return a.remove(ctx, dir, name, true)
}

// remove implements both removals.  billy exposes no atomic "remove only if a
// directory", so the type check and the emptiness check sit immediately before
// the Remove and a target that changes type in between can still slip through.
// A filesystem written against fsapi directly is expected to do better; this is
// as well as billy allows.
func (a *FS) remove(ctx context.Context, dir fsapi.Handle, name string, wantDir bool) (fsapi.Wcc, error) {
	bfs, dirPath, err := a.resolve(dir)
	if err != nil {
		return fsapi.Wcc{}, err
	}
	if err := checkWritable(bfs); err != nil {
		return fsapi.Wcc{}, err
	}
	parent, err := dirAttrs(bfs, dirPath)
	if err != nil {
		return fsapi.Wcc{}, err
	}
	if !parent.IsDir() {
		return fsapi.Wcc{}, fsapi.ErrNotDir
	}
	pre := parent.Cache()

	full, path := join(bfs, dirPath, name)
	// Lstat, not Stat: rmdir and unlink act on the entry itself, so a
	// symlink to a directory is not a directory here.
	info, err := bfs.Lstat(full)
	if err != nil {
		return fsapi.Wcc{Pre: &pre}, translate(err)
	}
	switch {
	case wantDir && !info.IsDir():
		return fsapi.Wcc{Pre: &pre}, fsapi.ErrNotDir
	case !wantDir && info.IsDir():
		return fsapi.Wcc{Pre: &pre}, fsapi.ErrIsDir
	}
	if wantDir {
		empty, err := a.isEmptyDir(ctx, bfs, full)
		if err != nil {
			return fsapi.Wcc{Pre: &pre}, translate(err)
		}
		if !empty {
			return fsapi.Wcc{Pre: &pre}, fsapi.ErrNotEmpty
		}
	}

	handle := a.issue(bfs, path)
	if err := bfs.Remove(full); err != nil {
		return fsapi.Wcc{Pre: &pre}, translate(err)
	}
	if err := a.handler.InvalidateHandle(bfs, []byte(handle)); err != nil {
		return fsapi.Wcc{Pre: &pre}, err
	}
	return wccFor(bfs, dirPath, &pre), nil
}

// isEmptyDir reports whether a directory has no entries, asking the handler's
// iterator for one entry where it has one rather than reading the whole
// listing.
func (a *FS) isEmptyDir(ctx context.Context, bfs billy.Filesystem, full string) (bool, error) {
	if h, ok := a.handler.(DirIteratorHandler); ok {
		it, err := h.OpenDir(ctx, full, 0, 0)
		if err != nil {
			return false, err
		}
		defer it.Close()
		if it.Next() {
			return false, nil
		}
		return true, it.Err()
	}
	entries, err := bfs.ReadDir(full)
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

func (a *FS) Rename(ctx context.Context, fromDir fsapi.Handle, fromName string, toDir fsapi.Handle, toName string) (fsapi.Wcc, fsapi.Wcc, error) {
	bfs, fromPath, err := a.resolve(fromDir)
	if err != nil {
		return fsapi.Wcc{}, fsapi.Wcc{}, err
	}
	toBfs, toPath, err := a.resolve(toDir)
	if err != nil {
		return fsapi.Wcc{}, fsapi.Wcc{}, err
	}
	if err := checkWritable(bfs); err != nil {
		return fsapi.Wcc{}, fsapi.Wcc{}, err
	}

	fromAttrs, err := dirAttrs(bfs, fromPath)
	if err != nil {
		return fsapi.Wcc{}, fsapi.Wcc{}, err
	}
	toAttrsValue, err := dirAttrs(toBfs, toPath)
	if err != nil {
		return fsapi.Wcc{}, fsapi.Wcc{}, err
	}
	if !fromAttrs.IsDir() || !toAttrsValue.IsDir() {
		return fsapi.Wcc{}, fsapi.Wcc{}, fsapi.ErrNotDir
	}
	// RFC 1813 3.3.14: the two directories are on one filesystem when the
	// fsid in their attributes matches.  A handler that reports no fsid
	// leaves both zero, which decides nothing, so fall back to comparing
	// the filesystems themselves - which is all billy offers.  Either way it
	// is a move this filesystem cannot make, so both say so the same way.
	if fromAttrs.FSID != toAttrsValue.FSID {
		return fsapi.Wcc{}, fsapi.Wcc{}, fsapi.ErrXDev
	}
	if fromAttrs.FSID == 0 && !reflect.DeepEqual(bfs, toBfs) {
		return fsapi.Wcc{}, fsapi.Wcc{}, fsapi.ErrXDev
	}
	fromPre, toPre := fromAttrs.Cache(), toAttrsValue.Cache()

	fromFull, oldPath := join(bfs, fromPath, fromName)
	toFull, _ := join(bfs, toPath, toName)
	handle := a.issue(bfs, oldPath)
	if err := bfs.Rename(fromFull, toFull); err != nil {
		return fsapi.Wcc{Pre: &fromPre}, fsapi.Wcc{Pre: &toPre}, translate(err)
	}
	if err := a.handler.InvalidateHandle(bfs, []byte(handle)); err != nil {
		return fsapi.Wcc{Pre: &fromPre}, fsapi.Wcc{Pre: &toPre}, err
	}
	return wccFor(bfs, fromPath, &fromPre), wccFor(toBfs, toPath, &toPre), nil
}

func (a *FS) FSStat(ctx context.Context, h fsapi.Handle) (fsapi.FSStat, *fsapi.Attrs, error) {
	bfs, path, err := a.resolve(h)
	if err != nil {
		return fsapi.FSStat{}, nil, err
	}
	// The same large defaults the billy-based server used: billy knows
	// nothing about free space, so the answer is "ask again later".
	const plenty = uint64(1) << 62
	stat := FSStat{
		TotalSize:      plenty,
		FreeSize:       plenty,
		AvailableSize:  plenty,
		TotalFiles:     plenty,
		FreeFiles:      plenty,
		AvailableFiles: plenty,
	}
	if !writable(bfs) {
		stat.AvailableSize = 0
		stat.AvailableFiles = 0
	}
	if err := a.handler.FSStat(ctx, bfs, &stat); err != nil {
		return fsapi.FSStat{}, nil, translate(err)
	}
	return fsapi.FSStat(stat), optAttrs(bfs, path), nil
}

func (a *FS) FSInfo(ctx context.Context, h fsapi.Handle) (fsapi.FSInfo, *fsapi.Attrs, error) {
	bfs, path, err := a.resolve(h)
	if err != nil {
		return fsapi.FSInfo{}, nil, err
	}
	info := fsapi.FSInfo{
		ReadMax:       1 << 30,
		ReadPref:      1 << 30,
		ReadMultiple:  4096,
		WriteMax:      1 << 30,
		WritePref:     1 << 30,
		WriteMultiple: 4096,
		DirPref:       8192,
		MaxFileSize:   1 << 62,
		TimeDelta:     time.Nanosecond,
		Properties:    fsapi.FSPropertyHomogeneous,
	}
	if writable(bfs) {
		info.Properties |= fsapi.FSPropertyCanSetTime
	}
	return info, optAttrs(bfs, path), nil
}
