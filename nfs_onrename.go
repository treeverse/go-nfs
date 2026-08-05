package nfs

import (
	"bytes"
	"context"
	"os"
	"reflect"

	"github.com/go-git/go-billy/v6"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

var doubleWccErrorBody = [16]byte{}

func onRename(ctx context.Context, w *response, userHandle Handler) error {
	w.errorFmt = errFormatterWithBody(doubleWccErrorBody[:])
	from := DirOpArg{}
	err := xdr.Read(w.req.Body, &from)
	if err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}
	fs, fromPath, err := userHandle.FromHandle(from.Handle)
	if err != nil {
		return &NFSStatusError{NFSStatusStale, err}
	}

	to := DirOpArg{}
	if err = xdr.Read(w.req.Body, &to); err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}
	fs2, toPath, err := userHandle.FromHandle(to.Handle)
	if err != nil {
		return &NFSStatusError{NFSStatusStale, err}
	}
	if !billy.CapabilityCheck(fs, billy.WriteCapability) {
		return &NFSStatusError{NFSStatusROFS, os.ErrPermission}
	}

	if len(string(from.Filename)) > PathNameMax || len(string(to.Filename)) > PathNameMax {
		return &NFSStatusError{NFSStatusNameTooLong, os.ErrInvalid}
	}

	fromDirPath := fs.Join(fromPath...)
	fromDirInfo, err := fs.Stat(fromDirPath)
	if err != nil {
		return statusError(err, NFSStatusIO)
	}
	if !fromDirInfo.IsDir() {
		return &NFSStatusError{NFSStatusNotDir, nil}
	}
	fromDirAttr := ToFileAttribute(fromDirInfo, fromDirPath)
	preCacheData := fromDirAttr.AsCache()

	// Resolve the destination against its own filesystem: fs2 is what to.Handle
	// named, and comparing two attributes read through fs would compare fs to
	// itself.
	toDirPath := fs2.Join(toPath...)
	toDirInfo, err := fs2.Stat(toDirPath)
	if err != nil {
		return statusError(err, NFSStatusIO)
	}
	if !toDirInfo.IsDir() {
		return &NFSStatusError{NFSStatusNotDir, nil}
	}
	toDirAttr := ToFileAttribute(toDirInfo, toDirPath)
	preDestData := toDirAttr.AsCache()

	// RFC 1813 3.3.14: same file system means "the fsid fields in the attributes
	// for the directories are the same".  A Filesystem that reports no fsid
	// leaves both zero, which decides nothing, so keep comparing the
	// filesystems for those.
	if fromDirAttr.FSID != toDirAttr.FSID {
		return &NFSStatusError{NFSStatusXDev, os.ErrInvalid}
	}
	if fromDirAttr.FSID == 0 && !reflect.DeepEqual(fs, fs2) {
		return &NFSStatusError{NFSStatusNotSupp, os.ErrPermission}
	}

	oldHandle := userHandle.ToHandle(fs, append(fromPath, string(from.Filename)))

	fromLoc := fs.Join(append(fromPath, string(from.Filename))...)
	toLoc := fs.Join(append(toPath, string(to.Filename))...)

	err = fs.Rename(fromLoc, toLoc)
	if err != nil {
		return statusError(err, NFSStatusIO)
	}

	if err := userHandle.InvalidateHandle(fs, oldHandle); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	if err := WriteWcc(writer, preCacheData, tryStat(fs, fromPath)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := WriteWcc(writer, preDestData, tryStat(fs, toPath)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
