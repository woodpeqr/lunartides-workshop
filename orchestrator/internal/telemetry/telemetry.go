package telemetry

import (
	"context"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/woodpeqr/lunartides-workshop/internal/config"
)

var logger *zap.Logger

// Setup initialises the three OTel providers (trace, metric, log) with OTLP gRPC
// exporters and wires zap to the OTel log bridge. The returned shutdown function
// must be called on process exit.
func Setup(ctx context.Context) (shutdown func(context.Context) error, err error) {
	res, _ := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(config.ServiceName),
		),
	)

	traceExp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(config.OTelEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	metricExp, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(config.OTelEndpoint),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp)),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	logExp, err := otlploggrpc.New(ctx,
		otlploggrpc.WithEndpoint(config.OTelEndpoint),
		otlploggrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)),
		sdklog.WithResource(res),
	)

	// Wire zap → OTel log bridge
	otelCore := otelzap.NewCore(config.ServiceName, otelzap.WithLoggerProvider(lp))
	baseLogger, _ := zap.NewProduction()
	logger = zap.New(zapcore.NewTee(baseLogger.Core(), otelCore))

	// Health metric: orchestrator.health = 0 (intentional placeholder for students)
	meter := mp.Meter(config.ServiceName)
	_, _ = meter.Int64ObservableGauge(
		"orchestrator.health",
		metric.WithDescription("Orchestrator health indicator (pre-wired boilerplate)"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(0)
			return nil
		}),
	)

	shutdown = func(ctx context.Context) error {
		tp.Shutdown(ctx)
		mp.Shutdown(ctx)
		lp.Shutdown(ctx)
		return nil
	}
	return shutdown, nil
}

// EmitStartupSignals emits three boilerplate OTel signals on startup:
// 1. A zap log "Orchestrator started" forwarded via the OTel log bridge
// 2. The health gauge (registered in Setup, reported on each metric collection)
// 3. A traced HTTP GET to the local /health endpoint
//
// The health server must already be listening before this is called.
func EmitStartupSignals(ctx context.Context) {
	logger.Info("Orchestrator started")

	client := &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
		Timeout:   2 * time.Second,
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:8080/health", nil)
	resp, err := client.Do(req)
	if err != nil {
		logger.Warn("startup health trace failed", zap.Error(err))
		return
	}
	resp.Body.Close()
}

// Logger returns the configured zap logger for use by other packages.
func Logger() *zap.Logger { return logger }
