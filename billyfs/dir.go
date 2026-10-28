package billyfs

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io/fs"
	"sort"

	billy "github.com/go-git/go-billy/v6"
	"github.com/treeverse/go-nfs/fsapi"
)

// serialFor turns an fsapi cookie into the serial a DirIteratorHandler resumes
// from, which by that interface's contract names the last entry already sent.
// Anything below the first cookie a filesystem may issue means the start of the
// listing.
func serialFor(cookie fsapi.Cookie) uint64 {
	if cookie < fsapi.FirstCookie {
		return 0
	}
	return uint64(cookie - fsapi.FirstCookie)
}

// indexAfter is serialFor for a listing read whole, where the entries are
// indexed directly: it is the index of the first entry not yet sent, one past
// the entry the cookie names.
func indexAfter(cookie fsapi.Cookie) int {
	if cookie < fsapi.FirstCookie {
		return 0
	}
	return int(cookie-fsapi.FirstCookie) + 1
}

// cookieFor is serialFor backwards.
func cookieFor(serial uint64) fsapi.Cookie {
	return fsapi.Cookie(serial) + fsapi.FirstCookie
}

func (a *FS) ReadDir(ctx context.Context, dir fsapi.Handle, cookie fsapi.Cookie, verf fsapi.Verifier) (fsapi.DirIterator, error) {
	bfs, path, err := a.resolve(dir)
	if err != nil {
		return nil, err
	}
	attrs, err := statAttrs(bfs, path)
	if err != nil {
		return nil, err
	}
	if !attrs.IsDir() {
		return nil, fsapi.ErrNotDir
	}
	full := bfs.Join(path...)

	if h, ok := a.handler.(DirIteratorHandler); ok {
		it, err := h.OpenDir(ctx, full, serialFor(cookie), uint64(verf))
		if err != nil {
			return nil, translate(err)
		}
		return &streamIterator{fs: a, billy: bfs, dir: path, it: it}, nil
	}

	entries, verifier, err := a.listing(bfs, full, verf)
	if err != nil {
		return nil, err
	}
	// A cookie into this listing means nothing once the listing has changed,
	// and here it is an index, so resuming on a changed one silently skips or
	// repeats entries.  Say so instead: the client starts the listing again.
	// A cookie below FirstCookie has had none of these entries sent to it, and
	// a client that kept no verifier has nothing to check against.
	if cookie >= fsapi.FirstCookie && verf != 0 && verifier != verf {
		return nil, fsapi.ErrStaleCookie
	}
	return &listIterator{
		fs:       a,
		billy:    bfs,
		dir:      path,
		entries:  entries,
		next:     indexAfter(cookie),
		verifier: verifier,
	}, nil
}

// listing reads a whole directory and settles on a verifier for it, reusing the
// listing a caching handler kept for that verifier where it has one.
func (a *FS) listing(bfs billy.Filesystem, full string, verf fsapi.Verifier) ([]fs.FileInfo, fsapi.Verifier, error) {
	if cache, ok := a.handler.(CachingHandler); ok && verf != 0 {
		if entries := cache.DataForVerifier(full, uint64(verf)); entries != nil {
			return entries, verf, nil
		}
	}
	entries, err := bfs.ReadDir(full)
	if err != nil {
		return nil, 0, translate(err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	if cache, ok := a.handler.(CachingHandler); ok {
		return entries, fsapi.Verifier(cache.VerifierFor(full, entries)), nil
	}
	return entries, fsapi.Verifier(hashPathAndContents(full, entries)), nil
}

// hashPathAndContents is the verifier for a handler that keeps no listings: it
// changes whenever the set of names does, which is when old cookies stop
// meaning what they did.
func hashPathAndContents(path string, contents []fs.FileInfo) uint64 {
	hash := sha256.New()
	// The path is included so that two directories holding the same names
	// do not share a verifier.
	hash.Write([]byte(path))
	for _, c := range contents {
		hash.Write([]byte(c.Name()))
	}
	return binary.BigEndian.Uint64(hash.Sum(nil)[0:8])
}

// listIterator walks a listing that was read in one go.
type listIterator struct {
	fs       *FS
	billy    billy.Filesystem
	dir      []string
	entries  []fs.FileInfo
	next     int
	current  fsapi.DirEntry
	verifier fsapi.Verifier
}

func (l *listIterator) Next(ctx context.Context) bool {
	if l.next >= len(l.entries) {
		return false
	}
	info := l.entries[l.next]
	_, path := join(l.billy, l.dir, info.Name())
	attrs := toAttrs(l.billy, path, info)
	l.current = fsapi.DirEntry{
		Name:   info.Name(),
		Handle: l.fs.issue(l.billy, path),
		Attrs:  &attrs,
		Cookie: cookieFor(uint64(l.next)),
	}
	l.next++
	return true
}

func (l *listIterator) Entry() fsapi.DirEntry    { return l.current }
func (l *listIterator) Verifier() fsapi.Verifier { return l.verifier }
func (l *listIterator) Err() error               { return nil }
func (l *listIterator) Close() error             { return nil }

// streamIterator wraps a handler's own iterator, which produces entries one at
// a time and already carries handles.
type streamIterator struct {
	fs      *FS
	billy   billy.Filesystem
	dir     []string
	it      DirIterator
	current fsapi.DirEntry
}

func (s *streamIterator) Next(ctx context.Context) bool {
	if !s.it.Next() {
		return false
	}
	info := s.it.FileInfo()
	_, path := join(s.billy, s.dir, info.Name())
	attrs := toAttrs(s.billy, path, info)
	s.current = fsapi.DirEntry{
		Name:   info.Name(),
		Handle: fsapi.Handle(s.it.Handle()),
		Attrs:  &attrs,
		Cookie: cookieFor(s.it.Cookie()),
	}
	return true
}

func (s *streamIterator) Entry() fsapi.DirEntry    { return s.current }
func (s *streamIterator) Verifier() fsapi.Verifier { return fsapi.Verifier(s.it.Verifier()) }
func (s *streamIterator) Err() error               { return translate(s.it.Err()) }

func (s *streamIterator) Close() error {
	s.it.Close()
	return nil
}
