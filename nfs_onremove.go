package nfs

import (
	"bytes"
	"context"
	"os"

	"github.com/go-git/go-billy/v6"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

// onRemove handles the REMOVE RPC. It fails with NFSStatusIsDir,
// without removing anything, if the named entry is a directory.
func onRemove(ctx context.Context, w *response, userHandle Handler) error {
	return onRemoveObj(ctx, w, userHandle, false)
}

// isEmptyDir reports whether path has zero entries.
// It uses userHandle's DirIteratorHandler when available, checking only a single entry,
// instead of reading the full listing via fs.ReadDir.
func isEmptyDir(ctx context.Context, userHandle Handler, fs billy.Filesystem, path string) (bool, error) {
	if h, ok := userHandle.(DirIteratorHandler); ok {
		it, err := h.OpenDir(ctx, path, 0, 0)
		if err != nil {
			return false, err
		}
		defer it.Close()
		return !it.Next(), nil
	}

	contents, err := fs.ReadDir(path)
	if err != nil {
		return false, err
	}
	return len(contents) == 0, nil
}

// onRemoveObj implements the shared logic behind REMOVE and RMDIR:
// it decodes a DirOpArg request and removes the named entry.
// When directory is true, the entry must be a directory (NFSStatusNotDir otherwise)
// and must be empty (NFSStatusNotEmpty otherwise); when directory is false,
// the entry must not be a directory (NFSStatusIsDir otherwise).
func onRemoveObj(ctx context.Context, w *response, userHandle Handler, directory bool) error {
	w.errorFmt = wccDataErrorFormatter
	obj := DirOpArg{}
	if err := xdr.Read(w.req.Body, &obj); err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}
	fs, path, err := userHandle.FromHandle(obj.Handle)
	if err != nil {
		return &NFSStatusError{NFSStatusStale, err}
	}

	if !billy.CapabilityCheck(fs, billy.WriteCapability) {
		return &NFSStatusError{NFSStatusROFS, os.ErrPermission}
	}

	if len(string(obj.Filename)) > PathNameMax {
		return &NFSStatusError{NFSStatusNameTooLong, nil}
	}

	fullPath := fs.Join(path...)
	dirInfo, err := fs.Stat(fullPath)
	if err != nil {
		return statusError(err, NFSStatusIO)
	}
	if !dirInfo.IsDir() {
		return &NFSStatusError{NFSStatusNotDir, nil}
	}
	preCacheData := ToFileAttribute(dirInfo, fullPath).AsCache()

	toDelete := fs.Join(append(path, string(obj.Filename))...)
	toDeleteHandle := userHandle.ToHandle(fs, append(path, string(obj.Filename)))

	// Best-effort check: billy exposes no atomic "remove only if a directory",
	// so a target that changes type between this Lstat and the Remove below can slip through.
	// We keep the check immediately before Remove to keep that window as small as possible.
	//
	// Lstat, not Stat: POSIX rmdir()/unlink() act on the entry itself, not what it points to :
	// rmdir() must reject a symlink to an empty directory, unlink() must remove the symlink itself.
	targetInfo, err := fs.Lstat(toDelete)
	if err != nil {
		// A permission error here means an ancestor directory in toDelete's path
		// lacks its executable permission, not that toDelete itself is inaccessible.
		return statusError(err, NFSStatusIO)
	}

	if directory && !targetInfo.IsDir() {
		return &NFSStatusError{NFSStatusNotDir, nil}
	}
	if !directory && targetInfo.IsDir() {
		return &NFSStatusError{NFSStatusIsDir, nil}
	}

	if directory {
		empty, err := isEmptyDir(ctx, userHandle, fs, toDelete)
		if err != nil {
			return statusError(err, NFSStatusIO)
		}
		if !empty {
			return &NFSStatusError{NFSStatusNotEmpty, nil}
		}
	}

	err = fs.Remove(toDelete)
	if err != nil {
		// The checks above passed, so an error here means the target changed
		// underneath us in the TOCTOU window - a directory that became non-empty,
		// say.
		Log.Errorf("remove %q after passing directory checks: %v", toDelete, err)
		return statusError(err, NFSStatusIO)
	}

	if err := userHandle.InvalidateHandle(fs, toDeleteHandle); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	if err := WriteWcc(writer, preCacheData, tryStat(fs, path)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
