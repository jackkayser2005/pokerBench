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
	cleaned, hintedKind, hasHint := parseModelSpec(model)
	cfg := apiConfig{
		Model:        strings.TrimSpace(cleaned),
		ExtraHeaders: map[string]string{},
	}

	provider := providerOpenAI
	if preferOpenRouterEnv() {
		provider = providerOpenRouter
	}

	if detected, ok := detectProviderFromEnv(cfg.Model); ok {
		provider = detected
	}

	if hasHint {
		provider = hintedKind
	}

	provider = detectProviderFromModel(provider, cfg.Model)

	overrideValue := strings.ToLower(strings.TrimSpace(os.Getenv("LLM_PROVIDER")))
	providerOverridden := false
	if overrideValue != "" {
		switch overrideValue {
		case "openrouter":
			provider = providerOpenRouter
			providerOverridden = true
		case "openai":
			provider = providerOpenAI
			providerOverridden = true
		}
	}

	if cfg.Model == "" {
		if provider == providerOpenRouter {
			cfg.Model = firstNonEmpty(
				os.Getenv("OPENROUTER_MODEL"),
				os.Getenv("OPENROUTER_MODEL_A"),
				os.Getenv("OPENROUTER_MODEL_B"),
				os.Getenv("OPENROUTER_MODEL_SB"),
				os.Getenv("OPENROUTER_MODEL_BB"),
			)
		}
		if cfg.Model == "" {
			cfg.Model = firstNonEmpty(
				os.Getenv("OPENAI_MODEL"),
				os.Getenv("OPENAI_MODEL_A"),
				os.Getenv("OPENAI_MODEL_B"),
				os.Getenv("OPENAI_MODEL_SB"),
				os.Getenv("OPENAI_MODEL_BB"),
			)
		}
		if cfg.Model == "" {
			cfg.Model = firstNonEmpty(
				os.Getenv("OPENROUTER_MODEL"),
				os.Getenv("OPENROUTER_MODEL_A"),
				os.Getenv("OPENROUTER_MODEL_B"),
				os.Getenv("OPENROUTER_MODEL_SB"),
				os.Getenv("OPENROUTER_MODEL_BB"),
			)
		}
	}

	if !providerOverridden {
		provider = detectProviderFromModel(provider, cfg.Model)
	}

	base := baseForProvider(provider)
	if strings.Contains(strings.ToLower(base), "openrouter") && provider != providerOpenRouter {
		provider = providerOpenRouter
		base = baseForProvider(provider)
	}

	cfg.Kind = provider
	cfg.BaseURL = base

	if cfg.Model == "" {
		return apiConfig{}, errors.New("model missing: set OPENAI_MODEL/OPENROUTER_MODEL or pass a value")
	}

	openAIKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	openRouterKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))

	switch provider {
	case providerOpenRouter:
		key := openRouterKey
		if key == "" && looksLikeOpenRouterKey(openAIKey) {
			key = openAIKey
		}
		if key == "" {
			return apiConfig{}, errors.New("OpenRouter API key missing: set OPENROUTER_API_KEY or mount ./secrets/openrouter_api_key.txt")
		}
		cfg.APIKey = key
	default:
		key := openAIKey
		if key == "" {
			return apiConfig{}, errors.New("OpenAI API key missing: set OPENAI_API_KEY or mount ./secrets/openai_api_key.txt")
		}
		cfg.APIKey = key
	}

	headerName := strings.TrimSpace(os.Getenv("OPENAI_API_KEY_HEADER"))
	headerPrefix := os.Getenv("OPENAI_API_KEY_PREFIX")
	if provider == providerOpenRouter {
		if v := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY_HEADER")); v != "" {
			headerName = v
		}
		if v := os.Getenv("OPENROUTER_API_KEY_PREFIX"); v != "" {
			headerPrefix = v
		}
	}
	if headerName == "" {
		headerName = "Authorization"
	}
	if strings.EqualFold(headerName, "authorization") && strings.TrimSpace(headerPrefix) == "" {
		headerPrefix = "Bearer "
	}
	cfg.HeaderName = headerName
	cfg.HeaderPrefix = headerPrefix
	if provider == providerOpenAI {
		cfg.Organization = strings.TrimSpace(os.Getenv("OPENAI_ORG"))
	}

	if provider == providerOpenRouter {
		if v := strings.TrimSpace(os.Getenv("OPENROUTER_SITE_URL")); v != "" {
			cfg.ExtraHeaders["HTTP-Referer"] = v
			cfg.ExtraHeaders["Referer"] = v
		}
		if v := strings.TrimSpace(os.Getenv("OPENROUTER_TITLE")); v != "" {
			cfg.ExtraHeaders["X-Title"] = v
		}
	}

	return cfg, nil
}

