package protocol

import (
	"bytes"
	"context"

	"github.com/treeverse/go-nfs/fsapi"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

func onLookup(ctx context.Context, w *response) error {
	w.errorFmt = opAttrErrorFormatter
	fs, dir, id, name, err := dirOpArgs(w)
	if err != nil {
		return err
	}

	var (
		found    fsapi.Handle
		attrs    *fsapi.Attrs
		dirAttrs *fsapi.Attrs
	)
	switch name {
	case ".":
		// A directory is its own ".", so there is nothing to look up:
		// report the same handle and its attributes twice.
		found = dir
		if a, err := fs.GetAttr(ctx, dir); err == nil {
			attrs, dirAttrs = &a, &a
		}
	case "..":
		found, attrs, err = fs.LookupParent(ctx, dir)
		if err != nil {
			return statusError(err, NFSStatusNoEnt)
		}
		if a, err := fs.GetAttr(ctx, dir); err == nil {
			dirAttrs = &a
		}
	default:
		found, attrs, dirAttrs, err = fs.Lookup(ctx, dir, name)
		if err != nil {
			return statusError(err, NFSStatusNoEnt)
		}
	}

	handle, err := wireHandle(id, found)
	if err != nil {
		return err
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := xdr.Write(writer, handle); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := writePostOpAttrs(writer, attrs); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := writePostOpAttrs(writer, dirAttrs); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
