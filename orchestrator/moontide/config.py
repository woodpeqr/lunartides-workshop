# PipelineInterval controls how often MoonTide runs the enrichment pipeline (seconds).
# Students: feel free to adjust this value.
PIPELINE_INTERVAL_SECONDS = 1

# Worker service URLs
FLUX_URL = "http://flux:8080/validate"
RIFT_URL = "http://rift:8080/enrich"
SWELL_URL = "http://swell:8080/score"

# OTel collector endpoint
OTEL_ENDPOINT = "http://otel-collector:4317"

# Service name for OTel
SERVICE_NAME = "moontide"
