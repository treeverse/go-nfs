package nfs

import (
	"bytes"
	"context"
	"os"

	"github.com/go-git/go-billy/v6"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

const (
	createModeUnchecked = 0
	createModeGuarded   = 1
	createModeExclusive = 2
)

func onCreate(ctx context.Context, w *response, userHandle Handler) error {
	w.errorFmt = wccDataErrorFormatter
	obj := DirOpArg{}
	err := xdr.Read(w.req.Body, &obj)
	if err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}
	how, err := xdr.ReadUint32(w.req.Body)
	if err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}
	var attrs *SetFileAttributes
	if how == createModeUnchecked || how == createModeGuarded {
		sattr, err := ReadSetFileAttributes(w.req.Body)
		if err != nil {
			return &NFSStatusError{NFSStatusInval, err}
		}
		attrs = sattr
	} else if how == createModeExclusive {
		// read createverf3
		var verf [8]byte
		if err := xdr.Read(w.req.Body, &verf); err != nil {
			return &NFSStatusError{NFSStatusInval, err}
		}
		Log.Errorf("failing create to indicate lack of support for 'exclusive' mode.")
		// TODO: support 'exclusive' mode.
		return &NFSStatusError{NFSStatusNotSupp, os.ErrPermission}
	} else {
		// invalid
		return &NFSStatusError{NFSStatusNotSupp, os.ErrInvalid}
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

	newFile := append(path, string(obj.Filename))
	newFilePath := fs.Join(newFile...)
	existed := false
	if s, err := fs.Stat(newFilePath); err == nil {
		if s.IsDir() {
			return &NFSStatusError{NFSStatusExist, nil}
		}
		if how == createModeGuarded {
			return &NFSStatusError{NFSStatusExist, os.ErrPermission}
		}
		existed = true
	} else {
		if s, err := fs.Stat(fs.Join(path...)); err != nil {
			return statusError(err, NFSStatusAccess)
		} else if !s.IsDir() {
			return &NFSStatusError{NFSStatusNotDir, nil}
		}
	}

	file, err := fs.Create(newFilePath)
	if err != nil {
		Log.Errorf("Error Creating: %v", err)
		return statusError(err, NFSStatusAccess)
	}
	if err := file.Close(); err != nil {
		Log.Errorf("Error Creating: %v", err)
		return statusError(err, NFSStatusAccess)
	}

	fp := userHandle.ToHandle(fs, newFile)
	changer := userHandle.Change(fs)
	if existed {
		// A file that already exists keeps its mode, ownership and times: only the size
		// carries over.  RFC 1813 3.3.8 does not say what UNCHECKED does to a file that
		// is already there; RFC 7530 16.16 specified it for NFSv4 - "the attributes
		// specified by createattrs are not used, except that when a size of zero is
		// specified, the existing file is truncated" - and Linux's nfsd does the same for
		// NFSv3, masking the request's attributes down to the size in nfsd3_create_file():
		// https://github.com/torvalds/linux/blob/v6.12/fs/nfsd/nfs3proc.c#L317-L325
		//
		// The size is carried over rather than forced to zero because the client sends a
		// mode on every create, but a size only under O_TRUNC and then only zero:
		// https://github.com/torvalds/linux/blob/v6.12/fs/nfs/dir.c#L2380-L2385
		// An open(path, O_WRONLY|O_CREAT|O_APPEND) of a file that exists therefore
		// arrives carrying a mode that must not be applied and no size at all.  SetSize
		// is nil there, and copying the field keeps it nil: an append must not be
		// truncated to zero, so "did not ask" has to stay distinct from "asked for zero"
		// rather than collapsing into a literal 0.
		attrs = &SetFileAttributes{SetSize: attrs.SetSize}
	}
	if err := attrs.Apply(changer, fs, newFilePath); err != nil {
		Log.Errorf("Error applying attributes: %v\n", err)
		return statusError(err, NFSStatusIO)
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
	if err := WritePostOpAttrs(writer, tryStat(fs, []string{file.Name()})); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	// dir_wcc (we don't include pre_op_attr)
	if err := xdr.Write(writer, uint32(0)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := WritePostOpAttrs(writer, tryStat(fs, path)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
