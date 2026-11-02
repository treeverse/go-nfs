package protocol

import (
	"bytes"
	"context"

	"github.com/willscott/go-nfs-client/nfs/xdr"
)

func onGetAttr(ctx context.Context, w *response) error {
	fs, h, _, err := fhArg(w)
	if err != nil {
		return err
	}

	attrs, err := fs.GetAttr(ctx, h)
	if err != nil {
		return statusError(err, NFSStatusIO)
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := xdr.Write(writer, toWire(attrs)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
