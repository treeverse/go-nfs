package protocol

import (
	"bytes"
	"context"

	"github.com/treeverse/go-nfs/fsapi"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

type nfsReadArgs struct {
	Handle []byte
	Offset uint64
	Count  uint32
}

type nfsReadResponse struct {
	Count uint32
	EOF   uint32
	Data  []byte
}

// MaxRead is the advertised largest buffer the server is willing to read
const MaxRead = 1 << 24

// CheckRead is the size above which the server asks the filesystem how long
// the file is before allocating anything.  Below it the buffer is small enough
// that the extra call costs more than it saves.
//
// It matters because the data is held more than once on its way out: once in
// the buffer read into, once in the reply being marshalled, and once in the
// response being assembled.  Allocating what the client asked for rather than
// what the file holds therefore costs several times over, and one request for
// megabytes at the end of a short file would pay all of it.
const CheckRead = 1 << 15

// readCount is how many bytes to allocate for a read of requested bytes at
// offset, from a file of size bytes.  It is the length the read can actually
// return, which is nothing at all once offset has reached the end.
func readCount(requested uint32, offset, size uint64) uint32 {
	if offset >= size {
		return 0
	}
	if available := size - offset; available < uint64(requested) {
		return uint32(available)
	}
	return requested
}

func onRead(ctx context.Context, w *response) error {
	w.errorFmt = opAttrErrorFormatter
	var args nfsReadArgs
	if err := xdr.Read(w.req.Body, &args); err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}
	fs, h, _, err := w.Server.fsFor(args.Handle)
	if err != nil {
		return err
	}

	count := args.Count
	if count > MaxRead {
		count = MaxRead
	}
	if count > CheckRead {
		// A failure here fails the read rather than falling back to the
		// unclamped buffer: a client able to provoke it could otherwise
		// use it to make the server allocate whatever it asked for.
		attrs, err := fs.GetAttr(ctx, h)
		if err != nil {
			return statusError(err, NFSStatusIO)
		}
		if args.Offset >= attrs.Size {
			// Nothing to read, and the filesystem cannot say so: handed
			// an empty buffer it returns no data and no end-of-file,
			// which a client reads as "ask again".
			return writeReadReply(w, nil, true, &attrs)
		}
		count = readCount(count, args.Offset, attrs.Size)
	}

	buf := make([]byte, count)
	n, eof, attrs, err := fs.Read(ctx, h, args.Offset, buf)
	if err != nil {
		return statusError(err, NFSStatusIO)
	}
	return writeReadReply(w, buf[:n], eof, attrs)
}

// writeReadReply sends a READ3resok: the file's attributes, then the data and
// whether the read reached the end of the file.
func writeReadReply(w *response, data []byte, eof bool, attrs *fsapi.Attrs) error {
	resp := nfsReadResponse{Count: uint32(len(data)), Data: data}
	if eof {
		resp.EOF = 1
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := writePostOpAttrs(writer, attrs); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := xdr.Write(writer, resp); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
