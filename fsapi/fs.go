package fsapi

import (
	"context"
	"time"
)

// FS is a filesystem exported over NFS.  Every method names its object by a
// Handle and takes a context, which is the context of the request being served:
// a filesystem that talks to anything slow should respect it.
//
// Handles are valid for the lifetime of the mount.  The server never announces
// that it has finished with one, so a filesystem must be able to resolve any
// handle it ever returned.  A handle whose object no longer exists resolves to
// ErrStale.  A filesystem that cannot hold them all implements HandleKeeper,
// and the server keeps within what it says.
//
// Several methods return attributes the protocol sends as an optional
// "post_op_attr": a nil *Attrs says the filesystem cannot report them, and is
// always allowed.  Returning them costs nothing extra here and saves the server
// a second call, so report them where you can.
type FS interface {
	Data

	// GetAttr returns the attributes of h.
	GetAttr(ctx context.Context, h Handle) (Attrs, error)

	// SetAttr applies change to h.  guard, if non-nil, is the ctime the
	// client believes h has: SetAttr fails with ErrNotSync if h's ctime
	// differs, so that a client can refuse to overwrite a change it has not
	// seen.
	SetAttr(ctx context.Context, h Handle, change AttrChange, guard *time.Time) (Wcc, error)

	// Lookup returns the entry name of directory dir.  name is a single path
	// component, never "." or ".." - the server serves those itself, using
	// LookupParent for the latter.
	Lookup(ctx context.Context, dir Handle, name string) (h Handle, attrs, dirAttrs *Attrs, err error)

	// LookupParent returns the directory holding dir.  The root is its own
	// parent.  The server uses it to serve a lookup of "..", and to report
	// the fileid of the ".." entry of a directory listing.
	LookupParent(ctx context.Context, dir Handle) (h Handle, attrs *Attrs, err error)

	// Access returns which of the operations in want the caller may perform
	// on h.  It is advice: a filesystem that grants an operation here may
	// still refuse it later, and one that reports every bit in want is
	// within its rights.
	Access(ctx context.Context, h Handle, want AccessMask) (granted AccessMask, attrs *Attrs, err error)

	// Create creates the file name in directory dir and applies change to
	// it.  how says what to do about a name that is already taken.
	Create(ctx context.Context, dir Handle, name string, how CreateHow, change AttrChange) (h Handle, attrs *Attrs, w Wcc, err error)

	// Mkdir creates the directory name in directory dir and applies change
	// to it.  name is a single component: unlike MkdirAll, Mkdir does not
	// create anything else along the way.
	Mkdir(ctx context.Context, dir Handle, name string, change AttrChange) (h Handle, attrs *Attrs, w Wcc, err error)

	// Remove removes name from dir.  It fails with ErrIsDir if name is a
	// directory.  The check and the removal are atomic.
	Remove(ctx context.Context, dir Handle, name string) (Wcc, error)

	// RemoveDir removes the empty directory name from dir.  It fails with
	// ErrNotDir if name is not a directory, and with ErrNotEmpty if it has
	// any entries.  The checks and the removal are atomic.
	RemoveDir(ctx context.Context, dir Handle, name string) (Wcc, error)

	// Rename moves fromName in fromDir to toName in toDir, replacing
	// whatever toName named.  fromDir and toDir may be the same directory.
	// A filesystem that cannot move an object between these two directories
	// fails with ErrXDev.
	Rename(ctx context.Context, fromDir Handle, fromName string, toDir Handle, toName string) (from, to Wcc, err error)

	// ReadDir returns an iterator over the entries of dir, resuming after cookie.  A zero
	// cookie starts a fresh listing and ignores verf; otherwise verf is the verifier from
	// the listing cookie came from, and ReadDir fails with ErrStaleCookie if it can no
	// longer resume there.
	//
	// The iterator must not report "." or "..", which the server serves itself.  It need
	// not sort entries.
	ReadDir(ctx context.Context, dir Handle, cookie Cookie, verf Verifier) (DirIterator, error)

	// FSStat returns how much space the filesystem holding h has.
	FSStat(ctx context.Context, h Handle) (FSStat, *Attrs, error)

	// FSInfo returns the fixed properties and limits of the filesystem
	// holding h.  The server sends them to the client, which uses them to
	// size its requests.
	FSInfo(ctx context.Context, h Handle) (FSInfo, *Attrs, error)
}

// Data moves file contents.  It is a separate interface so that a filesystem
// able to move bytes without copying them through the server can grow one
// without disturbing the rest of FS.
type Data interface {
	// Read reads from h at offset into buf, returning the number of bytes
	// read and whether the read reached the end of the file.  A short read
	// is not an error: the server sends back as much as it got.
	Read(ctx context.Context, h Handle, offset uint64, buf []byte) (n int, eof bool, attrs *Attrs, err error)

	// Write writes data to h at offset.  It must not return until the bytes
	// are durable: the server tells every client that every write is
	// committed, so a client is free to drop its own copy the moment Write
	// returns.  A filesystem that wants to defer durability instead needs
	// the stability negotiation this interface used to carry, and the COMMIT
	// handling that goes with it.
	Write(ctx context.Context, h Handle, offset uint64, data []byte) (n int, w Wcc, err error)
}

