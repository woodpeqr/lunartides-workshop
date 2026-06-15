using System.Net;
using MoonTide;

var builder = WebApplication.CreateBuilder(args);
builder.WebHost.UseUrls("http://*:8080");

// Set up OTel (trace, metrics, logs)
Telemetry.Setup(builder.Services, builder.Logging);

// Register HttpClient and Pipeline
builder.Services.AddHttpClient<Pipeline>();
builder.Services.AddSingleton<Pipeline>();

var app = builder.Build();

// Health endpoint
app.MapGet("/health", () => Results.Ok(new { status = "ok" }));

// Start the pipeline loop in background
var pipeline = app.Services.GetRequiredService<Pipeline>();
var cts = new CancellationTokenSource();
var pipelineTask = Task.Run(() => pipeline.RunAsync(cts.Token));

// Emit startup signals
var logger = app.Services.GetRequiredService<ILogger<Program>>();
var httpClient = app.Services.GetRequiredService<IHttpClientFactory>().CreateClient();
// Allow a brief moment for the server to bind before making the health call
await Task.Delay(50);
await Telemetry.EmitStartupSignalsAsync(logger, httpClient);

app.Lifetime.ApplicationStopping.Register(() => cts.Cancel());

await app.RunAsync();
