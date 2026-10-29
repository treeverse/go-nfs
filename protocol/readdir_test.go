package protocol

import (
	"testing"

	"github.com/treeverse/go-nfs/fsapi"
)

// keepsEverything is a filesystem whose handles live as long as the mount, and
// which therefore implements no fsapi.HandleKeeper.
type keepsEverything struct {
	fsapi.FS
}

// keepsSome is a filesystem that holds only so many handles at once.
type keepsSome struct {
	fsapi.FS
	limit int
}

func (k keepsSome) HandleLimit() int { return k.limit }

// A listing is bounded by what the filesystem can keep, so that the handles in
// one reply are all still alive when the client reads it.
func TestMaxListEntries(t *testing.T) {
	for _, tc := range []struct {
		name string
		fs   fsapi.FS
		want int
	}{
		{"a filesystem that keeps every handle is unbounded", keepsEverything{}, 0},
		{"half the limit, leaving the rest for handles the client holds", keepsSome{limit: 1024}, 512},
		{"an odd limit rounds down", keepsSome{limit: 9}, 4},
		{"a limit of two still reports one entry", keepsSome{limit: 2}, 1},
		{"a limit too small to halve bounds at one, not at none", keepsSome{limit: 1}, 1},
		{"a limit of zero is still a limit", keepsSome{limit: 0}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := maxListEntries(tc.fs); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}
