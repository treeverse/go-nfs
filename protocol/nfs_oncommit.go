package protocol

import (
	"bytes"
	"context"

	"github.com/treeverse/go-nfs/fsapi"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

type commitArgs struct {
	Handle []byte
	Offset uint64
	Count  uint32
}

// onCommit answers a client asking that earlier writes be made durable.  They
// already are: fsapi.Data.Write does not return until the bytes are, and every
// write is reported committed, so a COMMIT can find nothing outstanding.
// Succeeding without doing anything is the conforming answer here, not a
// shortcut.
func onCommit(ctx context.Context, w *response) error {
	w.errorFmt = wccDataErrorFormatter
	var args commitArgs
	if err := xdr.Read(w.req.Body, &args); err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}
	fs, h, _, err := w.Server.fsFor(args.Handle)
	if err != nil {
		return err
	}

	var wcc fsapi.Wcc
	if attrs, err := fs.GetAttr(ctx, h); err == nil {
		wcc.Post = &attrs
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := writeWcc(writer, wcc); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	// The write verifier, which COMMIT3resok requires.  A client compares it
	// against the one its writes returned, to find out whether anything it
	// had not yet committed was lost.
	if err := xdr.Write(writer, w.Server.ID); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
