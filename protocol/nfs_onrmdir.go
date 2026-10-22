package protocol

import (
	"context"
)

// onRmDir removes an empty directory.  As with onRemove, the filesystem makes
// the type and emptiness checks part of the removal.
func onRmDir(ctx context.Context, w *response) error {
	w.errorFmt = wccDataErrorFormatter
	fs, dir, _, name, err := dirOpArgs(w)
	if err != nil {
		return err
	}
	if err := checkNewName(name); err != nil {
		return err
	}

	wcc, err := fs.RemoveDir(ctx, dir, name)
	if err != nil {
		return statusError(err, NFSStatusAccess)
	}
	return writeWccOnly(w, wcc)
}
