package winfile

import (
	"context"
	"time"
)

type RetryOptions struct {
	MaxWait      time.Duration
	InitialDelay time.Duration
	MaxDelay     time.Duration
	OnRetry      func(attempt int, err error, elapsed time.Duration)
}

// RetryOperation retries Windows filesystem mutations blocked by transient
// sharing or access-denied locks. Other platforms and permanent errors return
// immediately.
func RetryOperation(ctx context.Context, operation func() error, options RetryOptions) error {
	return retry(ctx, operation, retryableFilesystemError, options)
}

func retry(
	ctx context.Context,
	operation func() error,
	retryable func(error) bool,
	options RetryOptions,
) error {
	maxWait := options.MaxWait
	if maxWait <= 0 {
		maxWait = 30 * time.Second
	}
	delay := options.InitialDelay
	if delay <= 0 {
		delay = 100 * time.Millisecond
	}
	maxDelay := options.MaxDelay
	if maxDelay <= 0 {
		maxDelay = time.Second
	}

	startedAt := time.Now()
	attempt := 0
	for {
		err := operation()
		if err == nil || !retryable(err) {
			return err
		}
		attempt++
		elapsed := time.Since(startedAt)
		if options.OnRetry != nil {
			options.OnRetry(attempt, err, elapsed)
		}
		remaining := maxWait - elapsed
		if remaining <= 0 {
			return err
		}
		wait := delay
		if wait > remaining {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
		if delay < maxDelay {
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}
}
