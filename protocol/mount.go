package protocol

import (
	"bytes"
	"context"

	"github.com/treeverse/go-nfs/fsapi"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

const (
	mountServiceID = 100005
)

func init() {
	_ = RegisterMessageHandler(mountServiceID, uint32(MountProcNull), onMountNull)
	_ = RegisterMessageHandler(mountServiceID, uint32(MountProcMount), onMount)
	_ = RegisterMessageHandler(mountServiceID, uint32(MountProcUmnt), onUMount)
}

func onMountNull(ctx context.Context, w *response) error {
	return w.writeHeader(ResponseCodeSuccess)
}

func onMount(ctx context.Context, w *response) error {
	// TODO: auth check.
	dirpath, err := xdr.ReadOpaque(w.req.Body)
	if err != nil {
		return err
	}
	req := fsapi.MountRequest{Header: w.req.Header, Dirpath: dirpath}
	status, fs, root, flavors := w.Server.Mounter.Mount(ctx, w.conn, req)

	// A mount that was granted needs an export id before its root handle can
	// be written, and running out of ids fails the mount rather than the
	// connection.
	var rootHandle []byte
	if status == fsapi.MountStatusOk {
		id, err := w.Server.exports.idFor(fs)
		if err != nil {
			Log.Errorf("mount %q: %v", dirpath, err)
			status = fsapi.MountStatusErrServerFault
		} else if rootHandle, err = encodeHandle(id, root); err != nil {
			Log.Errorf("mount %q: root handle: %v", dirpath, err)
			status = fsapi.MountStatusErrServerFault
		}
	}

	if err := w.writeHeader(ResponseCodeSuccess); err != nil {
		return err
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(status)); err != nil {
		return err
	}
	if status == fsapi.MountStatusOk {
		_ = xdr.Write(writer, rootHandle)
		_ = xdr.Write(writer, flavors)
	}
	return w.Write(writer.Bytes())
}

func onUMount(ctx context.Context, w *response) error {
	dirpath, err := xdr.ReadOpaque(w.req.Body)
	if err != nil {
		return err
	}
	req := fsapi.MountRequest{Header: w.req.Header, Dirpath: dirpath}
	if err := w.Server.Mounter.Umount(ctx, w.conn, req); err != nil {
		return err
	}
	return w.writeHeader(ResponseCodeSuccess)
}
