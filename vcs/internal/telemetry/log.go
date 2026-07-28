// Trace-correlated log helpers for the vcs hot paths.
//
// The otelslog (slog -> OTel) bridge is NOT vendored in this build, so instead
// of routing Go's slog we emit directly through the native otel/log API
// (log/global) — which is still the real OTLP log path to the collector/Loki,
// not a stdout fallback. Every record is stamped with trace_id/span_id from the
// active span so logs cross-link to traces in Grafana (CONTRACT §5 correlation).
//
// This is the student's job (emit signal); the provider chain wired in Init
// stays untouched.
package telemetry

import (
	"context"

	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/trace"
)

// Log emits one otel/log record at sev with body and attrs, injecting
// trace_id/span_id from ctx's active span for trace<->log correlation.
func Log(ctx context.Context, sev otellog.Severity, body string, attrs ...otellog.KeyValue) {
	var rec otellog.Record
	rec.SetBody(otellog.StringValue(body))
	rec.SetSeverity(sev)
	// Correlation: surface the active trace/span ids as explicit attributes so
	// the log backend (Loki) can link a record back to its trace in Grafana.
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		attrs = append(attrs,
			otellog.String("trace_id", sc.TraceID().String()),
			otellog.String("span_id", sc.SpanID().String()),
		)
	}
	rec.AddAttributes(attrs...)
	global.Logger("vcs").Emit(ctx, rec)
}

// Info/Warn/Error are severity-tagged conveniences over Log.
func Info(ctx context.Context, body string, attrs ...otellog.KeyValue) {
	Log(ctx, otellog.SeverityInfo, body, attrs...)
}

func Warn(ctx context.Context, body string, attrs ...otellog.KeyValue) {
	Log(ctx, otellog.SeverityWarn, body, attrs...)
}

func Error(ctx context.Context, body string, attrs ...otellog.KeyValue) {
	Log(ctx, otellog.SeverityError, body, attrs...)
}
