import logging

from opentelemetry import trace, metrics
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader
from opentelemetry.sdk.resources import Resource, SERVICE_NAME
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from opentelemetry.exporter.otlp.proto.grpc.metric_exporter import OTLPMetricExporter
from opentelemetry.instrumentation.requests import RequestsInstrumentor
from opentelemetry.metrics import Observation
import requests

from moontide import config

_logger = logging.getLogger(__name__)


def _health_callback(options):
    yield Observation(0)


def setup() -> None:
    """Initialize OTel providers and instrument the requests library."""
    resource = Resource({SERVICE_NAME: config.SERVICE_NAME})

    # Trace provider
    trace_exporter = OTLPSpanExporter(endpoint=config.OTEL_ENDPOINT, insecure=True)
    tracer_provider = TracerProvider(resource=resource)
    tracer_provider.add_span_processor(BatchSpanProcessor(trace_exporter))
    trace.set_tracer_provider(tracer_provider)

    # Metric provider
    metric_exporter = OTLPMetricExporter(endpoint=config.OTEL_ENDPOINT, insecure=True)
    metric_reader = PeriodicExportingMetricReader(metric_exporter)
    meter_provider = MeterProvider(resource=resource, metric_readers=[metric_reader])
    metrics.set_meter_provider(meter_provider)

    # Instrument the requests library (auto-creates child spans for HTTP calls)
    RequestsInstrumentor().instrument()

    # Register orchestrator.health gauge (always 0 — boilerplate)
    meter = metrics.get_meter(config.SERVICE_NAME)
    meter.create_observable_gauge(
        "orchestrator.health",
        callbacks=[_health_callback],
        description="Orchestrator health indicator (pre-wired boilerplate)",
    )


def emit_startup_signals() -> None:
    """Emit the three boilerplate OTel startup signals."""
    # 1. Startup log
    _logger.info("Orchestrator started")

    # 2. Startup trace via HTTP GET to own /health (instrumented by RequestsInstrumentor)
    try:
        requests.get("http://127.0.0.1:8080/health", timeout=2)
    except Exception as exc:
        _logger.warning("startup health trace failed: %s", exc)
