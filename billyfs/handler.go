// Package billyfs serves a billy.Filesystem through fsapi.
//
// It exists so that the handlers written against billy keep working while the
// server moves to fsapi: a Handler and the billy.Filesystem it hands out become
// an fsapi.FS, and everything that used to be a path operation happens in here.
//
// It is also where the handle-to-path cache lives.  fsapi handles come from the
// filesystem, but billy has no handles at all, so one has to be invented for it
// - which is what Handler.ToHandle and Handler.FromHandle do.  A filesystem
// written against fsapi directly needs none of this.
//
// A Handler is therefore what decides whether this package can keep the promise
// fsapi.FS makes about handles.  It must round-trip through ToHandle and
// FromHandle for the life of the mount, or report honestly through HandleLimit
// how many it holds at once: the server spends that number rather than minting
// handles the Handler will drop while a client is still using them.
package billyfs

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"time"

	billy "github.com/go-git/go-billy/v6"
	"github.com/treeverse/go-nfs/fsapi"
)

// Handler is the billy-based interface go-nfs exported before fsapi.  The root
// nfs package aliases it, so an existing implementation needs no change.
type Handler interface {
	// Required methods

	Mount(context.Context, net.Conn, fsapi.MountRequest) (fsapi.MountStatus, billy.Filesystem, []fsapi.AuthFlavor)

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

// FSStat returns metadata about a file system
type FSStat struct {
	TotalSize      uint64
	FreeSize       uint64
	AvailableSize  uint64
	TotalFiles     uint64
	FreeFiles      uint64
	AvailableFiles uint64
	// CacheHint is called "invarsec" in the nfs standard
	CacheHint time.Duration
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

// DirIterator streams directory entries one at a time.
type DirIterator interface {
	Next() bool
	// Handle returns the handle of this entry.  This handle is stable beyond the lifetime
	// of the iterator, and FromHandle reverses it.  The returned slice is immutable -
	// caller does not need to clone it.
	Handle() []byte
	// Err returns the error that stopped the iteration.
	Err() error
	FileInfo() fs.FileInfo
	// Cookie returns a 0-based serial index for the current entry. go-nfs
	// translates these to NFS cookies internally. callers must not add any offset.
	Cookie() uint64
	// Verifier returns the directory's cookie verifier. Stable throughout the lifetime of the iterator.
	Verifier() uint64
	Close()
}

// DirIteratorHandler is an optional interface for handlers that stream
// directory listings entry by entry.  When implemented, billyfs calls OpenDir
// instead of billy.Filesystem.ReadDir, eliminating the full-listing rebuild.
//
// cookie is a serial index: 0 means start from the beginning, k means resume
// after the entry whose Cookie() returned k.
type DirIteratorHandler interface {
	OpenDir(ctx context.Context, path string, cookie, verifier uint64) (DirIterator, error)
}

// UmountHandler is told of each MOUNTPROC_UMNT, which clients may skip.
type UmountHandler interface {
	Umount(ctx context.Context, conn net.Conn, req fsapi.MountRequest) error
}
