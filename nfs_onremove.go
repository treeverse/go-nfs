package nfs

import (
	"bytes"
	"context"
	"os"

	"github.com/go-git/go-billy/v6"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

func onRemove(_ context.Context, w *response, userHandle Handler) error {
	return removeEntry(w, userHandle, false)
}

// removeEntry implements the shared logic behind REMOVE and RMDIR:
// it decodes a DirOpArg request and removes the named entry. When requireDir is set,
// it fails with NFSStatusNotDir, without removing anything, if the target is not a directory.
func removeEntry(w *response, userHandle Handler, requireDir bool) error {
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
		if os.IsNotExist(err) {
			return &NFSStatusError{NFSStatusNoEnt, err}
		}
		if os.IsPermission(err) {
			return &NFSStatusError{NFSStatusAccess, err}
		}
		return &NFSStatusError{NFSStatusIO, err}
	}
	if !dirInfo.IsDir() {
		return &NFSStatusError{NFSStatusNotDir, nil}
	}
	preCacheData := ToFileAttribute(dirInfo, fullPath).AsCache()

	toDelete := fs.Join(append(path, string(obj.Filename))...)
	toDeleteHandle := userHandle.ToHandle(fs, append(path, string(obj.Filename)))

	if requireDir {
		// Best-effort check: billy exposes no atomic "remove only if a directory",
		// so a target that changes type between this Stat and the Remove below can slip through.
		// We keep the check immediately before Remove to keep that window as small as possible.
		targetInfo, err := fs.Stat(toDelete)
		if err != nil {
			if os.IsNotExist(err) {
				return &NFSStatusError{NFSStatusNoEnt, err}
			}
			if os.IsPermission(err) {
				return &NFSStatusError{NFSStatusAccess, err}
			}
			return &NFSStatusError{NFSStatusIO, err}
		}
		if !targetInfo.IsDir() {
			return &NFSStatusError{NFSStatusNotDir, nil}
		}
	}

	err = fs.Remove(toDelete)
	if err != nil {
		if os.IsNotExist(err) {
			return &NFSStatusError{NFSStatusNoEnt, err}
		}
		if os.IsPermission(err) {
			return &NFSStatusError{NFSStatusAccess, err}
		}
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
