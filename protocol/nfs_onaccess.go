package protocol

import (
	"bytes"
	"context"

	"github.com/treeverse/go-nfs/fsapi"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

func onAccess(ctx context.Context, w *response) error {
	w.errorFmt = opAttrErrorFormatter
	fs, h, _, err := fhArg(w)
	if err != nil {
		return err
	}
	mask, err := xdr.ReadUint32(w.req.Body)
	if err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}

	granted, attrs, err := fs.Access(ctx, h, fsapi.AccessMask(mask))
	if err != nil {
		return statusError(err, NFSStatusIO)
	}
	// RFC 1813 3.3.4: the reply may not grant anything the client did not
	// ask about.
	granted &= fsapi.AccessMask(mask)

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := writePostOpAttrs(writer, attrs); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := xdr.Write(writer, uint32(granted)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
