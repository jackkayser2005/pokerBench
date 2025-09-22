package llm

import (
	"strings"
	"testing"
)

func TestResolveAPIConfigDefaults(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	cfg, err := resolveAPIConfig("meta-llama/llama-3.1-70b-instruct")
	if err != nil {
		t.Fatalf("resolveAPIConfig returned error: %v", err)
	}
	if cfg.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("unexpected default base URL: %q", cfg.BaseURL)
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

func TestResolveAPIConfigOverrides(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_API_BASE", "https://router.example.com/v1")
	t.Setenv("OPENROUTER_SITE_URL", "https://example.com/app")
	t.Setenv("OPENROUTER_TITLE", "Custom Title")
	cfg, err := resolveAPIConfig("meta-llama/llama-3.1-70b-instruct")
	if err != nil {
		t.Fatalf("resolveAPIConfig returned error: %v", err)
	}
	if cfg.BaseURL != "https://router.example.com/v1" {
		t.Fatalf("unexpected base URL: %q", cfg.BaseURL)
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

func TestResolveAPIConfigInvalidSiteURL(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_SITE_URL", "ftp://example.com/bad")
	if _, err := resolveAPIConfig("meta-llama/llama-3.1-70b-instruct"); err == nil {
		t.Fatalf("expected error for invalid site URL, got nil")
	}
}

func TestResolveAPIConfigSiteURLNormalization(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
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

func TestResolveAPIConfigSiteURLLocalhost(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
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

func TestResolveAPIConfigMissingKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("OPENROUTER_MODEL", "meta-llama/llama-3.1-70b-instruct")

	if _, err := resolveAPIConfig(""); err == nil {
		t.Fatalf("expected error when OPENROUTER_API_KEY is missing")
	} else if !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveAPIConfigMissingModel(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_MODEL", "")

	if _, err := resolveAPIConfig(""); err == nil {
		t.Fatalf("expected error when OPENROUTER_MODEL is missing")
	} else if !strings.Contains(err.Error(), "OPENROUTER_MODEL") {
		t.Fatalf("unexpected error: %v", err)
	}
}
