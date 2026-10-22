// Package fsapi is the interface between the NFS server and the filesystem it
// exports.  It is handle-based: the server receives a single root handle when a
// client mounts, and every later call names its object by a handle that came
// from the filesystem.  Every call takes a context.
//
// fsapi imports neither billy nor any other go-nfs package, so a filesystem can
// depend on it without depending on the server.  In particular a filesystem
// reports failures with the sentinels in errors.go rather than with NFS status
// codes, which belong to the protocol and not here.
package fsapi

// Handle identifies one filesystem object.  It is opaque to the server, which
// never constructs one: handles come from Mount (the root), Lookup,
// LookupParent, Create, Mkdir and the entries of a ReadDir.
//
// It is a string, not a []byte, so that it is immutable and usable as a map
// key.  It is not required to be a valid UTF-8 encoding of a string.
type Handle string

// MaxHandleSize is the longest handle a filesystem may return.
//
// NFSv3 allows 64 bytes of fhandle3 on the wire, and the server spends some of
// that on itself - two bytes of export id today - so a filesystem does not get
// the whole budget.  32 is what Everest's handles need and leaves most of the
// difference spare; it can be raised toward 62 if a filesystem needs longer
// ones, as long as whatever the server prefixes still fits alongside.
const MaxHandleSize = 32
