package runtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunWithSupervisorRestartsAfterPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls int32
	run := func(context.Context) error {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			panic("panic for retry")
		}
		cancel()
		return context.Canceled
	}

	opts := SupervisorOptions{
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     2 * time.Millisecond,
		Sleep: func(_ context.Context, _ time.Duration) error {
			return nil
		},
	}

	if err := RunWithSupervisor(ctx, run, opts); err != nil {
		t.Fatalf("RunWithSupervisor() error = %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("expected 3 run attempts, got %d", got)
	}
}

func TestRunWithSupervisorReturnsErrorForNonRecoverableExit(t *testing.T) {
	ctx := context.Background()
	expected := errors.New("fatal")

	run := func(context.Context) error {
		return expected
	}
	opts := SupervisorOptions{
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     2 * time.Millisecond,
		Sleep: func(_ context.Context, _ time.Duration) error {
			return context.Canceled
		},
	}

	err := RunWithSupervisor(ctx, run, opts)
	if !errors.Is(err, expected) {
		t.Fatalf("expected error %v, got %v", expected, err)
	}
}
