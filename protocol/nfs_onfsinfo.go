package protocol

import (
	"bytes"
	"context"

	"github.com/treeverse/go-nfs/fsapi"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

// fsInfoResponse is the FSINFO3resok payload after the attributes.
type fsInfoResponse struct {
	Rtmax       uint32
	Rtpref      uint32
	Rtmult      uint32
	Wtmax       uint32
	Wtpref      uint32
	Wtmult      uint32
	Dtpref      uint32
	Maxfilesize uint64
	TimeDelta   FileTime
	Properties  uint32
}

func onFSInfo(ctx context.Context, w *response) error {
	w.errorFmt = opAttrErrorFormatter
	fs, h, _, err := fhArg(w)
	if err != nil {
		return err
	}

	info, attrs, err := fs.FSInfo(ctx, h)
	if err != nil {
		return statusError(err, NFSStatusServerFault)
	}

	// Whether there are links is not the filesystem's to assert: it follows
	// from which optional interfaces it implements, so the server fills
	// those two bits in and ignores what it was told.
	properties := info.Properties &^ (fsapi.FSPropertyLink | fsapi.FSPropertySymlink)
	if _, ok := fs.(fsapi.Linker); ok {
		properties |= fsapi.FSPropertyLink
	}
	if _, ok := fs.(fsapi.Symlinker); ok {
		properties |= fsapi.FSPropertySymlink
	}

	resp := fsInfoResponse{
		Rtmax:       clampRead(info.ReadMax),
		Rtpref:      clampRead(info.ReadPref),
		Rtmult:      info.ReadMultiple,
		Wtmax:       info.WriteMax,
		Wtpref:      info.WritePref,
		Wtmult:      info.WriteMultiple,
		Dtpref:      info.DirPref,
		Maxfilesize: info.MaxFileSize,
		TimeDelta:   durationToNFSTime(info.TimeDelta),
		Properties:  uint32(properties),
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

// clampRead keeps an advertised read size within what this server will actually
// serve, so that a client sizing its requests by FSINFO is never told it may
// ask for more than onRead will return.  Zero means the filesystem expressed no
// preference, and gets the server's own limit.
func clampRead(size uint32) uint32 {
	if size == 0 || size > MaxRead {
		return MaxRead
	}
	return size
}
