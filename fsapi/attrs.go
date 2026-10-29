package fsapi

import "time"

// FileType is the type of a filesystem object.
type FileType uint32

// FileType values.  From TypeRegular on they are the NFSv3 ftype3 values, which
// the protocol writes out directly.
const (
	// TypeUnknown is the zero value, and is not a type a filesystem may
	// report: an object it cannot classify has no ftype3 to be sent as.
	TypeUnknown FileType = iota
	TypeRegular
	TypeDir
	TypeBlock
	TypeChar
	TypeSymlink
	TypeSocket
	TypeFIFO
)

func (f FileType) String() string {
	switch f {
	case TypeRegular:
		return "regular"
	case TypeDir:
		return "dir"
	case TypeBlock:
		return "block"
	case TypeChar:
		return "char"
	case TypeSymlink:
		return "symlink"
	case TypeSocket:
		return "socket"
	case TypeFIFO:
		return "fifo"
	default:
		return "unknown"
	}
}

// Attrs are the attributes of one object.
type Attrs struct {
	Type FileType
	// Perm holds the permission bits in their raw st_mode positions: the low
	// nine, plus setuid, setgid and sticky.  Not a Go FileMode, which keeps
	// those three bits somewhere else entirely.
	Perm  uint32
	Nlink uint32
	UID   uint32
	GID   uint32
	// Size is the length of a file, or 0 for an object without a length.
	//
	// It must be the number of bytes a read can return, because the server
	// trusts it: for a large read it asks for Size first and allocates, and
	// reads, only what Size says is there.  An object that reports less than
	// it would yield is read short or not at all - an in-memory status file
	// reporting 0 would never be readable.
	Size uint64
	// Used is the number of bytes of storage the object actually consumes,
	// which may be less than Size for a sparse file.
	Used uint64
	// SpecData is the major and minor device number of a block or character
	// device, and zero for anything else.
	SpecData [2]uint32
	// FSID identifies the filesystem holding this object.
	FSID uint64
	// Fileid identifies the object within its filesystem, as an inode number
	// does.  Clients use it to detect that two names reach one object, so it
	// must be stable and must differ between objects.
	Fileid              uint64
	Atime, Mtime, Ctime time.Time
}

// CacheAttrs are the attributes a client compares to decide whether its cache of
// an object is still good.  They are the "before" half of a Wcc.
type CacheAttrs struct {
	Size         uint64
	Mtime, Ctime time.Time
}

// Cache returns the CacheAttrs of a.
func (a Attrs) Cache() CacheAttrs {
	return CacheAttrs{Size: a.Size, Mtime: a.Mtime, Ctime: a.Ctime}
}

// IsDir reports whether a describes a directory.
func (a Attrs) IsDir() bool {
	return a.Type == TypeDir
}

// Wcc reports what an operation changed about a directory or file: its
// attributes just before the operation, and just after.  A client uses the pair
// to tell whether anything else changed the object in between, and so whether it
// may keep the rest of its cache.
//
// Either half may be nil, meaning the filesystem cannot say.  A filesystem that
// reports neither is correct but makes clients re-read more than they need to.
type Wcc struct {
	Pre  *CacheAttrs
	Post *Attrs
}

// WccFrom returns the Wcc spanning pre to post.
func WccFrom(pre CacheAttrs, post Attrs) Wcc {
	return Wcc{Pre: &pre, Post: &post}
}

// AttrChange is a request to change attributes.  A nil field leaves its
// attribute alone; this is distinct from a field set to the zero value, which
// asks for that value.
type AttrChange struct {
	// Perm holds permission bits, as Attrs.Perm does.
	Perm  *uint32
	UID   *uint32
	GID   *uint32
	Size  *uint64
	Atime *time.Time
	Mtime *time.Time
}

// Empty reports whether c changes nothing.
func (c AttrChange) Empty() bool {
	return c.Perm == nil && c.UID == nil && c.GID == nil &&
		c.Size == nil && c.Atime == nil && c.Mtime == nil
}
