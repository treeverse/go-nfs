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
	if os.IsNotExist(err) {
		return &NFSStatusError{NFSStatusNoEnt, err}
	}
	if os.IsPermission(err) {
		return &NFSStatusError{NFSStatusAccess, err}
	}
	if err != nil {
		return &NFSStatusError{NFSStatusIO, err}
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
	if os.IsNotExist(err) {
		return &NFSStatusError{NFSStatusNoEnt, err}
	}
	if os.IsPermission(err) {
		return &NFSStatusError{NFSStatusAccess, err}
	}
	if err != nil {
		return &NFSStatusError{NFSStatusIO, err}
	}

	if directory && !targetInfo.IsDir() {
		return &NFSStatusError{NFSStatusNotDir, nil}
	}
	if !directory && targetInfo.IsDir() {
		return &NFSStatusError{NFSStatusIsDir, nil}
	}

	if directory {
		contents, err := fs.ReadDir(toDelete)
		if os.IsNotExist(err) {
			return &NFSStatusError{NFSStatusNoEnt, err}
		}
		if os.IsPermission(err) {
			return &NFSStatusError{NFSStatusAccess, err}
		}
		if err != nil {
			return &NFSStatusError{NFSStatusIO, err}
		}
		if len(contents) > 0 {
			return &NFSStatusError{NFSStatusNotEmpty, nil}
		}
	}

	err = fs.Remove(toDelete)
	if os.IsNotExist(err) {
		return &NFSStatusError{NFSStatusNoEnt, err}
	}
	if os.IsPermission(err) {
		return &NFSStatusError{NFSStatusAccess, err}
	}
	if err != nil {
		// We passed all directory/empty checks above, so an error here likely means
		// the target changed underneath us (the TOCTOU window noted above) - e.g. a
		// directory that just became non-empty. We report NFSStatusIO rather than
		// trying to distinguish that case, since billy doesn't expose a portable way
		// to recognize it.
		Log.Errorf("remove %q after passing directory checks: %v", toDelete, err)
		return &NFSStatusError{NFSStatusIO, err}
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
