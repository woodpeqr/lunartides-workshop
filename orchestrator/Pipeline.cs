using System.Net.Http.Json;

namespace MoonTide;

public record Contact(
    string Name = "",
    string Company = "",
    string Email = "",
    string NormalizedEmail = "",
    bool Valid = false,
    string Industry = "",
    string Size = "",
    string Region = "",
    int Score = 0,
    string[]? Tags = null
);

public class Pipeline
{
    private static readonly Contact SampleContact = new(
        Name: "Alex Johnson",
        Company: "Acme Corp",
        Email: "alex.johnson@acme.com"
    );

    private readonly HttpClient _httpClient;
    private readonly ILogger<Pipeline> _logger;

    public Pipeline(HttpClient httpClient, ILogger<Pipeline> logger)
    {
        _httpClient = httpClient;
        _logger = logger;
    }

    public async Task RunAsync(CancellationToken ct)
    {
        while (!ct.IsCancellationRequested)
        {
            await RunOnceAsync(ct);
            await Task.Delay(TimeSpan.FromSeconds(Config.PipelineIntervalSeconds), ct)
                .ConfigureAwait(false);
        }
    }

    private async Task RunOnceAsync(CancellationToken ct)
    {
        var contact = SampleContact;

        // Step 1: Validate
        try
        {
            var resp = await _httpClient.PostAsJsonAsync(Config.FluxUrl, contact, ct);
            resp.EnsureSuccessStatusCode();
            contact = await resp.Content.ReadFromJsonAsync<Contact>(ct) ?? contact;
        }
        catch (Exception ex) { _logger.LogWarning(ex, "Validate failed"); }

        // Step 2: Enrich
        try
        {
            var resp = await _httpClient.PostAsJsonAsync(Config.RiftUrl, contact, ct);
            resp.EnsureSuccessStatusCode();
            contact = await resp.Content.ReadFromJsonAsync<Contact>(ct) ?? contact;
        }
        catch (Exception ex) { _logger.LogWarning(ex, "Enrich failed"); }

        // Step 3: Score
        try
        {
            var resp = await _httpClient.PostAsJsonAsync(Config.SwellUrl, contact, ct);
            resp.EnsureSuccessStatusCode();
        }
        catch (Exception ex) { _logger.LogWarning(ex, "Score failed"); }
    }
}
