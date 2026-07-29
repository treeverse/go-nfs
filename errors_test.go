package nfs

import (
	"encoding/binary"
	"errors"
	"testing"
)

// A handler or filesystem error that go-nfs does not recognize must be reported
// to the client as an NFS-level status inside a normal accepted reply, never as
// an RPC-level accept_stat fault.  An RPC fault is read by some clients (notably
// macOS, as EBADRPC) as a hard protocol error, turning a transient server-side
// failure into a failed syscall.
func TestUnknownErrorMapsToNFSStatusNotRPCFault(t *testing.T) {
	unknown := errors.New("save chunks: predicate failed")

	for _, tc := range []struct {
		name      string
		formatter func(error) RPCError
	}{
		{"basic", basicErrorFormatter},
		{"wccData", wccDataErrorFormatter},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rpcErr := tc.formatter(unknown)

			if rpcErr.Code() != ResponseCodeSuccess {
				t.Fatalf("unknown error must be an accepted NFS-level reply (ResponseCodeSuccess), got RPC code %d", rpcErr.Code())
			}
			body, err := rpcErr.MarshalBinary()
			if err != nil {
				t.Fatalf("MarshalBinary: %v", err)
			}
			if len(body) < 4 {
				t.Fatalf("expected an NFS status in the reply body, got %d bytes", len(body))
			}
			if status := NFSStatus(binary.BigEndian.Uint32(body[:4])); status != NFSStatusServerFault {
				t.Fatalf("expected NFS status SERVERFAULT (%d), got %d", NFSStatusServerFault, status)
			}
		})
	}
}
