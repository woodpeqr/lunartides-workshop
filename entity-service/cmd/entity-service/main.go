// Command entity-service is the student-owned REST entity service.
//
// Runs as a container in the docker-compose stack (service "entity-service").
// Talks OTLP/gRPC to the collector at otel-collector:4317. Default listen addr
// :8081 (env ENTITY_ADDR). Can also run on the host via `go run ./cmd/entity-service`
// — override OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317 then.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	// available for runtime instrumentation
	_ "go.opentelemetry.io/contrib/instrumentation/runtime"

	"github.com/woodpeqr/lunartides-workshop/entity-service/internal/handlers"
	"github.com/woodpeqr/lunartides-workshop/entity-service/internal/store"
	"github.com/woodpeqr/lunartides-workshop/entity-service/internal/telemetry"
)

// config is the resolved runtime configuration, sourced from env.
type config struct {
	addr      string
	storePath string
	tel       telemetry.Config
}

// loadConfig reads ENTITY_ADDR, ENTITY_STORE_PATH and OTEL_* env vars, applying
// defaults.
func loadConfig() config {
	return config{
		addr:      env("ENTITY_ADDR", ":8081"),
		storePath: env("ENTITY_STORE_PATH", store.DefaultStorePath),
		tel: telemetry.Config{
			ServiceName: env("OTEL_SERVICE_NAME", "entity-service"),
			Endpoint:    env("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317"),
			Insecure:    envBool("OTEL_EXPORTER_OTLP_INSECURE", true),
		},
	}
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b
		}
	}
	return def
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("entity-service: %v", err)
	}
}

func run() error {
	cfg := loadConfig()

	// Root context cancelled on SIGINT/SIGTERM for graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Telemetry plumbing: pre-wired, trusted. Providers flushed on exit.
	providers, err := telemetry.Init(ctx, cfg.tel)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := providers.Shutdown(shutdownCtx); err != nil {
			log.Printf("entity-service: telemetry shutdown: %v", err)
		}
	}()

	// Ensure the store file's parent directory exists.
	if dir := filepath.Dir(cfg.storePath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	// Wire the store + handlers onto a stdlib mux (Go 1.22+ routing).
	st := store.New(cfg.storePath)
	h := handlers.New(st)
	mux := http.NewServeMux()
	h.Register(mux)

	// One example startup log via otel/log.
	emitStartupLog(ctx, cfg)

	// Wrap the mux in the one example middleware (a span + a request counter).
	handler, err := baseSignal(mux)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Serve in the background; report bind/serve errors on a channel.
	serveErr := make(chan error, 1)
	go func() {
		log.Printf("entity-service: listening on %s (service=%s otlp=%s store=%s)", cfg.addr, cfg.tel.ServiceName, cfg.tel.Endpoint, cfg.storePath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	// Block until shutdown signal or a serve error.
	select {
	case <-ctx.Done():
		log.Print("entity-service: shutdown signal received")
	case err := <-serveErr:
		return err
	}

	// Graceful drain.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// baseSignal is the ONE example of HTTP middleware instrumentation: it opens a
// single span per request with a single attribute (the request path) and bumps
// a single request counter. This is deliberately minimal — the rest of the
// service's observability is the student's to build.
func baseSignal(next http.Handler) (http.Handler, error) {
	tracer := otel.Tracer("entity-service")
	meter := otel.Meter("entity-service")

	requests, err := meter.Int64Counter(
		"entity.requests.total",
		metric.WithDescription("Total HTTP requests handled by entity-service."),
	)
	if err != nil {
		return nil, err
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ONE span, ONE attribute: the request path.
		ctx, span := tracer.Start(r.Context(), "request",
			trace.WithAttributes(attribute.String("http.target", r.URL.Path)),
		)
		defer span.End()

		// ONE metric: a request counter with a single path attribute.
		requests.Add(ctx, 1, metric.WithAttributes(attribute.String("path", r.URL.Path)))

		next.ServeHTTP(w, r.WithContext(ctx))
	}), nil
}

// emitStartupLog writes a single otel/log record at startup — the one log
// example.
func emitStartupLog(ctx context.Context, cfg config) {
	logger := global.Logger("entity-service")
	var rec otellog.Record
	rec.SetBody(otellog.StringValue("entity-service starting"))
	rec.SetSeverity(otellog.SeverityInfo)
	rec.AddAttributes(
		otellog.String("service.name", cfg.tel.ServiceName),
		otellog.String("listen.addr", cfg.addr),
		otellog.String("store.path", cfg.storePath),
	)
	logger.Emit(ctx, rec)
}
