package nfs

import (
	"errors"
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
