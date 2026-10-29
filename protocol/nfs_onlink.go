package protocol

import (
	"bytes"
	"context"

	"github.com/treeverse/go-nfs/fsapi"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

// onLink makes a second name for an existing object.
//
// LINK3args is an nfs_fh3 naming the object, then a diropargs3 naming where the
// new name goes - unlike SYMLINK, which carries a sattr3 and a path.  The
// billy-based server read it as though it were SYMLINK3args, apparently copied
// from onSymlink.
func onLink(ctx context.Context, w *response) error {
	w.errorFmt = wccDataErrorFormatter
	fs, h, id, err := fhArg(w)
	if err != nil {
		return err
	}
	_, dir, dirID, name, err := dirOpArgs(w)
	if err != nil {
		return err
	}
	if err := checkNewName(name); err != nil {
		return err
	}
	// A hard link cannot cross filesystems, and two exports are two
	// filesystems as far as a client is concerned.  Comparing the export
	// ids settles it without comparing the two FS values, which would panic
	// on a filesystem whose type Go cannot compare.
	if id != dirID {
		return &NFSStatusError{NFSStatusXDev, fsapi.ErrXDev}
	}

	linker, ok := fs.(fsapi.Linker)
	if !ok {
		return &NFSStatusError{NFSStatusNotSupp, fsapi.ErrNotSupported}
	}
	attrs, wcc, err := linker.Link(ctx, h, dir, name)
	if err != nil {
		return statusError(err, NFSStatusAccess)
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := writePostOpAttrs(writer, attrs); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := writeWcc(writer, wcc); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
