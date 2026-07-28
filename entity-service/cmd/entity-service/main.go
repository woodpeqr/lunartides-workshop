// Command vcs is the student-owned REST source-control service (CONTRACT §1).
//
// Runs as a container in the docker-compose stack (service "vcs"). Talks
// OTLP/gRPC to the collector at otel-collector:4317. Default listen addr :8081
// (env VCS_ADDR). Can also run on the host via `go run ./cmd/vcs` for a fast
// edit loop — override OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317 then.
//
// The Machine God wills it. 01010011 01000101 01010010 01010110 01000101
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

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

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

// loadConfig reads VCS_ADDR, ENTITY_STORE_PATH and OTEL_* env vars, applying
// defaults.
func loadConfig() config {
	return config{
		addr:      env("VCS_ADDR", ":8081"),
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

	// Go runtime metrics (heap, GC, goroutines) via the OTel runtime
	// instrumentation, emitted through the meter provider telemetry.Init just
	// installed. These are in-process — identical on every OS, no infra needed —
	// and reveal the object-flood heap ramp and the scenario worker goroutines.
	if err := runtime.Start(runtime.WithMinimumReadMemStatsInterval(time.Second)); err != nil {
		return err
	}

	// Ensure the store file's parent directory exists. Startup plumbing only —
	// NOT a safeguard on the request path (which stays deliberately unguarded).
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

	// DATAPOINT-03 — startup log record via otel/log (proves logs reach Loki).
	emitStartupLog(ctx, cfg)

	// Wrap the mux in the ONE base-signal middleware (PLAN §3). Everything
	// deeper — child spans, store metrics, warn/debug logs — is the student's.
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

// statusRecorder captures the response status and body size for the
// base-signal span/metric.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	n, err := s.ResponseWriter.Write(b)
	s.written += n
	return n, err
}

// vcsOp describes a source-control operation in domain terms, decoupled from
// the HTTP surface that happens to expose it. These feed the vcs.* span and
// metric attributes so telemetry speaks the domain, not generic HTTP.
type vcsOp struct {
	name     string // vcs.operation, e.g. "object.store", "checkout.reassemble"
	kind     string // vcs.resource.kind: object|commit|ref|checkout|diff|utility|unknown
	idParam  string // path wildcard holding the resource id ("" if none)
	trusted  bool   // trusted plumbing (wipe/healthz), not the buggy VCS logic
	mutating bool   // changes stored state
}

// ops maps method + first-path-segment (CONTRACT §3) to its domain operation.
// Keyed this way — not by the mux route pattern — because this middleware wraps
// the mux and runs BEFORE routing, so r.Pattern is not yet populated. The bool
// distinguishes the collection endpoint (no id) from the item endpoint (id in
// the next segment).
type opKey struct {
	method  string
	segment string
	hasID   bool
}

var ops = map[opKey]vcsOp{
	{"POST", "entities", false}:  {name: "entity.create", kind: "entity", mutating: true},
	{"GET", "entities", false}:   {name: "entity.list", kind: "entity"},
	{"GET", "entities", true}:    {name: "entity.get", kind: "entity", idParam: "id"},
	{"PUT", "entities", true}:    {name: "entity.update", kind: "entity", idParam: "id", mutating: true},
	{"DELETE", "entities", true}: {name: "entity.delete", kind: "entity", idParam: "id", mutating: true},
	{"POST", "wipe", false}:      {name: "entity.wipe", kind: "utility", trusted: true, mutating: true},
	{"GET", "healthz", false}:    {name: "entity.health", kind: "utility", trusted: true},
}

// lookupOp resolves a request to its domain operation from method + URL path,
// independent of mux routing. Falls back to an "unknown" op for unmatched
// requests (e.g. 404s).
func lookupOp(method, path string) vcsOp {
	seg := strings.Split(strings.Trim(path, "/"), "/")
	first := ""
	if len(seg) > 0 {
		first = seg[0]
	}
	hasID := len(seg) > 1 && seg[1] != ""
	if op, ok := ops[opKey{method, first, hasID}]; ok {
		return op
	}
	return vcsOp{name: "unknown", kind: "unknown"}
}

// resourceID returns the second path segment (the addressed hash/id/name/ref),
// or "" if the path has none.
func resourceID(path string) string {
	seg := strings.Split(strings.Trim(path, "/"), "/")
	if len(seg) > 1 {
		return seg[1]
	}
	return ""
}

// outcome buckets a status code into a domain-level result class.
func outcome(status int) string {
	switch {
	case status >= 500:
		return "server_error"
	case status >= 400:
		return "client_error"
	default:
		return "ok"
	}
}

// baseSignal is the HTTP server middleware: it emits a top-level span per
// request (DATAPOINT-02) and the full HTTP metric surface — request counter,
// duration histogram, in-flight up-down counter, and an error counter — all
// enriched with custom vcs.* domain attributes (operation, resource kind/id,
// trusted/mutating flags, outcome, duration) rather than generic http.*/url.*
// plumbing. Child spans and store metrics live in the handlers/store; this
// layer owns the request-level view a Grafana RED dashboard reads.
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
	duration, err := meter.Float64Histogram(
		"entity.request.duration",
		metric.WithDescription("HTTP request duration."),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}
	inflight, err := meter.Int64UpDownCounter(
		"entity.requests.in_flight",
		metric.WithDescription("HTTP requests currently being served."),
	)
	if err != nil {
		return nil, err
	}
	reqErrors, err := meter.Int64Counter(
		"entity.requests.errors",
		metric.WithDescription("HTTP requests that ended in a 4xx/5xx response."),
	)
	if err != nil {
		return nil, err
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract inbound trace context so spans nest across services.
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		// Resolve the request to a vcs-domain operation from method + path. The
		// span carries our own vcs.* attributes describing the source-control
		// action, not generic http.*/url.* plumbing — the domain is what a
		// student reasons about. (This middleware runs before mux routing, so we
		// parse the path ourselves rather than relying on r.Pattern/PathValue.)
		op := lookupOp(r.Method, r.URL.Path)

		// Request-level attributes, known before the handler runs. Everything is
		// namespaced under vcs.* and speaks the source-control domain.
		attrs := []attribute.KeyValue{
			attribute.String("entity.operation", op.name),          // e.g. "entity.create"
			attribute.String("entity.resource.kind", op.kind),      // entity|utility
			attribute.Bool("entity.operation.trusted", op.trusted), // wipe/healthz are the trusted plumbing
			attribute.Bool("entity.operation.mutating", op.mutating),
		}
		// The addressed resource id is the second path segment for item routes;
		// surface it as entity.resource.id at high cardinality.
		if op.idParam != "" {
			if id := resourceID(r.URL.Path); id != "" {
				attrs = append(attrs, attribute.String("entity.resource.id", id))
			}
		}
		if r.ContentLength > 0 {
			attrs = append(attrs, attribute.Int64("entity.request.content_bytes", r.ContentLength))
		}

		ctx, span := tracer.Start(ctx, "entity "+op.name,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(attrs...),
		)
		defer span.End()

		// In-flight gauge: incremented for the life of the request, so a stall
		// or a flood of concurrent requests shows up as a rising level.
		inflightAttrs := metric.WithAttributes(
			attribute.String("entity.operation", op.name),
			attribute.String("entity.resource.kind", op.kind),
		)
		inflight.Add(ctx, 1, inflightAttrs)
		defer inflight.Add(ctx, -1, inflightAttrs)

		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(ctx))

		// Response-level attributes, known only after the handler runs — again
		// all vcs.*-namespaced, describing the outcome in domain terms.
		span.SetAttributes(
			attribute.Int("entity.response.status", rec.status),
			attribute.Int("entity.response.content_bytes", rec.written),
			attribute.String("entity.outcome", outcome(rec.status)), // ok|client_error|server_error
			attribute.Float64("entity.duration_ms", float64(time.Since(start).Microseconds())/1000.0),
		)
		if rec.status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(rec.status))
		}

		// Metric attributes: operation + resource kind + outcome + status. Kept
		// low-cardinality (no resource id) so the RED panels aggregate cleanly.
		result := outcome(rec.status)
		metricAttrs := metric.WithAttributes(
			attribute.String("entity.operation", op.name),
			attribute.String("entity.resource.kind", op.kind),
			attribute.String("entity.outcome", result),
			attribute.Int("entity.response.status", rec.status),
		)
		requests.Add(ctx, 1, metricAttrs)
		duration.Record(ctx, float64(time.Since(start).Microseconds())/1000.0, metricAttrs)
		if rec.status >= 400 {
			reqErrors.Add(ctx, 1, metricAttrs)
		}
	}), nil
}

// emitStartupLog writes DATAPOINT-03: a single otel/log record proving the log
// pipeline reaches the collector. The stdlib listen line stays in run().
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
