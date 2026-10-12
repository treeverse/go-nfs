package nfs

import "context"

// openDirIterator opens a streaming iterator over the directory at path, when
// userHandle supports one.  ok is false if it does not, leaving the caller to
// read a full listing instead.  Any error is already an NFS status error.
//
// cookie is an NFS wire cookie: 0 and 1 are the "." and ".." sentinels, and
// both start a fresh listing.
func openDirIterator(ctx context.Context, userHandle Handler, path string, cookie, verifier uint64) (it DirIterator, ok bool, err error) {
	h, ok := userHandle.(DirIteratorHandler)
	if !ok {
		return nil, false, nil
	}
	var serial uint64
	if cookie >= 2 {
		serial = cookie - 2
	}
	it, err = h.OpenDir(ctx, path, serial, verifier)
	if err != nil {
		return nil, true, statusError(err, NFSStatusServerFault)
	}
	return it, true, nil
}
