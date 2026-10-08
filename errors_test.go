package nfs

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
)

func TestStatusErrorKeepsFallback(t *testing.T) {
	err := errors.New("backend exploded")
	statusErr := statusError(err, NFSStatusAccess)
	if statusErr.NFSStatus != NFSStatusAccess {
		t.Errorf("status = %v, want %v", statusErr.NFSStatus, NFSStatusAccess)
	}
	if !errors.Is(statusErr, err) {
		t.Errorf("statusError dropped the wrapped error: %v", statusErr)
	}
}

func TestStatusFromError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want NFSStatus
	}{
		{name: "status_error_keeps_status", err: &NFSStatusError{NFSStatusNotSupp, os.ErrPermission}, want: NFSStatusNotSupp},
		{name: "wrapped_status_error", err: fmt.Errorf("apply: %w", &NFSStatusError{NFSStatusROFS, os.ErrPermission}), want: NFSStatusROFS},
		{name: "enotsup", err: &os.PathError{Op: "remove", Path: "d", Err: syscall.ENOTSUP}, want: NFSStatusNotSupp},
		{name: "eopnotsupp", err: syscall.EOPNOTSUPP, want: NFSStatusNotSupp},
		{name: "unknown_uses_fallback", err: errors.New("backend exploded"), want: NFSStatusIO},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusFromError(tc.err, NFSStatusIO); got != tc.want {
				t.Errorf("status = %v, want %v", got, tc.want)
			}
		})
	}
}
