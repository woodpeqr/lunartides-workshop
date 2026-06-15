package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/woodpeqr/lunartides-workshop/internal/health"
	"github.com/woodpeqr/lunartides-workshop/internal/pipeline"
	"github.com/woodpeqr/lunartides-workshop/internal/telemetry"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 1. Start health server first so that telemetry's startup trace can reach it.
	mux := http.NewServeMux()
	mux.HandleFunc("/health", health.Handler)
	srv := &http.Server{Addr: ":8080", Handler: mux}
	go srv.ListenAndServe() //nolint:errcheck
	time.Sleep(50 * time.Millisecond)

	// 2. Set up OTel providers.
	shutdown, err := telemetry.Setup(ctx)
	if err != nil {
		// WHY: telemetry failure is non-fatal; the pipeline must still run for
		// students who haven't wired the collector yet.
		zap.L().Warn("telemetry setup failed", zap.Error(err))
		shutdown = func(context.Context) error { return nil }
	}
	defer shutdown(context.Background())

	// 3. Emit startup signals (log + startup trace span).
	telemetry.EmitStartupSignals(ctx)

	// 4. Run the enrichment pipeline until shutdown.
	go pipeline.Run(ctx)

	<-ctx.Done()
	srv.Shutdown(context.Background()) //nolint:errcheck
}
