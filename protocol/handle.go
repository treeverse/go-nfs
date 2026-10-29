package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/treeverse/go-nfs/fsapi"
)

// exportID identifies one exported filesystem within a server.  A wire handle
// carries it ahead of the handle the filesystem issued, so that a request
// arriving on any connection says which filesystem it is about: MOUNT and NFS
// are separate RPC programs and a client may use a different connection for
// each, so the server cannot learn this from the connection.
type exportID uint16

// exportIDSize is the number of bytes an exportID takes on the wire.
const exportIDSize = 2

// MaxWireHandleSize is the longest handle this server sends.  NFSv3 allows 64
// bytes, which leaves room for the export id ahead of the filesystem's own
// handle.
const MaxWireHandleSize = exportIDSize + fsapi.MaxHandleSize

// ErrBadHandle is a handle that did not come from this server.
var ErrBadHandle = errors.New("malformed file handle")

// exports maps each exported filesystem to its id and back.  A server holds
// one for its lifetime.
type exports struct {
	mu   sync.RWMutex
	byID map[exportID]fsapi.FS
	// byFS reuses the id of a filesystem that is mounted more than once.  It
	// holds only filesystems Go can compare; see idFor.
	byFS map[fsapi.FS]exportID
	next exportID
}

// idFor returns the id of fs, assigning one if it has none.
//
// A filesystem whose type Go cannot compare - a struct holding a slice, say -
// cannot be looked up in byFS, and so is given a fresh id on every mount.  That
// wastes ids rather than misdirecting a handle, and the waste is bounded by the
// number of mounts.
func (e *exports) idFor(fs fsapi.FS) (exportID, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	comparable := reflect.TypeOf(fs).Comparable()
	if comparable {
		if id, ok := e.byFS[fs]; ok {
			return id, nil
		}
	}
	if e.byID == nil {
		e.byID = make(map[exportID]fsapi.FS)
		e.byFS = make(map[fsapi.FS]exportID)
	}
	id := e.next
	if int(id)+1 > int(^exportID(0)) {
		return 0, fmt.Errorf("%d exports: %w", int(id)+1, errors.ErrUnsupported)
	}
	e.next++
	e.byID[id] = fs
	if comparable {
		e.byFS[fs] = id
	}
	return id, nil
}

// get returns the filesystem with this id.
func (e *exports) get(id exportID) (fsapi.FS, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	fs, ok := e.byID[id]
	return fs, ok
}

// encodeHandle returns the wire form of h within export id.
func encodeHandle(id exportID, h fsapi.Handle) ([]byte, error) {
	if len(h) > fsapi.MaxHandleSize {
		return nil, fmt.Errorf("%w: %d bytes exceeds the %d a filesystem may return",
			ErrBadHandle, len(h), fsapi.MaxHandleSize)
	}
	wire := make([]byte, exportIDSize, exportIDSize+len(h))
	binary.BigEndian.PutUint16(wire, uint16(id))
	return append(wire, h...), nil
}

// decodeHandle splits a wire handle back into its export and the handle the
// filesystem issued.
func decodeHandle(wire []byte) (exportID, fsapi.Handle, error) {
	if len(wire) < exportIDSize {
		return 0, "", fmt.Errorf("%w: %d bytes is too short to hold an export id",
			ErrBadHandle, len(wire))
	}
	if len(wire) > MaxWireHandleSize {
		return 0, "", fmt.Errorf("%w: %d bytes exceeds the %d this server issues",
			ErrBadHandle, len(wire), MaxWireHandleSize)
	}
	return exportID(binary.BigEndian.Uint16(wire)), fsapi.Handle(wire[exportIDSize:]), nil
}

// fsFor resolves a wire handle to the filesystem that issued it, the handle
// that filesystem knows it by, and the export it belongs to - which later
// handles from the same filesystem are wrapped with.  Every failure here is
// NFSStatusStale: a handle the server cannot place is one the client must stop
// using.
func (s *Server) fsFor(wire []byte) (fsapi.FS, fsapi.Handle, exportID, error) {
	id, h, err := decodeHandle(wire)
	if err != nil {
		return nil, "", 0, &NFSStatusError{NFSStatusStale, err}
	}
	fs, ok := s.exports.get(id)
	if !ok {
		return nil, "", 0, &NFSStatusError{NFSStatusStale,
			fmt.Errorf("%w: no export %d", ErrBadHandle, id)}
	}
	return fs, h, id, nil
}

// wireHandle returns the wire form of a handle a filesystem issued, for a
// request that arrived naming export id.  A handle too long to encode is the
// filesystem breaking its side of the contract, so it is a server fault and
// not a bad request.
func wireHandle(id exportID, h fsapi.Handle) ([]byte, error) {
	wire, err := encodeHandle(id, h)
	if err != nil {
		return nil, &NFSStatusError{NFSStatusServerFault, err}
	}
	return wire, nil
}
