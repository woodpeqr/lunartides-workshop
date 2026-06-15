namespace MoonTide;

public static class Config
{
    /// <summary>
    /// How often MoonTide runs the enrichment pipeline (seconds).
    /// Students: feel free to adjust this value.
    /// </summary>
    public const int PipelineIntervalSeconds = 1;

    public const string FluxUrl  = "http://flux:8080/validate";
    public const string RiftUrl  = "http://rift:8080/enrich";
    public const string SwellUrl = "http://swell:8080/score";

    public const string OtelEndpoint = "http://otel-collector:4317";
    public const string ServiceName  = "moontide";
}
