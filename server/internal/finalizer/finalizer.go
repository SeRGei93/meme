// Package finalizer closes sessions that stopped receiving live updates and
// queues their cards for generation.
package finalizer

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/SeRGei93/meme/server/internal/mask"
	"github.com/SeRGei93/meme/server/internal/store"
)

const (
	// DefaultIdle is the contract's 30-minute silence window.
	DefaultIdle = 30 * time.Minute
	// DefaultInterval is how often idle sessions are checked.
	DefaultInterval = time.Minute
)

// Finalizer finalizes idle sessions on a fixed interval.
type Finalizer struct {
	Store    *store.Store
	Idle     time.Duration
	Interval time.Duration
	Now      func() time.Time
	Logger   *slog.Logger
}

// Run finalizes idle sessions immediately, then on every interval until ctx is
// cancelled.
func (f *Finalizer) Run(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}

	ticker := time.NewTicker(f.interval())
	defer ticker.Stop()

	for {
		if _, err := f.RunOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			f.logger().Error("finalize idle sessions", "error", mask.Text(err.Error()))
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunOnce finalizes every session idle for at least Idle and returns the
// number of finalized records.
func (f *Finalizer) RunOnce(ctx context.Context) (int, error) {
	if f.Store == nil {
		return 0, errors.New("finalizer: store is nil")
	}

	finalized, err := f.Store.FinalizeIdleSessions(ctx, f.now(), f.idle())
	if err != nil {
		return 0, err
	}
	if finalized > 0 {
		f.logger().Info("idle sessions finalized", "count", finalized)
	}
	return finalized, nil
}

func (f *Finalizer) idle() time.Duration {
	if f.Idle <= 0 {
		return DefaultIdle
	}
	return f.Idle
}

func (f *Finalizer) interval() time.Duration {
	if f.Interval <= 0 {
		return DefaultInterval
	}
	return f.Interval
}

func (f *Finalizer) now() time.Time {
	if f.Now == nil {
		return time.Now().UTC()
	}
	return f.Now().UTC()
}

func (f *Finalizer) logger() *slog.Logger {
	if f.Logger == nil {
		return slog.Default()
	}
	return f.Logger
}
