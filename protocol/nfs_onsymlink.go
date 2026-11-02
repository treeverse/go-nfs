package protocol

import (
	"context"

	"github.com/treeverse/go-nfs/fsapi"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

func onSymlink(ctx context.Context, w *response) error {
	w.errorFmt = wccDataErrorFormatter
	fs, dir, id, name, err := dirOpArgs(w)
	if err != nil {
		return err
	}
	if err := checkNewName(name); err != nil {
		return err
	}

	// symlinkdata3 is the attributes for the link, then its target.
	change, err := readAttrChange(w.req.Body)
	if err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}
	target, err := xdr.ReadOpaque(w.req.Body)
	if err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}

	links, ok := fs.(fsapi.Symlinker)
	if !ok {
		return &NFSStatusError{NFSStatusNotSupp, fsapi.ErrNotSupported}
	}
	h, attrs, wcc, err := links.Symlink(ctx, dir, name, string(target), change)
	if err != nil {
		return statusError(err, NFSStatusAccess)
	}
	handle, err := wireHandle(id, h)
	if err != nil {
		return err
	}
	return writeNewObject(w, handle, attrs, wcc)
}
