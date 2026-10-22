package protocol

import (
	"context"
)

// onRemove removes a name that is not a directory.  The filesystem checks the
// type and removes atomically, so there is no window here in which the name
// could become a directory between the two.
func onRemove(ctx context.Context, w *response) error {
	w.errorFmt = wccDataErrorFormatter
	fs, dir, _, name, err := dirOpArgs(w)
	if err != nil {
		return err
	}
	if err := checkNewName(name); err != nil {
		return err
	}

	wcc, err := fs.Remove(ctx, dir, name)
	if err != nil {
		return statusError(err, NFSStatusAccess)
	}
	return writeWccOnly(w, wcc)
}
