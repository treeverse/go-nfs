package protocol

import (
	"context"

	"github.com/treeverse/go-nfs/fsapi"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

func onMknod(ctx context.Context, w *response) error {
	w.errorFmt = wccDataErrorFormatter
	fs, dir, id, name, err := dirOpArgs(w)
	if err != nil {
		return err
	}
	if err := checkNewName(name); err != nil {
		return err
	}

	ftype, err := xdr.ReadUint32(w.req.Body)
	if err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}

	special, ok := fs.(fsapi.SpecialFiler)
	if !ok {
		return &NFSStatusError{NFSStatusNotSupp, fsapi.ErrNotSupported}
	}

	var (
		h      fsapi.Handle
		attrs  *fsapi.Attrs
		wcc    fsapi.Wcc
		change fsapi.AttrChange
	)
	switch typ := fsapi.FileType(ftype); typ {
	case fsapi.TypeBlock, fsapi.TypeChar:
		// devicedata3 is the attributes, then the device numbers.
		if change, err = readAttrChange(w.req.Body); err != nil {
			return &NFSStatusError{NFSStatusInval, err}
		}
		// Declared, not short-declared: a := here would scope a second err
		// to this case, and the Mknod below would assign to that one while
		// the check after the switch read the outer one.
		var major, minor uint32
		if major, err = xdr.ReadUint32(w.req.Body); err != nil {
			return &NFSStatusError{NFSStatusInval, err}
		}
		if minor, err = xdr.ReadUint32(w.req.Body); err != nil {
			return &NFSStatusError{NFSStatusInval, err}
		}
		h, attrs, wcc, err = special.Mknod(ctx, dir, name, typ, major, minor, change)
	case fsapi.TypeFIFO:
		if change, err = readAttrChange(w.req.Body); err != nil {
			return &NFSStatusError{NFSStatusInval, err}
		}
		h, attrs, wcc, err = special.Mkfifo(ctx, dir, name, change)
	case fsapi.TypeSocket:
		if change, err = readAttrChange(w.req.Body); err != nil {
			return &NFSStatusError{NFSStatusInval, err}
		}
		h, attrs, wcc, err = special.Socket(ctx, dir, name, change)
	default:
		// MKNOD carries no arguments for a regular file, directory or
		// symbolic link: the client is meant to use CREATE, MKDIR or
		// SYMLINK for those.
		return &NFSStatusError{NFSStatusBadType, fsapi.ErrInval}
	}
	if err != nil {
		return statusError(err, NFSStatusAccess)
	}

	handle, err := wireHandle(id, h)
	if err != nil {
		return err
	}
	return writeNewObject(w, handle, attrs, wcc)
}
