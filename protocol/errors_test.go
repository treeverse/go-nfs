package protocol

import (
	"encoding/binary"
	"errors"
	"io/fs"
	"testing"
)

func TestStatusErrorKeepsFallback(t *testing.T) {
	err := errors.New("backend exploded")
	statusErr := statusError(err, NFSStatusAccess)
	if statusErr.NFSStatus != NFSStatusAccess {
		t.Errorf("status = %v, want %v", statusErr.NFSStatus, NFSStatusAccess)
	}
	if !errors.Is(statusErr, err) {
		t.Errorf("statusError dropped the wrapped error: %v", statusErr)
	}
}

// A handler or filesystem error that is not already an RPCError must be
// reported to the client as an NFS-level status inside a normal accepted reply,
// never as an RPC-level accept_stat fault.  An RPC fault is read by some clients
// (notably macOS, as EBADRPC) as a hard protocol error, turning a transient
// server-side failure into a failed syscall.
func TestUnknownErrorMapsToNFSStatusNotRPCFault(t *testing.T) {
	for _, f := range []struct {
		name      string
		formatter func(error) RPCError
	}{
		{"basic", basicErrorFormatter},
		{"wccData", wccDataErrorFormatter},
	} {
		for _, e := range []struct {
			name string
			err  error
			want NFSStatus
		}{
			{"unknown", errors.New("save chunks: predicate failed"), NFSStatusServerFault},
			{"notExist", fs.ErrNotExist, NFSStatusNoEnt},
		} {
			t.Run(f.name+"/"+e.name, func(t *testing.T) {
				rpcErr := f.formatter(e.err)

				if rpcErr.Code() != ResponseCodeSuccess {
					t.Fatalf("error must be an accepted NFS-level reply (ResponseCodeSuccess), got RPC code %d", rpcErr.Code())
				}
				body, err := rpcErr.MarshalBinary()
				if err != nil {
					t.Fatalf("MarshalBinary: %v", err)
				}
				if len(body) < 4 {
					t.Fatalf("expected an NFS status in the reply body, got %d bytes", len(body))
				}
				if status := NFSStatus(binary.BigEndian.Uint32(body[:4])); status != e.want {
					t.Fatalf("expected NFS status %d, got %d", e.want, status)
				}
			})
		}
	}
}
