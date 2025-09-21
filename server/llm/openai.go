package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"time"
)

// PingOptions controls JSON mode + reasoning + tokens.
type PingOptions struct {
	ReasoningEffort      string
	MaxOutputTokens      *int
	StructuredSchemaName string
	StructuredSchema     map[string]any
	StructuredStrict     bool
}

// PingText sends a minimal request to the chat/completions API and returns text.
func PingText(ctx context.Context, model, system, user string) (string, error) {
	return PingTextWithOpts(ctx, model, system, user, envPingOptions())
}

// PingTextWithOpts lets you pass custom knobs (used by PingText via env).
func PingTextWithOpts(ctx context.Context, model, system, user string, opts PingOptions) (string, error) {
	cfg, err := resolveAPIConfig(model)
	if err != nil {
		return "", err
	}

	payload := map[string]any{
		"model": cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	if opts.MaxOutputTokens != nil && *opts.MaxOutputTokens > 0 {
		payload["max_tokens"] = *opts.MaxOutputTokens
	}
	if strings.TrimSpace(opts.ReasoningEffort) != "" {
		payload["reasoning"] = map[string]any{"effort": opts.ReasoningEffort}
	}
	if opts.StructuredSchema != nil {
		payload["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   coalesce(opts.StructuredSchemaName, "structured"),
				"strict": opts.StructuredStrict,
				"schema": opts.StructuredSchema,
			},
		}
	} else {
		payload["response_format"] = map[string]any{"type": "json_object"}
	}
	applyTuningFromEnv(payload, cfg.Kind == providerOpenRouter)

	client := &http.Client{Timeout: 45 * time.Second}
	url := cfg.BaseURL + "/chat/completions"
	removed := map[string]bool{}

	maxAttempts := 3
	if cfg.Kind == providerOpenRouter {
		removalCandidates := 0
		for _, key := range []string{"response_format", "reasoning", "max_tokens"} {
			if _, exists := payload[key]; exists {
				removalCandidates++
			}
		}
		if attemptBudget := 1 + removalCandidates; attemptBudget > maxAttempts {
			maxAttempts = attemptBudget
		}
	}

	for attempts := 0; attempts < maxAttempts; attempts++ {
		b, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set(cfg.HeaderName, cfg.HeaderPrefix+cfg.APIKey)
		if cfg.Organization != "" {
			req.Header.Set("OpenAI-Organization", cfg.Organization)
		}
		for k, v := range cfg.ExtraHeaders {
			setHeaderPreserveCase(req.Header, k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}

		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		body := buf.Bytes()
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			var cc struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			if err := json.Unmarshal(body, &cc); err != nil {
				return "", err
			}
			if len(cc.Choices) == 0 {
				return "", errors.New("no choices returned")
			}
			return cc.Choices[0].Message.Content, nil
		}

		if (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity) && adjustOpenRouterPayloadForRetry(payload, cfg.Kind, body, removed) {
			continue
		}

		return "", fmt.Errorf("openai http %d: %s", resp.StatusCode, truncate(string(body), 800))
	}

	return "", errors.New("exhausted chat completion retries")
}

// PingChooseAction requests a structured JSON action from the model.
func PingChooseAction(ctx context.Context, model, system, user string, legal []string, minTo, maxTo int, opts PingOptions) (string, *int, string, error) {
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        legal,
				"description": "One of the legal poker actions",
			},
			"amount": map[string]any{
				"type":        []any{"integer", "null"},
				"minimum":     minTo,
				"maximum":     maxTo,
				"description": "Raise-to amount when action is raise; otherwise null",
			},
		},
		"required": []string{"action"},
	}
	opts.StructuredSchema = schema
	opts.StructuredSchemaName = coalesce(opts.StructuredSchemaName, "poker_action")
	opts.StructuredStrict = true

	text, err := PingTextWithOpts(ctx, model, system, user, opts)
	if err != nil {
		return "", nil, text, err
	}

	raw := strings.TrimSpace(text)
	if raw == "" {
		return "", nil, raw, errors.New("empty response")
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		if cleaned := extractJSONObject(raw); cleaned != "" {
			if err2 := json.Unmarshal([]byte(cleaned), &parsed); err2 != nil {
				return "", nil, raw, err
			}
		} else {
			return "", nil, raw, err
		}
	}
	act, amt, ok := coerceActionMap(parsed, legal, minTo, maxTo)
	if !ok {
		return "", nil, raw, errors.New("no valid action in response")
	}
	return act, amt, raw, nil
}

