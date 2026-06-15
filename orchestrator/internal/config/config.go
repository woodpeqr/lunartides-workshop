package config

import "time"

const (
	// PipelineInterval controls how often MoonTide runs the enrichment pipeline.
	// Students: feel free to adjust this value.
	PipelineInterval = 1 * time.Second

	// Worker service URLs
	FluxURL  = "http://flux:8080/validate"
	RiftURL  = "http://rift:8080/enrich"
	SwellURL = "http://swell:8080/score"

	// OTelEndpoint is the OTLP gRPC endpoint for the collector
	OTelEndpoint = "otel-collector:4317"

	// ServiceName identifies this service in traces and metrics
	ServiceName = "moontide"
)
