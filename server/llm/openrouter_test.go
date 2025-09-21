package llm

import "testing"

func TestResolveAPIConfigOpenRouterDefaults(t *testing.T) {
	t.Setenv("OPENAI_API_BASE", "https://openrouter.ai/api/v1")
	t.Setenv("OPENAI_API_KEY", "test-key")
	cfg, err := resolveAPIConfig("meta-llama/llama-3.1-70b-instruct")
	if err != nil {
		t.Fatalf("resolveAPIConfig returned error: %v", err)
	}
	if cfg.Kind != providerOpenRouter {
		t.Fatalf("expected providerOpenRouter, got %v", cfg.Kind)
	}
	if got := cfg.ExtraHeaders["HTTP-Referer"]; got != "http://localhost" {
		t.Fatalf("unexpected HTTP-Referer: %q", got)
	}
	if got := cfg.ExtraHeaders["Referer"]; got != "http://localhost" {
		t.Fatalf("unexpected Referer: %q", got)
	}
	if got := cfg.ExtraHeaders["X-Title"]; got != "PokerBench" {
		t.Fatalf("unexpected X-Title: %q", got)
	}
}

func TestResolveAPIConfigOpenRouterOverrides(t *testing.T) {
	t.Setenv("OPENAI_API_BASE", "https://openrouter.ai/api/v1")
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENROUTER_SITE_URL", "https://example.com/app")
	t.Setenv("OPENROUTER_TITLE", "Custom Title")
	cfg, err := resolveAPIConfig("meta-llama/llama-3.1-70b-instruct")
	if err != nil {
		t.Fatalf("resolveAPIConfig returned error: %v", err)
	}
	if cfg.Kind != providerOpenRouter {
		t.Fatalf("expected providerOpenRouter, got %v", cfg.Kind)
	}
	if got := cfg.ExtraHeaders["HTTP-Referer"]; got != "https://example.com/app" {
		t.Fatalf("unexpected HTTP-Referer: %q", got)
	}
	if got := cfg.ExtraHeaders["Referer"]; got != "https://example.com/app" {
		t.Fatalf("unexpected Referer: %q", got)
	}
	if got := cfg.ExtraHeaders["X-Title"]; got != "Custom Title" {
		t.Fatalf("unexpected X-Title: %q", got)
	}
}

func TestResolveAPIConfigOpenRouterInvalidSiteURL(t *testing.T) {
	t.Setenv("OPENAI_API_BASE", "https://openrouter.ai/api/v1")
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENROUTER_SITE_URL", "ftp://example.com/bad")
	if _, err := resolveAPIConfig("meta-llama/llama-3.1-70b-instruct"); err == nil {
		t.Fatalf("expected error for invalid site URL, got nil")
	}
}

func TestResolveAPIConfigOpenRouterSiteURLNormalization(t *testing.T) {
	t.Setenv("OPENAI_API_BASE", "https://openrouter.ai/api/v1")
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENROUTER_SITE_URL", "app.example.com/bench/")
	cfg, err := resolveAPIConfig("meta-llama/llama-3.1-70b-instruct")
	if err != nil {
		t.Fatalf("resolveAPIConfig returned error: %v", err)
	}
	if got := cfg.ExtraHeaders["HTTP-Referer"]; got != "https://app.example.com/bench" {
		t.Fatalf("unexpected HTTP-Referer: %q", got)
	}
	if got := cfg.ExtraHeaders["Referer"]; got != "https://app.example.com/bench" {
		t.Fatalf("unexpected Referer: %q", got)
	}
}

func TestResolveAPIConfigOpenRouterSiteURLLocalhost(t *testing.T) {
	t.Setenv("OPENAI_API_BASE", "https://openrouter.ai/api/v1")
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENROUTER_SITE_URL", "localhost:3000/ui")
	cfg, err := resolveAPIConfig("meta-llama/llama-3.1-70b-instruct")
	if err != nil {
		t.Fatalf("resolveAPIConfig returned error: %v", err)
	}
	want := "http://localhost:3000/ui"
	if got := cfg.ExtraHeaders["HTTP-Referer"]; got != want {
		t.Fatalf("unexpected HTTP-Referer: %q", got)
	}
	if got := cfg.ExtraHeaders["Referer"]; got != want {
		t.Fatalf("unexpected Referer: %q", got)
	}
}
