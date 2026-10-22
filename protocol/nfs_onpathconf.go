package protocol

import (
	"bytes"
	"context"

	"github.com/treeverse/go-nfs/fsapi"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

// pathConfResponse is the PATHCONF3resok payload after the attributes.
type pathConfResponse struct {
	LinkMax         uint32
	NameMax         uint32
	NoTrunc         uint32
	ChownRestricted uint32
	CaseInsensitive uint32
	CasePreserving  uint32
}

// pathConf is what the server reports for every object: one hard link, names
// no longer than it will itself carry, and an over-long name refused rather
// than quietly shortened.
//
// These are the server's own limits, not the filesystem's, which is why a
// filesystem is not asked.  NameMax in particular cannot come from below:
// checkName rejects anything longer whatever the filesystem would accept, so
// advertising more would be a promise the server breaks.
var pathConf = pathConfResponse{
	LinkMax:         1,
	NameMax:         PathNameMax,
	NoTrunc:         1,
	ChownRestricted: 0,
	CaseInsensitive: 0,
	CasePreserving:  1,
}

func onPathConf(ctx context.Context, w *response) error {
	w.errorFmt = opAttrErrorFormatter
	fs, h, _, err := fhArg(w)
	if err != nil {
		return err
	}

	var attrs *fsapi.Attrs
	if a, err := fs.GetAttr(ctx, h); err == nil {
		attrs = &a
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := writePostOpAttrs(writer, attrs); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := xdr.Write(writer, pathConf); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
