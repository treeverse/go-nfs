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

// ErrStaleCookie is returned by DirIteratorHandler.OpenDir when the cookie or
// verifier no longer matches the directory state.
var ErrStaleCookie = errors.New("stale NFS cookie verifier")

// DirIterator streams directory entries one at a time. go-nfs calls Next()
// and adds each entry to the response until the byte budget is exhausted, then
// reads the final Cookie and Verifier to include in the response.
type DirIterator interface {
	Next() bool
	FileInfo() fs.FileInfo
	// Cookie returns a 0-based serial index for the current entry. go-nfs
	// translates these to NFS cookies internally. callers must not add any offset.
	Cookie() uint64
	// Verifier returns the directory's cookie verifier. Stable throughout the lifetime of the iterator.
	Verifier() uint64
	Close()
}

// DirIteratorHandler is an optional interface for handlers that stream
// directory listings entry by entry. When implemented, go-nfs calls OpenDir
// instead of billy.Filesystem.ReadDir, eliminating the full-listing rebuild.
//
// cookie is a serial index: 0 means start from the beginning, k means resume
// after the entry whose Cookie() returned k. go-nfs translates NFS cookies to
// serial indices before calling OpenDir, so implementations never see the
// NFS-reserved values 0 ("." sentinel) and 1 (".." sentinel).
// verifier is 0 on the first page, or the value from the previous response.
type DirIteratorHandler interface {
	OpenDir(ctx context.Context, path string, cookie, verifier uint64) (DirIterator, error)
}
