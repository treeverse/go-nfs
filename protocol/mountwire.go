package protocol

import (
	"github.com/willscott/go-nfs-client/nfs/rpc"
)

// FHSize is the maximum size of a FileHandle
const FHSize = 64

// MNTNameLen is the maximum size of a mount name
const MNTNameLen = 255

// MntPathLen is the maximum size of a mount path
const MntPathLen = 1024

// FileHandle maps to a fhandle3
type FileHandle []byte

// MountProcedure is the valid RPC calls for the mount service.
type MountProcedure uint32

// MountProcedure Codes
const (
	MountProcNull MountProcedure = iota
	MountProcMount
	MountProcDump
	MountProcUmnt
	MountProcUmntAll
	MountProcExport
)

func (m MountProcedure) String() string {
	switch m {
	case MountProcNull:
		return "Null"
	case MountProcMount:
		return "Mount"
	case MountProcDump:
		return "Dump"
	case MountProcUmnt:
		return "Umnt"
	case MountProcUmntAll:
		return "UmntAll"
	case MountProcExport:
		return "Export"
	default:
		return "Unknown"
	}
}

// MountResponse is the server's response with status `MountStatusOk`
type MountResponse struct {
	rpc.Header
	FileHandle
	AuthFlavors []int
}
