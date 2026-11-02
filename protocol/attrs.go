package protocol

import (
	"io"
	"time"

	"github.com/treeverse/go-nfs/fsapi"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

// permBits are the bits of a mode word that fattr3 and sattr3 carry: the nine
// permission bits plus setuid, setgid and sticky.  An object's type is a field
// of its own in fattr3, so it never travels here.
const permBits = 0o7777

// fileAttribute is fattr3 on the wire.
type fileAttribute struct {
	Type                uint32
	FileMode            uint32
	Nlink               uint32
	UID                 uint32
	GID                 uint32
	Filesize            uint64
	Used                uint64
	SpecData            [2]uint32
	FSID                uint64
	Fileid              uint64
	Atime, Mtime, Ctime FileTime
}

// fileCacheAttribute is wcc_attr on the wire: the part of an object's
// attributes a client compares to decide whether its cache is still good.
type fileCacheAttribute struct {
	Filesize     uint64
	Mtime, Ctime FileTime
}

// toWire returns the fattr3 form of a.
func toWire(a fsapi.Attrs) fileAttribute {
	return fileAttribute{
		Type:     uint32(a.Type),
		FileMode: a.Perm & permBits,
		Nlink:    a.Nlink,
		UID:      a.UID,
		GID:      a.GID,
		Filesize: a.Size,
		Used:     a.Used,
		SpecData: a.SpecData,
		FSID:     a.FSID,
		Fileid:   a.Fileid,
		Atime:    ToNFSTime(a.Atime),
		Mtime:    ToNFSTime(a.Mtime),
		Ctime:    ToNFSTime(a.Ctime),
	}
}

// cacheToWire returns the wcc_attr form of c.
func cacheToWire(c fsapi.CacheAttrs) fileCacheAttribute {
	return fileCacheAttribute{
		Filesize: c.Size,
		Mtime:    ToNFSTime(c.Mtime),
		Ctime:    ToNFSTime(c.Ctime),
	}
}

// writePostOpAttrs writes the post_op_attr form of a, which is allowed to say
// that there are no attributes to report.
func writePostOpAttrs(w io.Writer, a *fsapi.Attrs) error {
	if a == nil {
		return xdr.Write(w, uint32(0))
	}
	if err := xdr.Write(w, uint32(1)); err != nil {
		return err
	}
	return xdr.Write(w, toWire(*a))
}

// writeWcc writes the wcc_data form of wcc.  Either half may be missing, which
// costs the client a re-read but is always permitted.
func writeWcc(w io.Writer, wcc fsapi.Wcc) error {
	if wcc.Pre == nil {
		if err := xdr.Write(w, uint32(0)); err != nil {
			return err
		}
	} else {
		if err := xdr.Write(w, uint32(1)); err != nil {
			return err
		}
		if err := xdr.Write(w, cacheToWire(*wcc.Pre)); err != nil {
			return err
		}
	}
	return writePostOpAttrs(w, wcc.Post)
}

// readAttrChange reads an sattr3 into the change it asks for.
func readAttrChange(r io.Reader) (fsapi.AttrChange, error) {
	var change fsapi.AttrChange

	hasMode, err := xdr.ReadUint32(r)
	if err != nil {
		return change, err
	}
	if hasMode != 0 {
		mode, err := xdr.ReadUint32(r)
		if err != nil {
			return change, err
		}
		mode &= permBits
		change.Perm = &mode
	}

	hasUID, err := xdr.ReadUint32(r)
	if err != nil {
		return change, err
	}
	if hasUID != 0 {
		uid, err := xdr.ReadUint32(r)
		if err != nil {
			return change, err
		}
		change.UID = &uid
	}

	hasGID, err := xdr.ReadUint32(r)
	if err != nil {
		return change, err
	}
	if hasGID != 0 {
		gid, err := xdr.ReadUint32(r)
		if err != nil {
			return change, err
		}
		change.GID = &gid
	}

	hasSize, err := xdr.ReadUint32(r)
	if err != nil {
		return change, err
	}
	if hasSize != 0 {
		var size uint64
		if err := xdr.Read(r, &size); err != nil {
			return change, err
		}
		change.Size = &size
	}

	if change.Atime, err = readSetTime(r); err != nil {
		return change, err
	}
	if change.Mtime, err = readSetTime(r); err != nil {
		return change, err
	}
	return change, nil
}

// readSetTime reads one set_atime or set_mtime: either "leave it alone", "set
// it to now", or an explicit time.
func readSetTime(r io.Reader) (*time.Time, error) {
	const (
		dontChange = 0
		setToNow   = 1
		setToGiven = 2
	)
	how, err := xdr.ReadUint32(r)
	if err != nil {
		return nil, err
	}
	switch how {
	case setToNow:
		now := time.Now()
		return &now, nil
	case setToGiven:
		var t FileTime
		if err := xdr.Read(r, &t); err != nil {
			return nil, err
		}
		return t.Native(), nil
	default:
		return nil, nil
	}
}
