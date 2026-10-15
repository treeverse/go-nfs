package nfs

import (
	"bytes"
	"context"
	"errors"
	"os"

	"github.com/go-git/go-billy/v6"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

const (
	mkdirDefaultMode = 755
)

func onMkdir(ctx context.Context, w *response, userHandle Handler) error {
	w.errorFmt = wccDataErrorFormatter
	obj := DirOpArg{}
	err := xdr.Read(w.req.Body, &obj)
	if err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}

	attrs, err := ReadSetFileAttributes(w.req.Body)
	if err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}

	if len(string(obj.Filename)) > PathNameMax {
		return &NFSStatusError{NFSStatusNameTooLong, os.ErrInvalid}
	}
	if string(obj.Filename) == "." || string(obj.Filename) == ".." {
		return &NFSStatusError{NFSStatusExist, os.ErrExist}
	}

	fp, attr, dirAttr, err := userHandle.Mkdir(ctx, obj.Handle, string(obj.Filename), attrs)
	if err != nil {
		var statusErr *NFSStatusError
		if errors.As(err, &statusErr) {
			return statusErr
		}
		return statusError(err, NFSStatusAccess)
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	// "handle follows"
	if err := xdr.Write(writer, uint32(1)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := xdr.Write(writer, fp); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := WritePostOpAttrs(writer, attr); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	if err := WriteWcc(writer, nil, dirAttr); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}

// BillyMkdir implements Handler.Mkdir for a handler that serves a billy.Filesystem,
// resolving handles through h.
func BillyMkdir(h Handler, dir []byte, name string, attrs *SetFileAttributes) ([]byte, *FileAttribute, *FileAttribute, error) {
	fs, path, err := h.FromHandle(dir)
	if err != nil {
		return nil, nil, nil, &NFSStatusError{NFSStatusStale, err}
	}
	if !billy.CapabilityCheck(fs, billy.WriteCapability) {
		return nil, nil, nil, &NFSStatusError{NFSStatusROFS, os.ErrPermission}
	}

	newFolder := append(path, name)
	newFolderPath := fs.Join(newFolder...)
	if s, err := fs.Stat(newFolderPath); err == nil {
		if s.IsDir() {
			return nil, nil, nil, &NFSStatusError{NFSStatusExist, nil}
		}
	} else {
		if s, err := fs.Stat(fs.Join(path...)); err != nil {
			return nil, nil, nil, statusError(err, NFSStatusAccess)
		} else if !s.IsDir() {
			return nil, nil, nil, &NFSStatusError{NFSStatusNotDir, nil}
		}
	}

	if err := fs.MkdirAll(newFolderPath, attrs.Mode(mkdirDefaultMode)); err != nil {
		return nil, nil, nil, statusError(err, NFSStatusAccess)
	}

	fp := h.ToHandle(fs, newFolder)
	changer := h.Change(fs)
	if changer != nil {
		if err := attrs.Apply(changer, fs, newFolderPath); err != nil {
			return nil, nil, nil, statusError(err, NFSStatusIO)
		}
	}

	return fp, tryStat(fs, newFolder), tryStat(fs, path), nil
}
