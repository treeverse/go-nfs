package protocol

import (
	"bytes"
	"context"
	"math"
	"time"

	"github.com/willscott/go-nfs-client/nfs/xdr"
)

// fsStatResponse is the FSSTAT3resok payload after the attributes.
//
// invarsec is a uint32 of seconds.  The billy-based server wrote the whole
// struct in one go with the field held as a time.Duration, which XDR encodes as
// eight bytes of nanoseconds, so the last field of every FSSTAT reply was four
// bytes too long and wrong in value.
type fsStatResponse struct {
	TotalSize      uint64
	FreeSize       uint64
	AvailableSize  uint64
	TotalFiles     uint64
	FreeFiles      uint64
	AvailableFiles uint64
	CacheHintSec   uint32
}

func onFSStat(ctx context.Context, w *response) error {
	w.errorFmt = opAttrErrorFormatter
	fs, h, _, err := fhArg(w)
	if err != nil {
		return err
	}

	stat, attrs, err := fs.FSStat(ctx, h)
	if err != nil {
		return statusError(err, NFSStatusServerFault)
	}

	hint := stat.CacheHint / time.Second
	if hint < 0 {
		hint = 0
	} else if hint > math.MaxUint32 {
		hint = math.MaxUint32
	}
	resp := fsStatResponse{
		TotalSize:      stat.TotalSize,
		FreeSize:       stat.FreeSize,
		AvailableSize:  stat.AvailableSize,
		TotalFiles:     stat.TotalFiles,
		FreeFiles:      stat.FreeFiles,
		AvailableFiles: stat.AvailableFiles,
		CacheHintSec:   uint32(hint),
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := writePostOpAttrs(writer, attrs); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := xdr.Write(writer, resp); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
