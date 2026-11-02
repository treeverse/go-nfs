package protocol

import (
	"bytes"
	"context"
	"time"

	"github.com/willscott/go-nfs-client/nfs/xdr"
)

func onSetAttr(ctx context.Context, w *response) error {
	w.errorFmt = wccDataErrorFormatter
	fs, h, _, err := fhArg(w)
	if err != nil {
		return err
	}

	change, err := readAttrChange(w.req.Body)
	if err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}

	guard, err := readGuard(w)
	if err != nil {
		return err
	}

	wcc, err := fs.SetAttr(ctx, h, change, guard)
	if err != nil {
		return statusError(err, NFSStatusIO)
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := writeWcc(writer, wcc); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}

// readGuard reads a sattr_guard3: the ctime a client believes the object has,
// so that it can refuse to overwrite a change it has not seen.  nil means the
// client did not ask for one.
func readGuard(w *response) (*time.Time, error) {
	check, err := xdr.ReadUint32(w.req.Body)
	if err != nil {
		return nil, &NFSStatusError{NFSStatusInval, err}
	}
	if check == 0 {
		return nil, nil
	}
	var t FileTime
	if err := xdr.Read(w.req.Body, &t); err != nil {
		return nil, &NFSStatusError{NFSStatusInval, err}
	}
	return t.Native(), nil
}