// DirEntry is one entry of a directory listing.
type DirEntry struct {
	// Name is the entry's name within its directory, not a path.
	Name string
	// Handle is the entry's handle.  It is valid beyond the lifetime of the
	// iterator that produced it.
	Handle Handle
	// Attrs are the entry's attributes, which a listing always reports.
	//
	// This is the one place in this package where attributes are not
	// optional, because a listing entry has no absent arm to map onto: both
	// of the NFS entry structures carry a mandatory fileid, and the fileid
	// is an attribute.  A filesystem that reported none would leave the
	// server to send a fileid of zero, which NFS reserves and clients read
	// as "no such object".
	Attrs Attrs
	// Cookie resumes a listing after this entry.
	Cookie Cookie
}

// Cookie marks a position in a directory listing.  The zero Cookie means the
// start of the listing.
//
// A filesystem must not issue 0, 1 or 2 for an entry: NFS listings begin with
// "." and "..", which the server supplies itself, and it needs cookies of its
// own to resume between and after them.  A filesystem numbering its entries
// can simply start at 3.
type Cookie uint64

// FirstCookie is the lowest Cookie a filesystem may issue for an entry.
const FirstCookie Cookie = 3

// Verifier tells a filesystem whether a Cookie it is handed is still one of
// its own.  It changes whenever previously issued Cookies stop being valid.
// The zero Verifier means the filesystem does not use one.
type Verifier uint64

// DirIterator walks the entries of one directory.  The caller calls Next until
// it returns false, then checks Err, and calls Close exactly once when done.
type DirIterator interface {
	// Next advances to the next entry, reporting whether there is one.  It
	// returns false at the end of the directory and on error; Err tells the
	// two apart.
	Next(ctx context.Context) bool
	// Entry returns the entry Next just advanced to.  It is valid until the
	// next call to Next.
	Entry() DirEntry
	// Verifier returns the verifier of this listing.  It does not change
	// over the life of the iterator.
	Verifier() Verifier
	// Err returns the error that stopped the iteration, or nil if it simply
	// ended.
	Err() error
	// Close releases the iterator.  It is safe to call after an error.
	Close() error
}

// HandleKeeper is an FS that cannot keep its handles for the lifetime of the
// mount, and says how many of them it holds at once.  An FS that does not
// implement it promises what FS documents: every handle it ever returned still
// resolves.
//
// The server spends the limit on directory listings, which are what mint
// handles in bulk.  It uses half of it for any one listing and leaves the rest
// for the handles a client already holds - the directory being listed, its
// parent, the root it walked from - rather than costing a client the handles it
// is still using to read the reply.
//
// The count describes a filesystem serving one request at a time.  Requests
// served concurrently each measure themselves against the same limit, so
// together they can mint more handles than it names.
type HandleKeeper interface {
	// HandleLimit returns how many handles this filesystem keeps alive at
	// once.  Minting one beyond that number costs it an older one.
	HandleLimit() int
}

// Symlinker is an FS that has symbolic links.  An FS that does not implement it
// reports no symlink support to clients, and fails READLINK and SYMLINK.
type Symlinker interface {
	// Symlink creates the symbolic link name in dir, pointing at target.
	// target is not resolved and need not exist.
	Symlink(ctx context.Context, dir Handle, name, target string, change AttrChange) (h Handle, attrs *Attrs, w Wcc, err error)
	// Readlink returns the target of the symbolic link h, failing with
	// ErrInval if h is not one.
	Readlink(ctx context.Context, h Handle) (target string, attrs *Attrs, err error)
}

// Linker is an FS that has hard links.
type Linker interface {
	// Link makes name in dir a second name for the existing object h.
	Link(ctx context.Context, h Handle, dir Handle, name string) (attrs *Attrs, w Wcc, err error)
}

// SpecialFiler is an FS that holds objects other than files, directories and
// symbolic links.
type SpecialFiler interface {
	// Mknod creates a block or character device.  typ is
	// TypeBlock or TypeChar.
	Mknod(ctx context.Context, dir Handle, name string, typ FileType, major, minor uint32, change AttrChange) (h Handle, attrs *Attrs, w Wcc, err error)
	// Mkfifo creates a named pipe.
	Mkfifo(ctx context.Context, dir Handle, name string, change AttrChange) (h Handle, attrs *Attrs, w Wcc, err error)
	// Socket creates a unix-domain socket.
	Socket(ctx context.Context, dir Handle, name string, change AttrChange) (h Handle, attrs *Attrs, w Wcc, err error)
}
