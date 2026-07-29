package nfs

import (
	"context"
)

// onRmDir handles the RMDIR RPC. It fails with NFSStatusNotDir,
// without removing anything, if the named entry is not a directory,
// and with NFSStatusNotEmpty if the directory is not empty.
func onRmDir(ctx context.Context, w *response, userHandle Handler) error {
	return onRemoveObj(ctx, w, userHandle, true)
}
