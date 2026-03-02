package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type SupervisorOptions struct {
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Sleep          func(ctx context.Context, d time.Duration) error
	OnRestart      func(attempt int, runErr error, backoff time.Duration)
}

func RunWithSupervisor(ctx context.Context, run func(context.Context) error, opts SupervisorOptions) error {
	initial := opts.InitialBackoff
	maximum := opts.MaxBackoff
	if initial <= 0 {
		initial = time.Second
	}
	if maximum < initial {
		maximum = initial
	}
	sleep := opts.Sleep
	if sleep == nil {
		sleep = sleepWithContext
	}

	backoff := initial
	attempt := 0

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		err := runRecover(ctx, run)
		if err == nil {
			return nil
		}
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return nil
		}

		attempt++
		if opts.OnRestart != nil {
			opts.OnRestart(attempt, err, backoff)
		}

		if sleepErr := sleep(ctx, backoff); sleepErr != nil {
			return err
		}
		backoff = nextBackoff(backoff, maximum)
	}
}

func runRecover(ctx context.Context, run func(context.Context) error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic recovered: %v", r)
		}
	}()
	return run(ctx)
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func nextBackoff(current time.Duration, maximum time.Duration) time.Duration {
	next := current * 2
	if next > maximum {
		return maximum
	}
	return next
}
