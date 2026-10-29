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

// maxListEntries is how many of a filesystem's own entries one listing reply
// may report.  A listing is the one request that mints handles in bulk, so a
// filesystem that cannot keep them all gets half its limit spent here; the rest
// stays for the handles the client already holds - the directory being listed,
// its parent, the root it walked from - which it would otherwise lose part way
// through reading the reply.
//
// Zero means no limit, which is what a filesystem keeping its handles for the
// life of the mount reports by not implementing fsapi.HandleKeeper.
func maxListEntries(fs fsapi.FS) int {
	keeper, ok := fs.(fsapi.HandleKeeper)
	if !ok {
		return 0
	}
	if half := keeper.HandleLimit() / 2; half > 0 {
		return half
	}
	// A limit this small halves to nothing, which would read as no limit at
	// all.  A listing still has to make progress, so it reports one entry at
	// a time instead.
	return 1
}

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
// ".." entries, and the attributes READDIRPLUS sends with it.
//
// dirAttrs are the listed directory's own attributes, which the caller has
// already read.  "." is that directory, so asking the filesystem again would be
// a second call for an answer already in hand.
//
// A fileid of zero is what is left when nothing reports one, and RFC 1813
// 3.3.16 asks a server to avoid sending it: UNIX clients give it a meaning of
// its own.  So ".." asks for the parent's attributes rather than settle for it.
func dirPrefixEntry(ctx context.Context, fs fsapi.FS, dir fsapi.Handle, dirAttrs *fsapi.Attrs, name string) (fileID uint64, cookie uint64, attrs *fsapi.Attrs) {
	switch name {
	case ".":
		if dirAttrs == nil {
			return 0, dotCookie, nil
		}
		return dirAttrs.Fileid, dotCookie, dirAttrs
	default:
		parent, a, err := fs.LookupParent(ctx, dir)
		if err != nil {
			return 0, dotDotCookie, nil
		}
		if a == nil {
			// LookupParent reports the parent's attributes as a
			// post_op_attr, so a filesystem may report none and
			// still be answering correctly.  The entry needs a
			// fileid either way.
			if got, err := fs.GetAttr(ctx, parent); err == nil {
				a = &got
			}
		}
		if a == nil {
			return 0, dotDotCookie, nil
		}
		return a.Fileid, dotDotCookie, a
	}
}
