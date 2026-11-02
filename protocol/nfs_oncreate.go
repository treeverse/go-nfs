package protocol

import (
	"context"

	"github.com/treeverse/go-nfs/fsapi"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

func onCreate(ctx context.Context, w *response) error {
	w.errorFmt = wccDataErrorFormatter
	fs, dir, id, name, err := dirOpArgs(w)
	if err != nil {
		return err
	}
	if err := checkNewName(name); err != nil {
		return err
	}

	how, change, err := readCreateHow(w)
	if err != nil {
		return err
	}

	h, attrs, wcc, err := fs.Create(ctx, dir, name, how, change)
	if err != nil {
		return statusError(err, NFSStatusAccess)
	}
	handle, err := wireHandle(id, h)
	if err != nil {
		return err
	}
	return writeNewObject(w, handle, attrs, wcc)
}

// readCreateHow reads a createhow3: which of the three creation modes the
// client asked for, and either the attributes to give the new file or the
// verifier that identifies the client's own attempt.
func readCreateHow(w *response) (fsapi.CreateHow, fsapi.AttrChange, error) {
	var (
		how    fsapi.CreateHow
		change fsapi.AttrChange
	)
	mode, err := xdr.ReadUint32(w.req.Body)
	if err != nil {
		return how, change, &NFSStatusError{NFSStatusInval, err}
	}

	switch fsapi.CreateMode(mode) {
	case fsapi.CreateUnchecked, fsapi.CreateGuarded:
		how.Mode = fsapi.CreateMode(mode)
		if change, err = readAttrChange(w.req.Body); err != nil {
			return how, change, &NFSStatusError{NFSStatusInval, err}
		}
	case fsapi.CreateExclusive:
		how.Mode = fsapi.CreateExclusive
		if err := xdr.Read(w.req.Body, &how.Verifier); err != nil {
			return how, change, &NFSStatusError{NFSStatusInval, err}
		}
	default:
		return how, change, &NFSStatusError{NFSStatusInval, fsapi.ErrInval}
	}
	return how, change, nil
}
