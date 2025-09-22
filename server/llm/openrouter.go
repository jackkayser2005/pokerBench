package llm

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
)

type apiConfig struct {
	Provider     string
	APIKey       string
	Model        string
	BaseURL      string
	HeaderName   string
	HeaderPrefix string
	ExtraHeaders map[string]string
}

func resolveAPIConfig(model string) (apiConfig, error) {
	trimmedModel := strings.TrimSpace(model)
	if trimmedModel == "" {
		trimmedModel = firstNonEmpty(
			os.Getenv("OPENROUTER_MODEL"),
			os.Getenv("OPENAI_MODEL"),
		)
	}
	if trimmedModel == "" {
		return apiConfig{}, errors.New("model missing: set OPENROUTER_MODEL/OPENAI_MODEL or pass a value")
	}

	if key := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")); key != "" {
		return resolveOpenRouterConfig(trimmedModel, key)
	}
	if key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); key != "" {
		return resolveOpenAIConfig(trimmedModel, key)
	}

	return apiConfig{}, errors.New("API key missing: set OPENROUTER_API_KEY or OPENAI_API_KEY (or provide the secrets file)")
}

func resolveOpenRouterConfig(model, key string) (apiConfig, error) {
	cfg := apiConfig{
		Provider:     "openrouter",
		APIKey:       key,
		Model:        model,
		HeaderName:   "Authorization",
		HeaderPrefix: "Bearer ",
		ExtraHeaders: map[string]string{},
	}

	base := firstNonEmpty(
		os.Getenv("OPENROUTER_API_BASE"),
		os.Getenv("OPENROUTER_BASE_URL"),
	)
	base = strings.TrimSpace(base)
	if base == "" {
		base = "https://openrouter.ai/api/v1"
	}
	cfg.BaseURL = strings.TrimRight(base, "/")

	headerName := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY_HEADER"))
	if headerName != "" {
		cfg.HeaderName = headerName
	}
	if prefix := os.Getenv("OPENROUTER_API_KEY_PREFIX"); prefix != "" {
		cfg.HeaderPrefix = prefix
	} else if cfg.HeaderName != "Authorization" {
		cfg.HeaderPrefix = ""
	}

	siteURL, err := resolveOpenRouterSiteURL()
	if err != nil {
		log.Printf("OpenRouter site URL error: %v", err)
		return apiConfig{}, err
	}
	if siteURL != "" {
		cfg.ExtraHeaders["HTTP-Referer"] = siteURL
		cfg.ExtraHeaders["Referer"] = siteURL
	}

	title := strings.TrimSpace(os.Getenv("OPENROUTER_TITLE"))
	if title == "" {
		title = strings.TrimSpace(os.Getenv("APP_NAME"))
	}
	if title == "" {
		title = "PokerBench"
	}
	if title != "" {
		cfg.ExtraHeaders["X-Title"] = title
	}

	return cfg, nil
}

func resolveOpenAIConfig(model, key string) (apiConfig, error) {
	cfg := apiConfig{
		Provider:     "openai",
		APIKey:       key,
		Model:        model,
		HeaderName:   "Authorization",
		HeaderPrefix: "Bearer ",
		ExtraHeaders: map[string]string{},
	}

	base := firstNonEmpty(
		os.Getenv("OPENAI_API_BASE"),
		os.Getenv("OPENAI_BASE_URL"),
	)
	base = strings.TrimSpace(base)
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	cfg.BaseURL = strings.TrimRight(base, "/")

	return cfg, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func resolveOpenRouterSiteURL() (string, error) {
	siteURL := strings.TrimSpace(os.Getenv("OPENROUTER_SITE_URL"))
	if siteURL == "" {
		siteURL = strings.TrimSpace(os.Getenv("SITE_URL"))
	}
	if siteURL == "" {
		port := strings.TrimSpace(os.Getenv("PORT"))
		if port != "" {
			siteURL = fmt.Sprintf("http://localhost:%s", port)
		} else {
			siteURL = "http://localhost"
		}
	}

	normalized, err := normalizeOpenRouterSiteURL(siteURL)
	if err != nil {
		return "", err
	}
	return normalized, nil
}

func normalizeOpenRouterSiteURL(siteURL string) (string, error) {
	trimmed := strings.TrimSpace(siteURL)
	if trimmed == "" {
		return "", errors.New("missing OpenRouter site URL: set OPENROUTER_SITE_URL or SITE_URL")
	}
	trimmed = strings.TrimRight(trimmed, "/")

	if !strings.Contains(trimmed, "://") {
		lower := strings.ToLower(trimmed)
		switch {
		case strings.HasPrefix(lower, "localhost"),
			strings.HasPrefix(lower, "127."),
			strings.HasPrefix(lower, "[::1]"),
			strings.HasPrefix(lower, "[0:0:0:0:0:0:0:1]"):
			trimmed = "http://" + trimmed
		default:
			trimmed = "https://" + trimmed
		}
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid OpenRouter site URL %q: %w", siteURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("invalid OpenRouter site URL scheme %q: must be http or https", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("invalid OpenRouter site URL %q: host missing", siteURL)
	}

	parsed.Fragment = ""
	normalized := parsed.String()
	normalized = strings.TrimRight(normalized, "/")
	return normalized, nil
}
