package file

import "os"

type FileInfo struct {
	Nlink  uint32
	UID    uint32
	GID    uint32
	Major  uint32
	Minor  uint32
	Fileid uint64
	// FSID identifies the file system holding the file.  A Filesystem with no
	// operating-system backing reports one by returning a FileInfo from
	// os.FileInfo.Sys(); leaving it zero makes every file appear to share a
	// single file system.
	FSID uint64
}

// GetInfo extracts some non-standardized items from the result of a Stat call.
func GetInfo(fi os.FileInfo) *FileInfo {
	sys := fi.Sys()
	switch v := sys.(type) {
	case FileInfo:
		return &v
	case *FileInfo:
		return v
	default:
		return getOSFileInfo(fi)
	}
}
