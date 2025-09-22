package llm

import (
	"net/http"
	"testing"
)

func TestSetHeaderPreserveCase(t *testing.T) {
	hdr := http.Header{}
	const rawKey = "X-HTTP-Referer"
	setHeaderPreserveCase(hdr, rawKey, "https://example.com/app")
	canonical := http.CanonicalHeaderKey(rawKey)
	found := false
	for k, vals := range hdr {
		if k == rawKey {
			found = true
			if len(vals) != 1 || vals[0] != "https://example.com/app" {
				t.Fatalf("expected %s slice to be preserved, got %+v", rawKey, vals)
			}
		}
		if canonical != rawKey && k == canonical {
			t.Fatalf("unexpected canonical header variant present: %+v", hdr)
		}
	}
	if !found {
		t.Fatalf("expected to find %s key in header map", rawKey)
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
