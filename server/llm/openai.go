package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return "", errors.New("OPENAI_API_KEY missing")
	}
	if model == "" {
		model = strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
	}
	if model == "" {
		return "", errors.New("OPENAI_MODEL missing: set env or pass a value")
	}

	base := strings.TrimSpace(os.Getenv("OPENAI_API_BASE"))
	if base == "" {
		base = strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	}
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	base = strings.TrimRight(base, "/")

	headerName := strings.TrimSpace(os.Getenv("OPENAI_API_KEY_HEADER"))
	if headerName == "" {
		headerName = "Authorization"
	}
	prefix := os.Getenv("OPENAI_API_KEY_PREFIX")
	if headerName == "Authorization" && strings.TrimSpace(prefix) == "" {
		prefix = "Bearer "
	}
	org := strings.TrimSpace(os.Getenv("OPENAI_ORG"))

	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	if opts.MaxOutputTokens != nil && *opts.MaxOutputTokens > 0 {
		payload["max_tokens"] = *opts.MaxOutputTokens
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
	applyTuningFromEnv(payload)

	b, _ := json.Marshal(payload)
	url := base + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set(headerName, prefix+apiKey)
	if org != "" {
		req.Header.Set("OpenAI-Organization", org)
	}
	if v := strings.TrimSpace(os.Getenv("OPENROUTER_SITE_URL")); v != "" {
		req.Header.Set("HTTP-Referer", v)
		req.Header.Set("Referer", v)
	}
	if v := strings.TrimSpace(os.Getenv("OPENROUTER_TITLE")); v != "" {
		req.Header.Set("X-Title", v)
	}

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	body := buf.Bytes()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("openai http %d: %s", resp.StatusCode, truncate(string(body), 800))
	}

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

func applyTuningFromEnv(m map[string]any) {
	if v := strings.TrimSpace(os.Getenv("OPENAI_TEMPERATURE")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			m["temperature"] = f
		}
	}
	if v := strings.TrimSpace(os.Getenv("OPENAI_TOP_P")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			m["top_p"] = f
		}
	}
	if v := strings.TrimSpace(os.Getenv("OPENAI_TOP_K")); v != "" {
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
	if v := strings.TrimSpace(os.Getenv("OPENAI_REASONING_EFFORT")); v != "" {
		opts.ReasoningEffort = v
	}
	if v := strings.TrimSpace(os.Getenv("OPENAI_MAX_OUTPUT_TOKENS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			opts.MaxOutputTokens = &n
		}
	}
	return opts
}
