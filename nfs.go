// Package nfs serves a billy.Filesystem over NFSv3.
//
// It is a thin layer over two packages that do the work, and is kept so that
// code written against the billy-based interface needs no change:
//
//   - fsapi is the interface the server actually addresses its backing store
//     through: handle-based, with a context on every call.
//   - protocol implements NFSv3 on top of fsapi.
//   - billyfs turns a Handler, and the billy.Filesystem it hands out, into an
//     fsapi.FS.
//
// A new filesystem is better written against fsapi directly: it then issues its
// own handles, instead of having them invented for it by ToHandle and
// FromHandle, and it is handed the request's context rather than having to
// stash one.
package nfs

import (
	"context"
	"net"

	"github.com/treeverse/go-nfs/billyfs"
	"github.com/treeverse/go-nfs/fsapi"
	"github.com/treeverse/go-nfs/protocol"
)

// The billy-based filesystem interface.  These live in billyfs, which is what
// implements them against fsapi.
type (
	Handler            = billyfs.Handler
	FSStat             = billyfs.FSStat
	UnixChange         = billyfs.UnixChange
	CachingHandler     = billyfs.CachingHandler
	DirIterator        = billyfs.DirIterator
	DirIteratorHandler = billyfs.DirIteratorHandler
	UmountHandler      = billyfs.UmountHandler
)

// ErrStaleCookie is returned by DirIteratorHandler.OpenDir when the cookie or
// verifier no longer matches the directory state.
var ErrStaleCookie = billyfs.ErrStaleCookie

// The mount protocol's types.  These live in fsapi, because a Mounter answers
// in them.
type (
	MountRequest = fsapi.MountRequest
	MountStatus  = fsapi.MountStatus
	AuthFlavor   = fsapi.AuthFlavor
)

// MountStatus codes
const (
	MountStatusOk             = fsapi.MountStatusOk
	MountStatusErrPerm        = fsapi.MountStatusErrPerm
	MountStatusErrNoEnt       = fsapi.MountStatusErrNoEnt
	MountStatusErrIO          = fsapi.MountStatusErrIO
	MountStatusErrAcces       = fsapi.MountStatusErrAcces
	MountStatusErrNotDir      = fsapi.MountStatusErrNotDir
	MountStatusErrInval       = fsapi.MountStatusErrInval
	MountStatusErrNameTooLong = fsapi.MountStatusErrNameTooLong
	MountStatusErrNotSupp     = fsapi.MountStatusErrNotSupp
	MountStatusErrServerFault = fsapi.MountStatusErrServerFault
)

// AuthFlavor codes
const (
	AuthFlavorNull  = fsapi.AuthFlavorNull
	AuthFlavorUnix  = fsapi.AuthFlavorUnix
	AuthFlavorShort = fsapi.AuthFlavorShort
	AuthFlavorDES   = fsapi.AuthFlavorDES
)

// The protocol's own types.
type (
	NFSStatus      = protocol.NFSStatus
	NFSStatusError = protocol.NFSStatusError
	NFSProcedure   = protocol.NFSProcedure
	MountProcedure = protocol.MountProcedure
	FileHandle     = protocol.FileHandle
	FileTime       = protocol.FileTime
	RPCError       = protocol.RPCError
	ResponseCode   = protocol.ResponseCode
	Logger         = protocol.Logger
	LogLevel       = protocol.LogLevel
)

// NFSStatus codes
const (
	NFSStatusOk          = protocol.NFSStatusOk
	NFSStatusPerm        = protocol.NFSStatusPerm
	NFSStatusNoEnt       = protocol.NFSStatusNoEnt
	NFSStatusIO          = protocol.NFSStatusIO
	NFSStatusNXIO        = protocol.NFSStatusNXIO
	NFSStatusAccess      = protocol.NFSStatusAccess
	NFSStatusExist       = protocol.NFSStatusExist
	NFSStatusXDev        = protocol.NFSStatusXDev
	NFSStatusNoDev       = protocol.NFSStatusNoDev
	NFSStatusNotDir      = protocol.NFSStatusNotDir
	NFSStatusIsDir       = protocol.NFSStatusIsDir
	NFSStatusInval       = protocol.NFSStatusInval
	NFSStatusFBig        = protocol.NFSStatusFBig
	NFSStatusNoSPC       = protocol.NFSStatusNoSPC
	NFSStatusROFS        = protocol.NFSStatusROFS
	NFSStatusMlink       = protocol.NFSStatusMlink
	NFSStatusNameTooLong = protocol.NFSStatusNameTooLong
	NFSStatusNotEmpty    = protocol.NFSStatusNotEmpty
	NFSStatusDQuot       = protocol.NFSStatusDQuot
	NFSStatusStale       = protocol.NFSStatusStale
	NFSStatusRemote      = protocol.NFSStatusRemote
	NFSStatusBadHandle   = protocol.NFSStatusBadHandle
	NFSStatusNotSync     = protocol.NFSStatusNotSync
	NFSStatusBadCookie   = protocol.NFSStatusBadCookie
	NFSStatusNotSupp     = protocol.NFSStatusNotSupp
	NFSStatusTooSmall    = protocol.NFSStatusTooSmall
	NFSStatusServerFault = protocol.NFSStatusServerFault
	NFSStatusBadType     = protocol.NFSStatusBadType
	NFSStatusJukebox     = protocol.NFSStatusJukebox
)