func detectProviderFromModel(current providerKind, model string) providerKind {
	lower := strings.ToLower(strings.TrimSpace(model))
	if lower == "" {
		return current
	}
	if strings.Contains(lower, "openrouter/") {
		return providerOpenRouter
	}
	return current
}

func parseModelSpec(raw string) (string, providerKind, bool) {
	trimmed := strings.TrimSpace(raw)
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "openrouter:"):
		return strings.TrimSpace(trimmed[len("openrouter:"):]), providerOpenRouter, true
	case strings.HasPrefix(lower, "openai:"):
		return strings.TrimSpace(trimmed[len("openai:"):]), providerOpenAI, true
	}
	return trimmed, providerOpenAI, false
}

func detectProviderFromEnv(model string) (providerKind, bool) {
	normalized := strings.TrimSpace(model)
	if normalized == "" {
		return providerOpenAI, false
	}
	matches := func(candidate string) (bool, providerKind, bool) {
		parsed, kind, hinted := parseModelSpec(candidate)
		if strings.EqualFold(strings.TrimSpace(parsed), normalized) {
			if hinted {
				return true, kind, true
			}
			return true, providerOpenAI, false
		}
		if strings.EqualFold(strings.TrimSpace(candidate), normalized) {
			return true, providerOpenAI, false
		}
		return false, providerOpenAI, false
	}

	routerCandidates := []string{
		os.Getenv("OPENROUTER_MODEL"),
		os.Getenv("OPENROUTER_MODEL_A"),
		os.Getenv("OPENROUTER_MODEL_B"),
		os.Getenv("OPENROUTER_MODEL_SB"),
		os.Getenv("OPENROUTER_MODEL_BB"),
	}
	for _, candidate := range routerCandidates {
		if ok, kind, hinted := matches(candidate); ok {
			if hinted {
				return kind, true
			}
			return providerOpenRouter, true
		}
	}

	openAICandidates := []string{
		os.Getenv("OPENAI_MODEL"),
		os.Getenv("OPENAI_MODEL_A"),
		os.Getenv("OPENAI_MODEL_B"),
		os.Getenv("OPENAI_MODEL_SB"),
		os.Getenv("OPENAI_MODEL_BB"),
	}
	for _, candidate := range openAICandidates {
		if ok, kind, hinted := matches(candidate); ok {
			if hinted {
				return kind, true
			}
			return providerOpenAI, true
		}
	}

	return providerOpenAI, false
}

func baseForProvider(kind providerKind) string {
	var base string
	switch kind {
	case providerOpenRouter:
		base = firstNonEmpty(
			os.Getenv("OPENROUTER_API_BASE"),
			os.Getenv("OPENROUTER_BASE_URL"),
		)
		if base == "" {
			candidate := firstNonEmpty(os.Getenv("OPENAI_API_BASE"), os.Getenv("OPENAI_BASE_URL"))
			if strings.Contains(strings.ToLower(candidate), "openrouter") {
				base = candidate
			}
		}
		if base == "" {
			base = "https://openrouter.ai/api/v1"
		}
	default:
		base = firstNonEmpty(
			os.Getenv("OPENAI_API_BASE"),
			os.Getenv("OPENAI_BASE_URL"),
		)
		if base == "" {
			base = "https://api.openai.com/v1"
		}
	}
	return strings.TrimRight(strings.TrimSpace(base), "/")
}

func looksLikeOpenRouterKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	lower := strings.ToLower(key)
	if strings.HasPrefix(lower, "sk-or-") {
		return true
	}
	return strings.HasPrefix(lower, "or-")
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

func LooksLikeOpenRouterKey(key string) bool {
	return looksLikeOpenRouterKey(key)
}
