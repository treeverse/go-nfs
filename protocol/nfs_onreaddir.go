package protocol

import (
	"bytes"
	"context"
	"io"

	"github.com/treeverse/go-nfs/fsapi"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

type readDirArgs struct {
	Handle      []byte
	Cookie      uint64
	CookieVerif uint64
	Count       uint32
}

type readDirEntity struct {
	FileID uint64
	Name   []byte
	Cookie uint64
	Next   bool
}

// readDirOverhead is a conservative allowance for the fixed parts of a READDIR
// reply, and readDirPerEntry for one entry, both used to stop filling the reply
// before it outgrows what the client asked for.
const (
	readDirOverhead = 100
	readDirPerEntry = 512
)

func onReadDir(ctx context.Context, w *response) error {
	w.errorFmt = opAttrErrorFormatter
	var args readDirArgs
	if err := xdr.Read(w.req.Body, &args); err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}
	if args.Count < 1024 {
		return &NFSStatusError{NFSStatusTooSmall, io.ErrShortBuffer}
	}
	fs, dir, _, err := w.Server.fsFor(args.Handle)
	if err != nil {
		return err
	}

	entities := make([]readDirEntity, 0)
	used := uint32(readDirOverhead)

	prefix, from := dirPrefix(ctx, fs, dir, args.Cookie)
	for _, name := range prefix {
		fileID, cookie, _ := dirPrefixEntry(ctx, fs, dir, name)
		entities = append(entities, readDirEntity{
			FileID: fileID,
			Name:   []byte(name),
			Cookie: cookie,
			Next:   true,
		})
	}

	// The directory's own attributes are read before the iterator is opened.
	// A filesystem may hold the directory while a listing is open, so asking
	// it anything else in between risks taking the same lock twice.
	var dirAttrs *fsapi.Attrs
	if a, err := fs.GetAttr(ctx, dir); err == nil {
		dirAttrs = &a
	}

	it, err := fs.ReadDir(ctx, dir, from, fsapi.Verifier(args.CookieVerif))
	if err != nil {
		return statusError(err, NFSStatusServerFault)
	}
	closed := false
	defer func() {
		if !closed {
			if closeErr := it.Close(); closeErr != nil {
				Log.Errorf("closing directory iterator: %v", closeErr)
			}
		}
	}()

	eof := true
	for it.Next(ctx) {
		e := it.Entry()
		used += readDirPerEntry // TODO: better estimation.
		if used > args.Count {
			eof = false
			break
		}
		var fileID uint64
		if e.Attrs != nil {
			fileID = e.Attrs.Fileid
		}
		entities = append(entities, readDirEntity{
			FileID: fileID,
			Name:   []byte(e.Name),
			Cookie: uint64(e.Cookie),
			Next:   true,
		})
	}
	if err := it.Err(); err != nil {
		return statusError(err, NFSStatusServerFault)
	}
	verifier := it.Verifier()
	closed = true
	if err := it.Close(); err != nil {
		return statusError(err, NFSStatusServerFault)
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := writePostOpAttrs(writer, dirAttrs); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := xdr.Write(writer, uint64(verifier)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := xdr.Write(writer, len(entities) > 0); err != nil { // next
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if len(entities) > 0 {
		// No next for the last entity.
		entities[len(entities)-1].Next = false
		for _, e := range entities {
			if err := xdr.Write(writer, e); err != nil {
				return &NFSStatusError{NFSStatusServerFault, err}
			}
		}
	}
	if err := xdr.Write(writer, eof); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
