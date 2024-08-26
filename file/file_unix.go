//go:build dragonfly || freebsd || linux || nacl || netbsd || openbsd || solaris

package file

import (
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func getOSFileInfo(info os.FileInfo) *FileInfo {
	fi := &FileInfo{}
	if s, ok := info.Sys().(*syscall.Stat_t); ok {
		fi.Nlink = uint32(s.Nlink)
		fi.UID = s.Uid
		fi.GID = s.Gid
		fi.Major = unix.Major(uint64(s.Rdev))
		fi.Minor = unix.Minor(uint64(s.Rdev))
		fi.Fileid = s.Ino
		fi.Atime = time.Unix(s.Atim.Sec, s.Atim.Nsec)
		fi.Ctime = time.Unix(s.Ctim.Sec, s.Ctim.Nsec)
		return fi
	}
	return nil
}
