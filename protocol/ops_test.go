package protocol

import (
	"errors"
	"strings"
	"testing"
)

// statusOf returns the NFS status a name check reported, or NFSStatusOk for a
// name it accepted.
func statusOf(t *testing.T, err error) NFSStatus {
	t.Helper()
	if err == nil {
		return NFSStatusOk
	}
	var status *NFSStatusError
	if !errors.As(err, &status) {
		t.Fatalf("got %v, want an *NFSStatusError", err)
	}
	return status.NFSStatus
}

// A name is one component of a path, so the server refuses anything that is
// not: a filesystem told it may trust that promise would otherwise walk a path
// the client never looked up.  checkNewName refuses the same names, and "."
// and ".." on top of them.
func TestCheckName(t *testing.T) {
	for _, tc := range []struct {
		name        string
		input       string
		wantName    NFSStatus
		wantNewName NFSStatus
	}{
		{"an ordinary name", "x.txt", NFSStatusOk, NFSStatusOk},
		{"the longest name allowed", strings.Repeat("x", PathNameMax), NFSStatusOk, NFSStatusOk},
		{"one character longer", strings.Repeat("x", PathNameMax+1), NFSStatusNameTooLong, NFSStatusNameTooLong},
		{"empty", "", NFSStatusInval, NFSStatusInval},
		{"two components", "sub/deeper", NFSStatusInval, NFSStatusInval},
		{"a leading separator", "/etc", NFSStatusInval, NFSStatusInval},
		{"a trailing separator", "dir/", NFSStatusInval, NFSStatusInval},
		{"a parent walked by separator", "../x", NFSStatusInval, NFSStatusInval},
		{"an embedded NUL", "x\x00y", NFSStatusInval, NFSStatusInval},
		// "." and ".." are names a lookup must still reach, because the
		// server answers both itself rather than asking the filesystem.
		{"dot", ".", NFSStatusOk, NFSStatusExist},
		{"dotdot", "..", NFSStatusOk, NFSStatusExist},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusOf(t, checkName(tc.input)); got != tc.wantName {
				t.Errorf("checkName: got %v, want %v", got, tc.wantName)
			}
			if got := statusOf(t, checkNewName(tc.input)); got != tc.wantNewName {
				t.Errorf("checkNewName: got %v, want %v", got, tc.wantNewName)
			}
		})
	}
}
