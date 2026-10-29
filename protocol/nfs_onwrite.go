package protocol

import (
	"bytes"
	"context"
	"math"

	"github.com/treeverse/go-nfs/fsapi"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

// writeFileSync is stable_how = FILE_SYNC, the strongest of the three values
// WRITE3args may carry and the only one WRITE3resok ever reports here.
const writeFileSync uint32 = 2

type writeArgs struct {
	Handle []byte
	Offset uint64
	Count  uint32
	How    uint32
	Data   []byte
}

func onWrite(ctx context.Context, w *response) error {
	w.errorFmt = wccDataErrorFormatter
	var args writeArgs
	if err := xdr.Read(w.req.Body, &args); err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}
	fs, h, _, err := w.Server.fsFor(args.Handle)
	if err != nil {
		return err
	}

	if len(args.Data) > math.MaxInt32 || args.Count > math.MaxInt32 {
		return &NFSStatusError{NFSStatusFBig, fsapi.ErrTooBig}
	}
	// stable_how is checked but not acted on.  An out-of-range value is a
	// malformed request, while any of the three valid ones gets the same
	// answer: fsapi.Data.Write does not return until the bytes are durable,
	// so every write is reported as writeFileSync however little the client
	// asked for.
	if args.How > writeFileSync {
		return &NFSStatusError{NFSStatusInval, fsapi.ErrInval}
	}

	// Count is what the client says it sent; Data is what arrived.  Writing
	// the shorter of the two keeps a truncated request from reading past the
	// end of the buffer.
	data := args.Data
	if uint32(len(data)) > args.Count {
		data = data[:args.Count]
	}

	n, wcc, err := fs.Write(ctx, h, args.Offset, data)
	if err != nil {
		return statusError(err, NFSStatusIO)
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := writeWcc(writer, wcc); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := xdr.Write(writer, uint32(n)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := xdr.Write(writer, writeFileSync); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := xdr.Write(writer, w.Server.ID); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
