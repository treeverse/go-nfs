package protocol

import (
	"context"
)

func onMkdir(ctx context.Context, w *response) error {
	w.errorFmt = wccDataErrorFormatter
	fs, dir, id, name, err := dirOpArgs(w)
	if err != nil {
		return err
	}
	if err := checkNewName(name); err != nil {
		return err
	}

	change, err := readAttrChange(w.req.Body)
	if err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}

	h, attrs, wcc, err := fs.Mkdir(ctx, dir, name, change)
	if err != nil {
		return statusError(err, NFSStatusAccess)
	}
	handle, err := wireHandle(id, h)
	if err != nil {
		return err
	}
	return writeNewObject(w, handle, attrs, wcc)
}
