package fsapi

import "errors"

// Errors an FS returns.  The protocol maps each to the NFS status that says the
// same thing, so prefer them to an errno or an fs sentinel, which it can only
// guess at.  Wrap them to add context - the protocol matches with errors.Is -
// and return anything else to say "something went wrong", which reaches the
// client as a server fault.
var (
	// ErrStale is a handle naming an object that is gone, or that this
	// filesystem never issued.
	ErrStale = errors.New("stale handle")
	// ErrNotFound is a name that is not in the directory it was looked up
	// in.
	ErrNotFound = errors.New("not found")
	// ErrExists is a name that is already taken.
	ErrExists = errors.New("already exists")
	// ErrNotDir is a directory operation on something that is not a
	// directory.
	ErrNotDir = errors.New("not a directory")
	// ErrIsDir is a non-directory operation on a directory.
	ErrIsDir = errors.New("is a directory")
	// ErrNotEmpty is a removal of a directory that still has entries.
	ErrNotEmpty = errors.New("directory not empty")
	// ErrAccess is an operation the caller is not permitted to perform.
	ErrAccess = errors.New("permission denied")
	// ErrPerm is an operation only the owner of the object may perform.
	ErrPerm = errors.New("not owner")
	// ErrReadOnlyFS is any change to a filesystem that does not accept them.
	// It is the filesystem that refuses, not this one object: an object that
	// this caller may not change is ErrAccess.
	ErrReadOnlyFS = errors.New("read-only filesystem")
	// ErrNotSupported is an operation this filesystem does not implement,
	// as opposed to one it refuses.
	ErrNotSupported = errors.New("operation not supported")
	// ErrInval is an argument that makes no sense for the object it names,
	// such as a Readlink of a file.
	ErrInval = errors.New("invalid argument")
	// ErrNotSync is a guarded SetAttr whose guard does not match the
	// object's ctime.
	ErrNotSync = errors.New("attributes changed since guard was read")
	// ErrNameTooLong is a name longer than the filesystem accepts.
	ErrNameTooLong = errors.New("name too long")
	// ErrStaleCookie is a directory listing that can no longer be resumed
	// where it left off.
	ErrStaleCookie = errors.New("stale directory cookie")
	// ErrXDev is a rename between two directories the filesystem cannot
	// move an object between.
	ErrXDev = errors.New("cross-device link")
	// ErrNoSpace is a write with nowhere left to put the bytes.
	ErrNoSpace = errors.New("no space left on device")
	// ErrQuota is a write refused because the owner is over quota, with
	// space remaining on the filesystem itself.
	ErrQuota = errors.New("quota exceeded")
	// ErrTooBig is a write that would make a file longer than the
	// filesystem allows.
	ErrTooBig = errors.New("file too large")
	// ErrTooManyLinks is a hard link beyond the number the object may have.
	ErrTooManyLinks = errors.New("too many links")
	// ErrSlow is an operation that has started but will not finish soon,
	// asking the client to come back for it.
	ErrSlow = errors.New("operation in progress")
)
