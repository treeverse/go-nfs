package nfs

import (
	"crypto/sha256"
	"encoding/binary"
	"sync"
)

// pagedCookieTable maps (nfsVerifier, lastNFSCookie) to the tree-native
// resume point for the next page of a paged directory listing.  It holds
// one entry per page boundary per active listing — negligibly small.
type pagedCookieTable struct {
	mu      sync.RWMutex
	entries map[pagedCookieKey]pagedCookieValue
}

type pagedCookieKey struct {
	nfsVerifier uint64
	nfsCookie   uint64
}

type pagedCookieValue struct {
	treeCookie   []byte
	treeVerifier []byte
}

func newPagedCookieTable() *pagedCookieTable {
	return &pagedCookieTable{entries: make(map[pagedCookieKey]pagedCookieValue)}
}

func (t *pagedCookieTable) store(nfsVerifier, nfsCookie uint64, treeCookie, treeVerifier []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries[pagedCookieKey{nfsVerifier, nfsCookie}] = pagedCookieValue{
		treeCookie:   treeCookie,
		treeVerifier: treeVerifier,
	}
}

func (t *pagedCookieTable) lookup(nfsVerifier, nfsCookie uint64) (pagedCookieValue, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	v, ok := t.entries[pagedCookieKey{nfsVerifier, nfsCookie}]
	return v, ok
}

// hashVerifier maps arbitrary-length tree verifier bytes to the uint64 NFS
// cookie verifier.  The mapping is stable and always non-zero (0 means "no
// verifier" in the NFS protocol).
func hashVerifier(v []byte) uint64 {
	h := sha256.Sum256(v)
	result := binary.BigEndian.Uint64(h[:8])
	if result == 0 {
		result = 1
	}
	return result
}
