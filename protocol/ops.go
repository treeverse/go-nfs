package protocol

import (
	"bytes"
	"strings"

	"github.com/treeverse/go-nfs/fsapi"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

// PathNameMax is the longest name this server accepts for one path component.
const PathNameMax = 255

// fhArg reads a file handle argument and resolves it.
func fhArg(w *response) (fsapi.FS, fsapi.Handle, exportID, error) {
	wire, err := xdr.ReadOpaque(w.req.Body)
	if err != nil {
		return nil, "", 0, &NFSStatusError{NFSStatusInval, err}
	}
	return w.Server.fsFor(wire)
}

// dirOpArgs reads a diropargs3 - a directory handle and a name within it - and
// resolves the handle.  It checks the name, so a caller that reads a
// diropargs3 itself must call checkName.
func dirOpArgs(w *response) (fs fsapi.FS, dir fsapi.Handle, id exportID, name string, err error) {
	var arg DirOpArg
	if err := xdr.Read(w.req.Body, &arg); err != nil {
		return nil, "", 0, "", &NFSStatusError{NFSStatusInval, err}
	}
	fs, dir, id, err = w.Server.fsFor(arg.Handle)
	if err != nil {
		return nil, "", 0, "", err
	}
	name = string(arg.Filename)
	if err := checkName(name); err != nil {
		return nil, "", 0, "", err
	}
	return fs, dir, id, name, nil
}

// checkName rejects a name this server will not pass to a filesystem.  A name
// is one component of a path, and fsapi.FS promises its implementations as
// much, so a separator or an embedded NUL is refused here: a filesystem that
// believed the promise would otherwise be handed a path to walk, reaching
// objects the client never looked up.  Only the server can keep that promise,
// because only the server sees the name before it becomes a path.
func checkName(name string) error {
	if len(name) > PathNameMax {
		return &NFSStatusError{NFSStatusNameTooLong, fsapi.ErrNameTooLong}
	}
	if name == "" {
		return &NFSStatusError{NFSStatusInval, fsapi.ErrInval}
	}
	if strings.ContainsAny(name, "/\x00") {
		return &NFSStatusError{NFSStatusInval, fsapi.ErrInval}
	}
	return nil
}

// checkNewName is checkName for a name being created or removed, which may not
// be "." or "..": a client asking to create ".." is asking for something that
// cannot be created, and NFS gives EEXIST for it rather than inventing an
// entry.
func checkNewName(name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	if name == "." || name == ".." {
		return &NFSStatusError{NFSStatusExist, fsapi.ErrExists}
	}
	return nil
}

// writeNewObject writes the reply shared by every procedure that creates
// something: the new object's handle and attributes, then what the change did
// to the directory holding it.
func writeNewObject(w *response, handle []byte, attrs *fsapi.Attrs, wcc fsapi.Wcc) error {
	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	// "handle follows": this server always has one to report.
	if err := xdr.Write(writer, uint32(1)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := xdr.Write(writer, handle); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := writePostOpAttrs(writer, attrs); err != nil {
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

// writeWccOnly writes the reply shared by every procedure whose only result is
// what it did to a directory.
func writeWccOnly(w *response, wcc fsapi.Wcc) error {
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
