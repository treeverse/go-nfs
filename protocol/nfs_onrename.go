package protocol

import (
	"bytes"
	"context"

	"github.com/treeverse/go-nfs/fsapi"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

// doubleWccErrorBody is the two empty wcc_data a failed RENAME still owes the
// client, one for each directory.
var doubleWccErrorBody = [16]byte{}

func onRename(ctx context.Context, w *response) error {
	w.errorFmt = errFormatterWithBody(doubleWccErrorBody[:])
	fromFS, fromDir, fromID, fromName, err := dirOpArgs(w)
	if err != nil {
		return err
	}
	_, toDir, toID, toName, err := dirOpArgs(w)
	if err != nil {
		return err
	}
	if err := checkNewName(fromName); err != nil {
		return err
	}
	if err := checkNewName(toName); err != nil {
		return err
	}
	// Both directories must belong to one filesystem.  The billy-based
	// server compared reported fsids and fell back to comparing the two
	// Filesystem values, which needed a backend to report an fsid just to
	// make rename work; an export is one filesystem, so comparing export
	// ids settles it - and does not panic on an FS Go cannot compare.
	if fromID != toID {
		return &NFSStatusError{NFSStatusXDev, fsapi.ErrXDev}
	}

	fromWcc, toWcc, err := fromFS.Rename(ctx, fromDir, fromName, toDir, toName)
	if err != nil {
		return statusError(err, NFSStatusAccess)
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := writeWcc(writer, fromWcc); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := writeWcc(writer, toWcc); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
