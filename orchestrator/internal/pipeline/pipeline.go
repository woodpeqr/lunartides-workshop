package pipeline

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/woodpeqr/lunartides-workshop/internal/config"
	"github.com/woodpeqr/lunartides-workshop/internal/telemetry"
	"github.com/woodpeqr/lunartides-workshop/internal/worker"
)

// sampleContact is the contact record the pipeline processes each tick.
// Students can change this to test different inputs.
var sampleContact = worker.Contact{
	Name:    "Alex Johnson",
	Company: "Acme Corp",
	Email:   "alex.johnson@acme.com",
}

// Run executes the enrichment pipeline in a loop until ctx is cancelled.
func Run(ctx context.Context) {
	log := telemetry.Logger()
	for {
		runOnce(ctx, log)
		select {
		case <-ctx.Done():
			return
		case <-time.After(config.PipelineInterval):
		}
	}
}

func runOnce(ctx context.Context, log *zap.Logger) {
	validated, err := worker.Validate(ctx, sampleContact)
	if err != nil {
		log.Warn("validate failed", zap.Error(err))
		validated = sampleContact
	}

	enriched, err := worker.Enrich(ctx, validated)
	if err != nil {
		log.Warn("enrich failed", zap.Error(err))
		enriched = validated
	}

	_, err = worker.Score(ctx, enriched)
	if err != nil {
		log.Warn("score failed", zap.Error(err))
	}
}
