package protocol

import "testing"

// A large read is allocated for what the file can return, not for what the
// client asked for, so that one request for megabytes at the end of a short
// file does not cost megabytes several times over.
func TestReadCount(t *testing.T) {
	const (
		file  = uint64(1) << 20
		huge  = uint64(1) << 40 // longer than a uint32 can hold
		asked = uint32(1) << 20
	)
	for _, tc := range []struct {
		name      string
		requested uint32
		offset    uint64
		size      uint64
		want      uint32
	}{
		{"request fits well inside the file", 4096, 0, file, 4096},
		{"request ends exactly at the end", 4096, file - 4096, file, 4096},
		{"request runs past the end", asked, file - 100, file, 100},
		{"offset exactly at the end", 4096, file, file, 0},
		{"offset beyond the end", 4096, file + 1, file, 0},
		{"empty file", 4096, 0, 0, 0},
		{"file longer than a uint32", asked, 0, huge, asked},
		{"file longer than a uint32, read near its end", asked, huge - 7, huge, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := readCount(tc.requested, tc.offset, tc.size); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}
