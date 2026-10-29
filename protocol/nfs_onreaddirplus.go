package protocol

import (
	"bytes"
	"context"

	"github.com/treeverse/go-nfs/fsapi"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

type readDirPlusArgs struct {
	Handle      []byte
	Cookie      uint64
	CookieVerif uint64
	DirCount    uint32
	MaxCount    uint32
}

type readDirPlusEntity struct {
	FileID     uint64
	Name       []byte
	Cookie     uint64
	Attributes *fileAttribute `xdr:"optional"`
	Handle     *[]byte        `xdr:"optional"`
	Next       bool
}

// readDirPlusNameOverhead is the allowance per entry against the client's
// DirCount, which bounds the name part of the reply alone.
const readDirPlusNameOverhead = 20

func onReadDirPlus(ctx context.Context, w *response) error {
	w.errorFmt = opAttrErrorFormatter
	var args readDirPlusArgs
	if err := xdr.Read(w.req.Body, &args); err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}
	// In the test case, nfs-client sends DirCount = 512 and MaxCount = 4096.
	if args.DirCount < 512 || args.MaxCount < 4096 {
		return &NFSStatusError{NFSStatusTooSmall, nil}
	}
	fs, dir, id, err := w.Server.fsFor(args.Handle)
	if err != nil {
		return err
	}

	entities := make([]readDirPlusEntity, 0)
	dirBytes := uint32(0)
	used := uint32(readDirOverhead)

	prefix, from := dirPrefix(ctx, fs, dir, args.Cookie)
	for _, name := range prefix {
		fileID, cookie, attrs := dirPrefixEntry(ctx, fs, dir, name)
		entry := readDirPlusEntity{
			FileID: fileID,
			Name:   []byte(name),
			Cookie: cookie,
			Next:   true,
		}
		if attrs != nil {
			wire := toWire(*attrs)
			entry.Attributes = &wire
		}
		entities = append(entities, entry)
	}

	// Read before the iterator is opened: see onReadDir.
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
	maxEntries := maxListEntries(fs)
	listed := 0
	for it.Next(ctx) {
		e := it.Entry()
		dirBytes += uint32(len(e.Name) + readDirPlusNameOverhead)
		used += readDirPerEntry // TODO: better estimation.
		if dirBytes > args.DirCount || used > args.MaxCount ||
			(maxEntries > 0 && listed >= maxEntries) {
			eof = false
			break
		}
		listed++

		entry := readDirPlusEntity{
			Name:   []byte(e.Name),
			Cookie: uint64(e.Cookie),
			Next:   true,
		}
		if e.Attrs != nil {
			entry.FileID = e.Attrs.Fileid
			wire := toWire(*e.Attrs)
			entry.Attributes = &wire
		}
		// An entry whose handle cannot be encoded is reported without
		// one: the client then looks the name up, which costs a round
		// trip but is correct.
		if handle, err := encodeHandle(id, e.Handle); err == nil {
			entry.Handle = &handle
		} else {
			Log.Errorf("readdirplus %q: %v", e.Name, err)
		}
		entities = append(entities, entry)
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
