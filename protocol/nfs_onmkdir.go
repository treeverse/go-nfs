package protocol

import (
	"context"
)

// mkdirDefaultMode is the mode a directory gets when the client asks for none.
//
// The billy-based server used a constant written 755, which is decimal and so
// means 0o1363 - group-executable, other-writable, with the sticky bit set.
// This is the octal that was meant.
const mkdirDefaultMode = 0o755

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
	if change.Perm == nil {
		mode := uint32(mkdirDefaultMode)
		change.Perm = &mode
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