func applyTuningFromEnv(m map[string]any, preferOpenRouter bool) {
	if v := envWithFallback(preferOpenRouter, "OPENAI_TEMPERATURE", "OPENROUTER_TEMPERATURE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			m["temperature"] = f
		}
	}
	if v := envWithFallback(preferOpenRouter, "OPENAI_TOP_P", "OPENROUTER_TOP_P"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			m["top_p"] = f
		}
	}
	if v := envWithFallback(preferOpenRouter, "OPENAI_TOP_K", "OPENROUTER_TOP_K"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			m["top_k"] = n
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func coalesce(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func extractJSONObject(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}
	end := strings.LastIndex(s, "}")
	if end < start {
		return ""
	}
	return strings.TrimSpace(s[start : end+1])
}

func coerceActionMap(parsed map[string]any, legal []string, minRaiseTo, maxRaiseTo int) (string, *int, bool) {
	var act string
	if v, ok := parsed["action"].(string); ok {
		act = strings.ToLower(strings.TrimSpace(v))
	}
	if act == "bet" {
		act = "raise"
	}
	valid := false
	for _, k := range legal {
		if k == act {
			valid = true
			break
		}
	}
	if !valid {
		return "", nil, false
	}

	var amount *int
	if rawAmt, ok := parsed["amount"]; ok && rawAmt != nil {
		switch t := rawAmt.(type) {
		case float64:
			v := int(t)
			amount = &v
		case json.Number:
			if n, err := t.Int64(); err == nil {
				v := int(n)
				amount = &v
			}
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
				v := n
				amount = &v
			}
		}
	}
	if act == "raise" {
		if amount == nil {
			v := minRaiseTo
			amount = &v
		}
		if *amount < minRaiseTo || *amount > maxRaiseTo {
			return "", nil, false
		}
	} else {
		amount = nil
	}
	return act, amount, true
}

func envPingOptions() PingOptions {
	opts := PingOptions{}
	preferOpenRouter := preferOpenRouterEnv()
	if v := envWithFallback(preferOpenRouter, "OPENAI_REASONING_EFFORT", "OPENROUTER_REASONING_EFFORT"); v != "" {
		opts.ReasoningEffort = v
	}
	if v := envWithFallback(preferOpenRouter, "OPENAI_MAX_OUTPUT_TOKENS", "OPENROUTER_MAX_OUTPUT_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			opts.MaxOutputTokens = &n
		}
	}
	return opts
}

func adjustOpenRouterPayloadForRetry(payload map[string]any, kind providerKind, body []byte, removed map[string]bool) bool {
	if kind != providerOpenRouter {
		return false
	}

	lowerMsg := strings.ToLower(string(body))
	lowerParam := ""
	var errBody struct {
		Error struct {
			Message string `json:"message"`
			Param   string `json:"param"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &errBody); err == nil {
		if strings.TrimSpace(errBody.Error.Message) != "" {
			lowerMsg = strings.ToLower(errBody.Error.Message)
		}
		lowerParam = strings.ToLower(errBody.Error.Param)
	}

	match := func(field string, aliases ...string) bool {
		if removed[field] {
			return false
		}
		if _, exists := payload[field]; !exists {
			return false
		}
		if lowerParam != "" && (lowerParam == field || strings.Contains(lowerParam, field)) {
			return true
		}
		if strings.Contains(lowerMsg, field) {
			return true
		}
		for _, alias := range aliases {
			if strings.Contains(lowerMsg, alias) {
				return true
			}
		}
		return false
	}

	if match("response_format", "json_schema", "structured outputs", "structured_output") {
		delete(payload, "response_format")
		removed["response_format"] = true
		return true
	}
	if match("reasoning", "internal reasoning", "reasoning_effort") {
		delete(payload, "reasoning")
		removed["reasoning"] = true
		return true
	}
	if match("max_tokens", "max_output_tokens") {
		delete(payload, "max_tokens")
		removed["max_tokens"] = true
		return true
	}

	return false
}

func envWithFallback(preferOpenRouter bool, openAIKey, openRouterKey string) string {
	keys := []string{openAIKey, openRouterKey}
	if preferOpenRouter {
		keys[0], keys[1] = keys[1], keys[0]
	}
	for _, key := range keys {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

func setHeaderPreserveCase(h http.Header, key, value string) {
	k := strings.TrimSpace(key)
	v := strings.TrimSpace(value)
	if k == "" || v == "" {
		return
	}
	canonical := textproto.CanonicalMIMEHeaderKey(k)
	if canonical == k {
		h.Set(k, v)
		return
	}
	h.Del(canonical)
	h.Del(k)
	h[k] = []string{v}
}

func preferOpenRouterEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LLM_PROVIDER"))) {
	case "openrouter":
		return true
	case "openai":
		return false
	}
	if strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")) != "" && strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("OPENROUTER_API_BASE")) != "" || strings.TrimSpace(os.Getenv("OPENROUTER_BASE_URL")) != "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("OPENROUTER_MODEL")) != "" && strings.TrimSpace(os.Getenv("OPENAI_MODEL")) == "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("OPENROUTER_MODEL_A")) != "" && strings.TrimSpace(os.Getenv("OPENAI_MODEL_A")) == "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("OPENROUTER_MODEL_B")) != "" && strings.TrimSpace(os.Getenv("OPENAI_MODEL_B")) == "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("OPENROUTER_MODEL_SB")) != "" && strings.TrimSpace(os.Getenv("OPENAI_MODEL_SB")) == "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("OPENROUTER_MODEL_BB")) != "" && strings.TrimSpace(os.Getenv("OPENAI_MODEL_BB")) == "" {
		return true
	}
	if base := strings.TrimSpace(os.Getenv("OPENAI_API_BASE")); base != "" && strings.Contains(strings.ToLower(base), "openrouter") {
		return true
	}
	if base := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL")); base != "" && strings.Contains(strings.ToLower(base), "openrouter") {
		return true
	}
	return false
}
