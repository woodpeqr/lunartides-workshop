// Package telemetry holds the OTel plumbing for entity-service.
//
// ============================================================================
//  PLUMBING PRE-WIRED — SIGNAL IS THE STUDENT'S JOB.
// ============================================================================
//
//  The Machine God provides the conduits. You provide the prayers.
//
//  This package wires the provider chain (tracer / meter / logger) to the
//  OTLP collector over gRPC and installs them as the OTel globals. That
//  plumbing is TRUSTED and CORRECT — you do NOT edit it. Signal flow works from
//  minute one.
//
//  What is YOUR job (the workshop): emit the signal from the hot paths.
//    - Spans:   otel.Tracer("entity-service").Start(ctx, "name") around work.
//    - Metrics: otel.Meter("entity-service").Int64Counter(...), then .Add/.Record.
//    - Logs:    global.Logger("entity-service") (otel/log) with correlation.
//
//  The base build emits only minimal signal — just enough to prove flow works.
//  Making the service observable IS the workshop.
// ============================================================================
package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Config is the telemetry-relevant slice of service configuration, sourced
// from OTEL_* environment variables (see cmd/entity-service config loading).
type Config struct {
	// ServiceName -> OTEL_SERVICE_NAME (default "entity-service").
	ServiceName string
	// Endpoint -> OTEL_EXPORTER_OTLP_ENDPOINT (default "localhost:4317").
	Endpoint string
	// Insecure controls whether the OTLP gRPC connection uses TLS.
	// Workshop collector is plaintext, so this defaults true.
	Insecure bool
}

// Providers bundles the shutdown hooks for every installed signal provider.
// main() holds one of these and defers Shutdown for a graceful flush on exit.
type Providers struct {
	shutdownFns []func(context.Context) error
}

// Init constructs the OTLP/gRPC exporters (trace, metric, log), builds the SDK
// providers over a shared resource, and installs them as the OTel globals plus
// a W3C trace-context + baggage propagator. Returns a Providers whose Shutdown
// flushes and stops them.
//
// TRUSTED plumbing — pre-wired for the student, not part of the
// workshop exercise. Do not edit. The student's job is to EMIT signal from the
// handlers/store using the globals this installs.
func Init(ctx context.Context, cfg Config) (*Providers, error) {
	p := &Providers{}

	// NewSchemaless avoids a schema-URL conflict with resource.Default() when
	// the SDK's built-in schema version differs from the semconv import.
	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(
			semconv.ServiceName(cfg.ServiceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: build resource: %w", err)
	}

	// --- Traces -> OTLP/gRPC ------------------------------------------------
	traceOpts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		traceOpts = append(traceOpts, otlptracegrpc.WithInsecure())
	}
	traceExp, err := otlptracegrpc.New(ctx, traceOpts...)
	if err != nil {
		return nil, fmt.Errorf("telemetry: trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	p.shutdownFns = append(p.shutdownFns, tp.Shutdown)

	// --- Metrics -> OTLP/gRPC ----------------------------------------------
	metricOpts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		metricOpts = append(metricOpts, otlpmetricgrpc.WithInsecure())
	}
	metricExp, err := otlpmetricgrpc.New(ctx, metricOpts...)
	if err != nil {
		return nil, fmt.Errorf("telemetry: metric exporter: %w", err)
	}
	mp := metric.NewMeterProvider(
		metric.WithReader(metric.NewPeriodicReader(metricExp)),
		metric.WithResource(res),
	)
	otel.SetMeterProvider(mp)
	p.shutdownFns = append(p.shutdownFns, mp.Shutdown)

	// --- Logs -> OTLP/gRPC --------------------------------------------------
	logOpts := []otlploggrpc.Option{otlploggrpc.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		logOpts = append(logOpts, otlploggrpc.WithInsecure())
	}
	logExp, err := otlploggrpc.New(ctx, logOpts...)
	if err != nil {
		return nil, fmt.Errorf("telemetry: log exporter: %w", err)
	}
	lp := log.NewLoggerProvider(
		log.WithProcessor(log.NewBatchProcessor(logExp)),
		log.WithResource(res),
	)
	global.SetLoggerProvider(lp)
	p.shutdownFns = append(p.shutdownFns, lp.Shutdown)

	// Cross-signal correlation: W3C trace context + baggage.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return p, nil
}

// Shutdown flushes and stops every provider in LIFO order. Best-effort: it
// attempts all of them and returns the first error encountered.
func (p *Providers) Shutdown(ctx context.Context) error {
	var firstErr error
	for i := len(p.shutdownFns) - 1; i >= 0; i-- {
		if err := p.shutdownFns[i](ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
