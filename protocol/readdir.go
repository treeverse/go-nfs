package protocol

import (
	"context"

	"github.com/treeverse/go-nfs/fsapi"
)

// Cookies 1 and 2 mark the "." and ".." entries the server adds to the front of
// every listing.  A filesystem's own cookies start at fsapi.FirstCookie, so
// they never collide with these or with the zero cookie that starts a listing.
const (
	dotCookie    = 1
	dotDotCookie = 2
)

// dirPrefix is the "." and ".." entries a listing resuming at cookie still owes
// the client, and the cookie to resume the filesystem's own listing from.
//
// A client resumes with the cookie of the last entry it saw, so a listing cut
// short after "." comes back asking for cookie 1, and one cut short after ".."
// asks for 2.  Either way the filesystem's entries start from its beginning,
// because none of them has been sent yet.
func dirPrefix(ctx context.Context, fs fsapi.FS, dir fsapi.Handle, cookie uint64) (names []string, from fsapi.Cookie) {
	switch cookie {
	case 0:
		return []string{".", ".."}, 0
	case dotCookie:
		return []string{".."}, 0
	case dotDotCookie:
		return nil, 0
	default:
		return nil, fsapi.Cookie(cookie)
	}
}

// dirPrefixEntry returns the fileid and cookie to report for one of the "." and
// ".." entries.  A fileid the filesystem will not report is sent as 0, which
// clients tolerate: it is only a hint for detecting hard links.
func dirPrefixEntry(ctx context.Context, fs fsapi.FS, dir fsapi.Handle, name string) (fileID uint64, cookie uint64, attrs *fsapi.Attrs) {
	switch name {
	case ".":
		if a, err := fs.GetAttr(ctx, dir); err == nil {
			return a.Fileid, dotCookie, &a
		}
		return 0, dotCookie, nil
	default:
		if _, a, err := fs.LookupParent(ctx, dir); err == nil && a != nil {
			return a.Fileid, dotDotCookie, a
		}
		return 0, dotDotCookie, nil
	}
}
