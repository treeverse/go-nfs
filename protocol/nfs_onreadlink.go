package protocol

import (
	"bytes"
	"context"

	"github.com/treeverse/go-nfs/fsapi"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

func onReadLink(ctx context.Context, w *response) error {
	w.errorFmt = opAttrErrorFormatter
	fs, h, _, err := fhArg(w)
	if err != nil {
		return err
	}

	links, ok := fs.(fsapi.Symlinker)
	if !ok {
		return &NFSStatusError{NFSStatusNotSupp, fsapi.ErrNotSupported}
	}
	target, attrs, err := links.Readlink(ctx, h)
	if err != nil {
		return statusError(err, NFSStatusIO)
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := writePostOpAttrs(writer, attrs); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := xdr.Write(writer, []byte(target)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
