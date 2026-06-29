package nfs

import (
	"context"
	"errors"
	"testing"
)

// A panic in a handler (or in a billy operation that escaped the recovery
// helpers) must not unwind the per-connection serve goroutine and crash the
// process. dispatch recovers it and turns it into a SERVERFAULT so the request
// fails with an NFS error reply instead.
func TestConnDispatchRecoversPanic(t *testing.T) {
	c := &conn{Server: &Server{}}
	w := &response{conn: c, req: &request{}}

	appError := c.dispatch(context.Background(), w, func(ctx context.Context, w *response, userHandler Handler) error {
		panic("handler panic")
	})

	var statusErr *NFSStatusError
	if !errors.As(appError, &statusErr) {
		t.Fatalf("expected *NFSStatusError, got %v", appError)
	}
	if statusErr.NFSStatus != NFSStatusServerFault {
		t.Errorf("expected NFSStatusServerFault, got %v", statusErr.NFSStatus)
	}
	if !errors.Is(appError, errHandlerPanic) {
		t.Errorf("expected errHandlerPanic, got %v", appError)
	}
}

// A handler that returns an error normally must pass that error through
// unchanged.
func TestConnDispatchPassesThroughError(t *testing.T) {
	c := &conn{Server: &Server{}}
	w := &response{conn: c, req: &request{}}

	sentinel := errors.New("app error")
	appError := c.dispatch(context.Background(), w, func(ctx context.Context, w *response, userHandler Handler) error {
		return sentinel
	})

	if !errors.Is(appError, sentinel) {
		t.Errorf("expected sentinel error, got %v", appError)
	}
}
