using System.Diagnostics;
using System.Diagnostics.Metrics;
using OpenTelemetry;
using OpenTelemetry.Logs;
using OpenTelemetry.Metrics;
using OpenTelemetry.Resources;
using OpenTelemetry.Trace;

namespace MoonTide;

public static class Telemetry
{
    public static readonly ActivitySource ActivitySource = new(Config.ServiceName);
    private static readonly Meter Meter = new(Config.ServiceName);

    public static void Setup(IServiceCollection services, ILoggingBuilder logging)
    {
        var resource = ResourceBuilder.CreateDefault()
            .AddService(Config.ServiceName);

        // Register HttpClient instrumentation (auto-creates child spans for all HttpClient calls)
        services.AddOpenTelemetry()
            .WithTracing(b => b
                .SetResourceBuilder(resource)
                .AddHttpClientInstrumentation()
                .AddSource(Config.ServiceName)
                .AddOtlpExporter(o => o.Endpoint = new Uri(Config.OtelEndpoint)))
            .WithMetrics(b => b
                .SetResourceBuilder(resource)
                .AddHttpClientInstrumentation()
                .AddMeter(Config.ServiceName)
                .AddOtlpExporter(o => o.Endpoint = new Uri(Config.OtelEndpoint)));

        logging.AddOpenTelemetry(o =>
        {
            o.SetResourceBuilder(resource);
            o.AddOtlpExporter(otlp => otlp.Endpoint = new Uri(Config.OtelEndpoint));
        });

        // Register orchestrator.health gauge (always 0 — boilerplate)
        Meter.CreateObservableGauge(
            "orchestrator.health",
            () => 0,
            description: "Orchestrator health indicator (pre-wired boilerplate)");
    }

    public static async Task EmitStartupSignalsAsync(ILogger logger, HttpClient httpClient)
    {
        // 1. Startup log
        logger.LogInformation("Orchestrator started");

        // 3. Startup trace via HTTP GET to own /health
        // AddHttpClientInstrumentation() already instruments httpClient calls
        try
        {
            await httpClient.GetAsync("http://127.0.0.1:8080/health");
        }
        catch (Exception ex)
        {
            logger.LogWarning(ex, "Startup health trace failed");
        }
    }
}
