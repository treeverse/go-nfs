package nfs

import (
	"context"
)

// onRmDir removes a directory. It fails with NFSStatusNotDir, without removing anything,
// if the named entry is not a directory.
func onRmDir(_ context.Context, w *response, userHandle Handler) error {
	return removeEntry(w, userHandle, true)
}
