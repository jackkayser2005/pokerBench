package llm

import (
	"net/http"
	"testing"
)

func TestSetHeaderPreserveCase(t *testing.T) {
	hdr := http.Header{}
	setHeaderPreserveCase(hdr, "HTTP-Referer", "https://example.com/app")
	if vals := hdr["HTTP-Referer"]; len(vals) != 1 || vals[0] != "https://example.com/app" {
		t.Fatalf("expected HTTP-Referer slice to be preserved, got %+v", vals)
	}
	if _, exists := hdr["Http-Referer"]; exists {
		t.Fatalf("unexpected canonical header variant present: %+v", hdr)
	}

	setHeaderPreserveCase(hdr, "Referer", "https://example.com/app")
	if got := hdr.Get("Referer"); got != "https://example.com/app" {
		t.Fatalf("expected Referer to be set via canonical path, got %q", got)
	}

	// Blank values should be ignored.
	setHeaderPreserveCase(hdr, "  ", "value")
	setHeaderPreserveCase(hdr, "X-Test", "   ")
	if _, exists := hdr[" "]; exists {
		t.Fatalf("expected blank header keys to be ignored")
	}
	if got := hdr.Get("X-Test"); got != "" {
		t.Fatalf("expected blank header values to be skipped, got %q", got)
	}
}

func TestAdjustOpenRouterPayloadForRetryResponseFormat(t *testing.T) {
	payload := map[string]any{
		"response_format": map[string]any{"type": "json_schema"},
	}
	body := []byte(`{"error":{"message":"model does not support response_format","param":"response_format"}}`)
	removed := map[string]bool{}
	if !adjustOpenRouterPayloadForRetry(payload, body, removed) {
		t.Fatalf("expected response_format adjustment to trigger retry")
	}
	if _, ok := payload["response_format"]; ok {
		t.Fatalf("response_format should have been removed from payload")
	}
	if adjustOpenRouterPayloadForRetry(payload, body, removed) {
		t.Fatalf("second adjustment attempt should not trigger once removed")
	}
}

func TestAdjustOpenRouterPayloadForRetryReasoning(t *testing.T) {
	payload := map[string]any{
		"reasoning": map[string]any{"effort": "high"},
	}
	body := []byte(`{"error":{"message":"reasoning parameter unsupported"}}`)
	removed := map[string]bool{}
	if !adjustOpenRouterPayloadForRetry(payload, body, removed) {
		t.Fatalf("expected reasoning adjustment to trigger retry")
	}
	if _, ok := payload["reasoning"]; ok {
		t.Fatalf("reasoning should have been removed from payload")
	}
	if adjustOpenRouterPayloadForRetry(payload, body, removed) {
		t.Fatalf("second adjustment attempt should not trigger once removed")
	}
}
