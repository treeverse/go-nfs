package nfs

import (
	"context"
	"errors"
	"io/fs"
	"net"

	billy "github.com/go-git/go-billy/v6"
)

// Handler represents the interface of the file system / vfs being exposed over NFS
type Handler interface {
	// Required methods

	Mount(context.Context, net.Conn, MountRequest) (MountStatus, billy.Filesystem, []AuthFlavor)

	// Change can return 'nil' if filesystem is read-only
	// If the returned value can be cast to `UnixChange`, mknod and link RPCs will be available.
	Change(billy.Filesystem) billy.Change

	// Optional methods - generic helpers or trivial implementations can be sufficient depending on use case.

	// Fill in information about a file system's free space.
	FSStat(context.Context, billy.Filesystem, *FSStat) error

	// represent file objects as opaque references
	// Can be safely implemented via helpers/cachinghandler.
	ToHandle(fs billy.Filesystem, path []string) []byte
	FromHandle(fh []byte) (billy.Filesystem, []string, error)
	InvalidateHandle(billy.Filesystem, []byte) error

	// How many handles can be safely maintained by the handler.
	HandleLimit() int
}

// UnixChange extends the billy `Change` interface with support for special files.
type UnixChange interface {
	billy.Change
	Mknod(path string, mode uint32, major uint32, minor uint32) error
	Mkfifo(path string, mode uint32) error
	Socket(path string) error
	Link(path string, link string) error
}

// CachingHandler represents the optional caching work that a user may wish to over-ride with
// their own implementations, but which can be otherwise provided through defaults.
type CachingHandler interface {
	VerifierFor(path string, contents []fs.FileInfo) uint64

	// fs.FileInfo needs to be sorted by Name(), nil in case of a cache-miss
	DataForVerifier(path string, verifier uint64) []fs.FileInfo
}

// ErrStaleCookie is returned by PagedDirHandler.ReadDirPage when the
// cookie verifier no longer matches the directory state.  go-nfs maps
// this to NFSStatusBadCookie so the client restarts the listing.
var ErrStaleCookie = errors.New("stale NFS cookie verifier")

// PagedDirHandler is an optional interface for handlers that can serve
// directory listings one page at a time without materialising the full
// listing.  When implemented, go-nfs calls ReadDirPage instead of
// billy.Filesystem.ReadDir.
//
// startAfterCookie is 0 for the first page, or the cookie of the last
// entry from the previous page.  verifier is 0 for the first page, or
// the value returned by the previous call.  Entries must be sorted by
// Name().  The implementation should return ErrStaleCookie when the
// directory has changed and the listing cannot be continued.
type PagedDirHandler interface {
	ReadDirPage(ctx context.Context, path string, startAfterCookie, verifier uint64, maxEntries int) (entries []fs.FileInfo, newVerifier uint64, eof bool, err error)
}
