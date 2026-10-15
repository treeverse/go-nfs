package nfs

import (
	"bytes"
	"context"
	"os"

	"github.com/willscott/go-nfs-client/nfs/xdr"
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
