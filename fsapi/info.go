package fsapi

import "time"

// AccessMask is a set of operations, as asked about and answered by FS.Access.
type AccessMask uint32

// AccessMask bits.  They are the NFSv3 ACCESS3 values.
const (
	// AccessRead is reading a file, or reading the entries of a directory.
	AccessRead AccessMask = 1 << iota
	// AccessLookup is looking a name up in a directory.  It is meaningless
	// for anything that is not a directory.
	AccessLookup
	// AccessModify is overwriting part of a file, or removing or renaming
	// an entry of a directory.
	AccessModify
	// AccessExtend is making a file longer, or adding an entry to a
	// directory.
	AccessExtend
	// AccessDelete is removing an entry from a directory.  It is
	// meaningless for anything that is not a directory.
	AccessDelete
	// AccessExecute is executing a file.  It is meaningless for a
	// directory.
	AccessExecute
)

// AccessWrite is every operation that changes something.  A read-only
// filesystem grants none of these.
const AccessWrite = AccessModify | AccessExtend | AccessDelete

// CreateMode says what FS.Create does about a name that is already taken.
type CreateMode uint32

const (
	// CreateUnchecked creates the file, or keeps the one already there.
	// The requested attributes apply either way, except that a file already
	// there keeps everything but its size.
	CreateUnchecked CreateMode = iota
	// CreateGuarded creates the file, and fails with ErrExists if the name
	// is taken.  Create must check and create atomically.
	CreateGuarded
	// CreateExclusive creates the file and stores CreateHow.Verifier with
	// it, so that a client whose request was retried can recognize the file
	// its own earlier attempt created.  A filesystem with nowhere to keep
	// the verifier fails with ErrNotSupported.
	CreateExclusive
)

// CreateHow is how FS.Create should create a file.
type CreateHow struct {
	Mode CreateMode
	// Verifier identifies the creating client, and is meaningful only for
	// CreateExclusive.  Create stores it; a second CreateExclusive of the
	// same name with the same Verifier succeeds and returns the file that
	// is already there, while one with a different Verifier fails with
	// ErrExists.
	Verifier [8]byte
}

// FSStat is how much space a filesystem has.  Sizes are in bytes.
//
// A filesystem that does not track one of these may report a large value: it
// says "ask again later" to a client that is deciding whether a write will fit.
type FSStat struct {
	TotalSize      uint64
	FreeSize       uint64
	AvailableSize  uint64
	TotalFiles     uint64
	FreeFiles      uint64
	AvailableFiles uint64
	// CacheHint is how long a client may reuse these numbers before asking
	// again.  Zero asks it not to cache them at all.
	CacheHint time.Duration
}

// FSProperties are the optional abilities of a filesystem, as reported to
// clients.
type FSProperties uint32

// FSProperties bits.  They are the NFSv3 FSF3 values.
const (
	// FSPropertyLink is hard links.
	FSPropertyLink FSProperties = 0x0001
	// FSPropertySymlink is symbolic links.
	FSPropertySymlink FSProperties = 0x0002
	// FSPropertyHomogeneous says every object has the same limits, so a
	// client need not ask about each one.
	FSPropertyHomogeneous FSProperties = 0x0008
	// FSPropertyCanSetTime says times can be set to a requested value.
	FSPropertyCanSetTime FSProperties = 0x0010
)

// FSInfo are the fixed properties of a filesystem.  The read and write sizes
// below are advice to a client about how to split up its requests; the server
// clamps them to what it can actually carry.
type FSInfo struct {
	// ReadMax is the largest read the filesystem will serve, ReadPref the
	// size it would rather be asked for, and ReadMultiple the granularity
	// it prefers reads to be a multiple of.
	ReadMax       uint32
	ReadPref      uint32
	ReadMultiple  uint32
	WriteMax      uint32
	WritePref     uint32
	WriteMultiple uint32
	// DirPref is the preferred size of a READDIR request.
	DirPref uint32
	// MaxFileSize is the longest a file may be.
	MaxFileSize uint64
	// TimeDelta is the precision of the filesystem's timestamps.
	TimeDelta time.Duration
	// Properties are the filesystem's optional abilities.  The server adds
	// FSPropertyLink and FSPropertySymlink itself, from whether the FS
	// implements Linker and Symlinker, so leave those alone.
	Properties FSProperties
}
