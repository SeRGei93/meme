// Package queue runs the ingestion job worker: claim, retry with exponential
// backoff, terminal failure and restart recovery. Card generation itself is a
// Processor supplied by the caller.
package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SeRGei93/meme/server/internal/store"
)

const (
	// DefaultPollInterval is how often the worker looks for due jobs.
	DefaultPollInterval = time.Second
	// DefaultMaxAttempts is the terminal failure threshold.
	DefaultMaxAttempts = 5
	// BaseBackoff is the first retry delay.
	BaseBackoff = time.Second
	// MaxBackoff is the retry delay ceiling.
	MaxBackoff = time.Minute
)

// Processor performs the actual work for one claimed job. Returning an error
// schedules a retry; after the worker's attempt limit the job becomes failed.
// Wrap an error with Permanent to fail immediately without retrying.
type Processor interface {
	Process(ctx context.Context, job store.Job) error
}

// PermanentError marks a processor failure that must not be retried.
type PermanentError struct {
	Err error
}

// Error implements error.
func (e *PermanentError) Error() string {
	return e.Err.Error()
}

// Unwrap exposes the wrapped error.
func (e *PermanentError) Unwrap() error {
	return e.Err
}

// Permanent marks err as non-retryable. A nil error stays nil.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

func isPermanent(err error) bool {
	var permanent *PermanentError
	return errors.As(err, &permanent)
}

// ProcessorFunc adapts a function to Processor.
type ProcessorFunc func(ctx context.Context, job store.Job) error

// Process calls f.
func (f ProcessorFunc) Process(ctx context.Context, job store.Job) error {
	return f(ctx, job)
}

// Worker claims due jobs and runs them through a Processor. A zero value is
// usable when Store and Processor are set.
type Worker struct {
	Store        *store.Store
	Processor    Processor
	PollInterval time.Duration
	MaxAttempts  int
	Now          func() time.Time
	Logger       *slog.Logger
}

// Backoff returns the exponential retry delay for an attempt count. Attempt 1
// waits BaseBackoff; the delay doubles up to MaxBackoff.
func Backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := BaseBackoff
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= MaxBackoff {
			return MaxBackoff
		}
	}
	return delay
}

// Run recovers jobs interrupted by a previous process, then processes due jobs
// until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	if _, err := w.Recover(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}

	ticker := time.NewTicker(w.pollInterval())
	defer ticker.Stop()

	for {
		for {
			processed, err := w.RunOnce(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			if !processed || ctx.Err() != nil {
				break
			}
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Recover moves jobs left running by a restart back to the queue.
func (w *Worker) Recover(ctx context.Context) (int, error) {
	if w.Store == nil {
		return 0, errors.New("queue worker: store is nil")
	}
	return w.Store.RecoverRunningJobs(ctx, w.now())
}

// RunOnce claims and processes at most one due job. It reports whether a job
// was claimed; processor failures are reflected in the job row, not returned
// as an error.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	if w.Store == nil {
		return false, errors.New("queue worker: store is nil")
	}
	if w.Processor == nil {
		return false, errors.New("queue worker: processor is nil")
	}

	job, ok, err := w.Store.ClaimNextJob(ctx, w.now())
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}

	processErr := w.process(ctx, job)
	if ctx.Err() != nil {
		// Leave the job running: the next process recovers it instead of
		// recording a shutdown as a failed attempt.
		return true, nil
	}

	if processErr == nil {
		if err := w.Store.CompleteJob(ctx, job.ID, w.now()); err != nil {
			return true, err
		}
		w.logger().Debug("job done", "job_id", job.ID, "attempts", job.Attempts)
		return true, nil
	}

	if isPermanent(processErr) || job.Attempts >= w.maxAttempts() {
		if err := w.Store.FailJob(ctx, job.ID, w.now(), processErr.Error()); err != nil {
			return true, err
		}
		w.logger().Error("job failed",
			"job_id", job.ID,
			"attempts", job.Attempts,
			"error", processErr,
		)
		return true, nil
	}

	next := w.now().Add(Backoff(job.Attempts))
	if err := w.Store.RetryJob(ctx, job.ID, w.now(), next, processErr.Error()); err != nil {
		return true, err
	}
	w.logger().Warn("job retry scheduled",
		"job_id", job.ID,
		"attempts", job.Attempts,
		"next_retry_at", next,
		"error", processErr,
	)
	return true, nil
}

func (w *Worker) process(ctx context.Context, job store.Job) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("processor panic: %v", recovered)
		}
	}()
	return w.Processor.Process(ctx, job)
}

func (w *Worker) pollInterval() time.Duration {
	if w.PollInterval <= 0 {
		return DefaultPollInterval
	}
	return w.PollInterval
}

func (w *Worker) maxAttempts() int {
	if w.MaxAttempts <= 0 {
		return DefaultMaxAttempts
	}
	return w.MaxAttempts
}

func (w *Worker) now() time.Time {
	if w.Now == nil {
		return time.Now().UTC()
	}
	return w.Now().UTC()
}

func (w *Worker) logger() *slog.Logger {
	if w.Logger == nil {
		return slog.Default()
	}
	return w.Logger
}
