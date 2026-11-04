package card

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SeRGei93/meme/server/internal/mask"
	"github.com/SeRGei93/meme/server/internal/store"
)

// Client talks to an LLM. OpenAIClient is the production implementation;
// tests and alternative providers can implement this interface directly.
type Client interface {
	Complete(ctx context.Context, messages []Message) (string, error)
}

// Generator is the queue processor that turns a final session into a card. Any
// LLM failure is terminal for the attempt: the heuristic card written at ingest
// stays and card_status becomes failed, so a bad provider response cannot lose
// the record or retry forever.
type Generator struct {
	Store         *store.Store
	Client        Client
	Model         string
	MaxInputChars int
	Now           func() time.Time
	Logger        *slog.Logger
}

// Process generates and stores one card. Store failures are returned so the
// queue retries them; LLM and validation failures fall back to the heuristic
// card and are recorded as a failed card.
func (g *Generator) Process(ctx context.Context, job store.Job) error {
	if g.Store == nil {
		return errors.New("card generator: store is nil")
	}
	if job.RecordID == nil {
		return nil
	}

	record, err := g.Store.GetRecord(ctx, *job.RecordID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("card generator: load record %d: %w", *job.RecordID, err)
	}
	// A queued final ingest shares its created_at with the record's
	// finalized_at; memories are stamped the same way. A mismatch means a newer
	// final version already queued its own job, so this one must not touch the
	// current card.
	if record.FinalizedAt == nil || !record.FinalizedAt.Equal(job.CreatedAt) {
		g.logger().Info("skipping stale card job",
			"record_id", record.ID,
			"job_id", job.ID,
			"job_created_at", job.CreatedAt,
			"finalized_at", record.FinalizedAt,
		)
		return nil
	}

	if g.Client == nil {
		return g.keepHeuristic(ctx, record, errors.New("LLM client is not configured"))
	}

	content, err := g.Client.Complete(ctx, BuildMessages(record, g.MaxInputChars))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return g.fallback(ctx, record, fmt.Errorf("generate card: %w", err))
	}

	generated, err := Validate([]byte(content))
	if err != nil {
		return g.fallback(ctx, record, err)
	}

	applied, err := g.Store.SaveCard(ctx, record.ID, record.FinalizedAt, generated, g.Model, g.now())
	if err != nil {
		return err
	}
	if !applied {
		g.logger().Info("card discarded because the record is no longer queued for this final version",
			"record_id", record.ID,
		)
		return nil
	}

	g.logger().Info("card generated",
		"record_id", record.ID,
		"model", g.Model,
		"questions", len(generated.Questions),
		"keywords", len(generated.Keywords),
		"atoms", len(generated.Atoms),
	)
	return nil
}

func (g *Generator) fallback(ctx context.Context, record store.Record, cause error) error {
	applied, err := g.Store.FailCard(ctx, record.ID, record.FinalizedAt, g.now())
	if err != nil {
		return err
	}
	if applied {
		g.logger().Warn("card generation failed; keeping the heuristic card",
			"record_id", record.ID,
			"error", mask.Text(cause.Error()),
		)
	}
	return nil
}

func (g *Generator) keepHeuristic(ctx context.Context, record store.Record, cause error) error {
	applied, err := g.Store.KeepHeuristicCard(ctx, record.ID, record.FinalizedAt, g.now())
	if err != nil {
		return err
	}
	if applied {
		g.logger().Warn("card generation is not configured; keeping the heuristic card",
			"record_id", record.ID,
			"reason", mask.Text(cause.Error()),
		)
	}
	return nil
}

func (g *Generator) now() time.Time {
	if g.Now == nil {
		return time.Now().UTC()
	}
	return g.Now().UTC()
}

func (g *Generator) logger() *slog.Logger {
	if g.Logger == nil {
		return slog.Default()
	}
	return g.Logger
}
