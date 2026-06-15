package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/woodpeqr/lunartides-workshop/internal/config"
)

// Contact mirrors the shared Contact struct from the workers.
type Contact struct {
	Name            string   `json:"name"`
	Company         string   `json:"company"`
	Email           string   `json:"email"`
	NormalizedEmail string   `json:"normalized_email,omitempty"`
	Valid           bool     `json:"valid,omitempty"`
	Industry        string   `json:"industry,omitempty"`
	Size            string   `json:"size,omitempty"`
	Region          string   `json:"region,omitempty"`
	Score           int      `json:"score,omitempty"`
	Tags            []string `json:"tags,omitempty"`
}

var httpClient = &http.Client{
	Transport: otelhttp.NewTransport(http.DefaultTransport),
}

func callWorker(ctx context.Context, url string, in Contact) (Contact, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return Contact{}, fmt.Errorf("marshal contact: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Contact{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return Contact{}, fmt.Errorf("call %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Contact{}, fmt.Errorf("worker %s returned %d", url, resp.StatusCode)
	}

	var out Contact
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Contact{}, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

func Validate(ctx context.Context, c Contact) (Contact, error) {
	return callWorker(ctx, config.FluxURL, c)
}

func Enrich(ctx context.Context, c Contact) (Contact, error) {
	return callWorker(ctx, config.RiftURL, c)
}

func Score(ctx context.Context, c Contact) (Contact, error) {
	return callWorker(ctx, config.SwellURL, c)
}