// NFSProcedure codes
const (
	NFSProcedureNull        = protocol.NFSProcedureNull
	NFSProcedureGetAttr     = protocol.NFSProcedureGetAttr
	NFSProcedureSetAttr     = protocol.NFSProcedureSetAttr
	NFSProcedureLookup      = protocol.NFSProcedureLookup
	NFSProcedureAccess      = protocol.NFSProcedureAccess
	NFSProcedureReadlink    = protocol.NFSProcedureReadlink
	NFSProcedureRead        = protocol.NFSProcedureRead
	NFSProcedureWrite       = protocol.NFSProcedureWrite
	NFSProcedureCreate      = protocol.NFSProcedureCreate
	NFSProcedureMkDir       = protocol.NFSProcedureMkDir
	NFSProcedureSymlink     = protocol.NFSProcedureSymlink
	NFSProcedureMkNod       = protocol.NFSProcedureMkNod
	NFSProcedureRemove      = protocol.NFSProcedureRemove
	NFSProcedureRmDir       = protocol.NFSProcedureRmDir
	NFSProcedureRename      = protocol.NFSProcedureRename
	NFSProcedureLink        = protocol.NFSProcedureLink
	NFSProcedureReadDir     = protocol.NFSProcedureReadDir
	NFSProcedureReadDirPlus = protocol.NFSProcedureReadDirPlus
	NFSProcedureFSStat      = protocol.NFSProcedureFSStat
	NFSProcedureFSInfo      = protocol.NFSProcedureFSInfo
	NFSProcedurePathConf    = protocol.NFSProcedurePathConf
	NFSProcedureCommit      = protocol.NFSProcedureCommit
)

// MountProcedure codes
const (
	MountProcNull    = protocol.MountProcNull
	MountProcMount   = protocol.MountProcMount
	MountProcDump    = protocol.MountProcDump
	MountProcUmnt    = protocol.MountProcUmnt
	MountProcUmntAll = protocol.MountProcUmntAll
	MountProcExport  = protocol.MountProcExport
)

// Sizes of the mount protocol's fields.
const (
	FHSize      = protocol.FHSize
	MNTNameLen  = protocol.MNTNameLen
	MntPathLen  = protocol.MntPathLen
	PathNameMax = protocol.PathNameMax
	MaxRead     = protocol.MaxRead
	CheckRead   = protocol.CheckRead
)

// Log levels
const (
	PanicLevel = protocol.PanicLevel
	FatalLevel = protocol.FatalLevel
	ErrorLevel = protocol.ErrorLevel
	WarnLevel  = protocol.WarnLevel
	InfoLevel  = protocol.InfoLevel
	DebugLevel = protocol.DebugLevel
	TraceLevel = protocol.TraceLevel
)

// Log is where this package and the protocol under it write.  Set it through
// SetLogger, so that both are changed together.
var Log = protocol.Log

// SetLogger directs this package and the protocol under it at logger.
func SetLogger(logger Logger) {
	Log = logger
	protocol.SetLogger(logger)
}

// ToNFSTime generates the nfs 64bit time format from a golang time.
var ToNFSTime = protocol.ToNFSTime

// Server is a handle to the listening NFS server.
type Server struct {
	Handler
	ID [8]byte
	context.Context
}

// Serve listens on the provided listener port for incoming client requests.
func (s *Server) Serve(l net.Listener) error {
	inner := &protocol.Server{
		Mounter: billyfs.NewMounter(s.Handler),
		ID:      s.ID,
		Context: s.Context,
	}
	return inner.Serve(l)
}

// Serve is a singleton listener paralleling http.Serve
func Serve(l net.Listener, handler Handler) error {
	srv := &Server{Handler: handler}
	return srv.Serve(l)
}
