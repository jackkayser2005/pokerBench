package llm

import (
	"errors"
	"os"
	"strings"
)

type providerKind int

const (
	providerOpenAI providerKind = iota
	providerOpenRouter
)

type apiConfig struct {
	Kind         providerKind
	APIKey       string
	Model        string
	BaseURL      string
	HeaderName   string
	HeaderPrefix string
	Organization string
	ExtraHeaders map[string]string
}

func resolveAPIConfig(model string) (apiConfig, error) {
	cfg := apiConfig{
		Model:        strings.TrimSpace(model),
		ExtraHeaders: map[string]string{},
	}

	if preferOpenRouterEnv() {
		cfg.Kind = providerOpenRouter
	} else {
		cfg.Kind = providerOpenAI
	}

	if override := strings.ToLower(strings.TrimSpace(os.Getenv("LLM_PROVIDER"))); override != "" {
		switch override {
		case "openrouter":
			cfg.Kind = providerOpenRouter
		case "openai":
			cfg.Kind = providerOpenAI
		}
	}

	base := firstNonEmpty(
		os.Getenv("OPENAI_API_BASE"),
		os.Getenv("OPENAI_BASE_URL"),
		os.Getenv("OPENROUTER_API_BASE"),
		os.Getenv("OPENROUTER_BASE_URL"),
	)
	base = strings.TrimSpace(base)
	if base == "" {
		if cfg.Kind == providerOpenRouter {
			base = "https://openrouter.ai/api/v1"
		} else {
			base = "https://api.openai.com/v1"
		}
	}
	cfg.BaseURL = strings.TrimRight(base, "/")
	if strings.Contains(strings.ToLower(cfg.BaseURL), "openrouter") {
		cfg.Kind = providerOpenRouter
	}

	openAIKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	openRouterKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	switch cfg.Kind {
	case providerOpenRouter:
		if openRouterKey != "" {
			cfg.APIKey = openRouterKey
		} else {
			cfg.APIKey = openAIKey
		}
	default:
		cfg.APIKey = openAIKey
	}
	if cfg.Kind == providerOpenRouter && openRouterKey != "" {
		cfg.APIKey = openRouterKey
	}
	if cfg.APIKey == "" {
		cfg.APIKey = openRouterKey
	}
	if cfg.APIKey == "" {
		return apiConfig{}, errors.New("API key missing: set OPENAI_API_KEY or OPENROUTER_API_KEY")
	}

	headerName := strings.TrimSpace(os.Getenv("OPENAI_API_KEY_HEADER"))
	if headerName == "" {
		headerName = strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY_HEADER"))
	}
	if headerName == "" {
		headerName = "Authorization"
	}
	prefix := os.Getenv("OPENAI_API_KEY_PREFIX")
	if prefix == "" {
		prefix = os.Getenv("OPENROUTER_API_KEY_PREFIX")
	}
	if headerName == "Authorization" && strings.TrimSpace(prefix) == "" {
		prefix = "Bearer "
	}
	cfg.HeaderName = headerName
	cfg.HeaderPrefix = prefix
	cfg.Organization = strings.TrimSpace(os.Getenv("OPENAI_ORG"))

	if cfg.Kind == providerOpenRouter {
		if v := strings.TrimSpace(os.Getenv("OPENROUTER_SITE_URL")); v != "" {
			cfg.ExtraHeaders["HTTP-Referer"] = v
			cfg.ExtraHeaders["Referer"] = v
		}
		if v := strings.TrimSpace(os.Getenv("OPENROUTER_TITLE")); v != "" {
			cfg.ExtraHeaders["X-Title"] = v
		}
	}

	if cfg.Model == "" {
		if cfg.Kind == providerOpenRouter {
			cfg.Model = strings.TrimSpace(os.Getenv("OPENROUTER_MODEL"))
		}
		if cfg.Model == "" {
			cfg.Model = strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
		}
	}
	if cfg.Model == "" {
		return apiConfig{}, errors.New("model missing: set OPENAI_MODEL/OPENROUTER_MODEL or pass a value")
	}

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

func PreferOpenRouter() bool {
	return preferOpenRouterEnv()
}
