package fsapi

import (
	"context"
	"net"

	"github.com/willscott/go-nfs-client/nfs/rpc"
)

// MountStatus is the result of a mount attempt.
type MountStatus uint32

// MountStatus values.  They are the NFSv3 mountstat3 values.
const (
	MountStatusOk             MountStatus = 0
	MountStatusErrPerm        MountStatus = 1
	MountStatusErrNoEnt       MountStatus = 2
	MountStatusErrIO          MountStatus = 5
	MountStatusErrAcces       MountStatus = 13
	MountStatusErrNotDir      MountStatus = 20
	MountStatusErrInval       MountStatus = 22
	MountStatusErrNameTooLong MountStatus = 63
	MountStatusErrNotSupp     MountStatus = 10004
	MountStatusErrServerFault MountStatus = 10006
)

// AuthFlavor is a way of authenticating a caller, per RFC 1057 section 7.2.
type AuthFlavor uint32

// AuthFlavor values.
const (
	AuthFlavorNull  AuthFlavor = 0
	AuthFlavorUnix  AuthFlavor = 1
	AuthFlavorShort AuthFlavor = 2
	AuthFlavorDES   AuthFlavor = 3
)

// MountRequest is a client's request to mount an export.
type MountRequest struct {
	rpc.Header
	// Dirpath is the export the client asked for.  It is a client-supplied
	// string and nothing has checked it.
	Dirpath []byte
}

// Mounter hands out filesystems to clients.  It is what a server is built
// around: everything else reaches the filesystem through the root handle
// Mount returns.
type Mounter interface {
	// Mount answers a client's request to mount an export.  On
	// MountStatusOk it returns the filesystem, the handle of its root, and
	// the authentication flavors the client may use; on anything else the
	// other results are ignored.
	//
	// conn is the connection the request arrived on, for a Mounter that
	// decides by peer address.  It must not be retained.
	Mount(ctx context.Context, conn net.Conn, req MountRequest) (status MountStatus, fs FS, root Handle, flavors []AuthFlavor)

	// Umount is told that a client has finished with an export.  Clients
	// are not obliged to send it, and a client that crashes will not, so
	// treat it as a hint and not as the end of the mount.
	Umount(ctx context.Context, conn net.Conn, req MountRequest) error
}
