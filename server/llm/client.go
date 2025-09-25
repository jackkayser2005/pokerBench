package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
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

// UsageStats captures token/cost metadata returned by the LLM provider.
type UsageStats struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	TotalCostMicros  int64
	CallCount        int
}

// Add merges another usage summary into the receiver.
func (u *UsageStats) Add(other UsageStats) {
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens += other.TotalTokens
	u.TotalCostMicros += other.TotalCostMicros
	u.CallCount += other.CallCount
}

// PingResult contains the primary text plus usage metadata from an LLM call.
type PingResult struct {
	Text  string
	Usage UsageStats
}

// PingText sends a minimal request to the chat/completions API and returns text.
func PingText(ctx context.Context, model, system, user string) (PingResult, error) {
	return PingTextWithOpts(ctx, model, system, user, envPingOptions())
}

// PingTextWithOpts lets you pass custom knobs (used by PingText via env).
func PingTextWithOpts(ctx context.Context, model, system, user string, opts PingOptions) (PingResult, error) {
	cfg, err := resolveAPIConfig(model)
	if err != nil {
		return PingResult{}, err
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
	applyTuningFromEnv(payload)

	client := &http.Client{Timeout: 45 * time.Second}
	url := cfg.BaseURL + "/chat/completions"
	removed := map[string]bool{}

	maxAttempts := 3
	removalCandidates := 0
	for _, key := range []string{"response_format", "reasoning", "max_tokens"} {
		if _, exists := payload[key]; exists {
			removalCandidates++
		}
	}
	if attemptBudget := 1 + removalCandidates; attemptBudget > maxAttempts {
		maxAttempts = attemptBudget
	}

	for attempts := 0; attempts < maxAttempts; attempts++ {
		b, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
		if err != nil {
			return PingResult{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set(cfg.HeaderName, cfg.HeaderPrefix+cfg.APIKey)
		for k, v := range cfg.ExtraHeaders {
			setHeaderPreserveCase(req.Header, k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			return PingResult{}, err
		}

		var buf bytes.Buffer
		if _, err := buf.ReadFrom(resp.Body); err != nil {
			return PingResult{}, fmt.Errorf("read chat response: %w", err)
		}
		body := buf.Bytes()
		if cerr := resp.Body.Close(); cerr != nil {
			log.Printf("llm: closing response body: %v", cerr)
		}

		usage := usageFromHeaders(resp.Header)
		usageFromBody := usageFromJSONBody(body)

		if usage.PromptTokens == 0 {
			usage.PromptTokens = usageFromBody.PromptTokens
		}
		if usage.CompletionTokens == 0 {
			usage.CompletionTokens = usageFromBody.CompletionTokens
		}
		if usage.TotalTokens == 0 {
			usage.TotalTokens = usageFromBody.TotalTokens
		}
		if usage.TotalCostMicros == 0 {
			usage.TotalCostMicros = usageFromBody.TotalCostMicros
		}
		if usage.CallCount == 0 {
			usage.CallCount = 1
		}
		result := PingResult{Usage: usage}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			var cc struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			if err := json.Unmarshal(body, &cc); err != nil {
				return result, err
			}
			if len(cc.Choices) == 0 {
				return result, errors.New("no choices returned")
			}
			result.Text = cc.Choices[0].Message.Content
			return result, nil
		}

		if (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity) && adjustOpenRouterPayloadForRetry(payload, body, removed) {
			continue
		}

		return result, fmt.Errorf("openrouter http %d: %s", resp.StatusCode, truncate(string(body), 800))
	}

	return PingResult{}, errors.New("exhausted chat completion retries")
}

// PingChooseAction requests a structured JSON action from the model.
func PingChooseAction(ctx context.Context, model, system, user string, legal []string, minTo, maxTo int, opts PingOptions) (string, *int, string, UsageStats, error) {
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

	totalUsage := UsageStats{}
	res, err := PingTextWithOpts(ctx, model, system, user, opts)
	totalUsage.Add(res.Usage)
	text := res.Text
	if err != nil {
		return "", nil, text, totalUsage, err
	}

	raw := strings.TrimSpace(text)
	if raw == "" {
		return "", nil, raw, totalUsage, errors.New("empty response")
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		if cleaned := extractJSONObject(raw); cleaned != "" {
			if err2 := json.Unmarshal([]byte(cleaned), &parsed); err2 != nil {
				return "", nil, raw, totalUsage, err
			}
		} else {
			return "", nil, raw, totalUsage, err
		}
	}
	act, amt, ok := coerceActionMap(parsed, legal, minTo, maxTo)
	if !ok {
		return "", nil, raw, totalUsage, errors.New("no valid action in response")
	}
	return act, amt, raw, totalUsage, nil
}

func applyTuningFromEnv(m map[string]any) {
	if v := strings.TrimSpace(os.Getenv("OPENROUTER_TEMPERATURE")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			m["temperature"] = f
		}
	}
	if v := strings.TrimSpace(os.Getenv("OPENROUTER_TOP_P")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			m["top_p"] = f
		}
	}
	if v := strings.TrimSpace(os.Getenv("OPENROUTER_TOP_K")); v != "" {
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
	if v := strings.TrimSpace(os.Getenv("OPENROUTER_REASONING_EFFORT")); v != "" {
		opts.ReasoningEffort = v
	}
	if v := strings.TrimSpace(os.Getenv("OPENROUTER_MAX_OUTPUT_TOKENS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			opts.MaxOutputTokens = &n
		}
	}
	return opts
}

func adjustOpenRouterPayloadForRetry(payload map[string]any, body []byte, removed map[string]bool) bool {
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

func usageFromHeaders(h http.Header) UsageStats {
	usage := UsageStats{}
	parseIntHeader := func(keys ...string) int {
		for _, key := range keys {
			if val := strings.TrimSpace(h.Get(key)); val != "" {
				if n, err := strconv.Atoi(val); err == nil {
					return n
				}
				if f, err := strconv.ParseFloat(val, 64); err == nil {
					return int(math.Round(f))
				}
			}
		}
		return 0
	}

	usage.PromptTokens = parseIntHeader(
		"X-Openrouter-Usage-Prompt-Tokens",
		"X-Openrouter-Prompt-Tokens",
		"X-Prompt-Tokens",
	)
	usage.CompletionTokens = parseIntHeader(
		"X-Openrouter-Usage-Completion-Tokens",
		"X-Openrouter-Completion-Tokens",
		"X-Completion-Tokens",
	)
	usage.TotalTokens = parseIntHeader(
		"X-Openrouter-Usage-Total-Tokens",
		"X-Openrouter-Total-Tokens",
		"X-Total-Tokens",
	)

	parseCostHeader := func(keys ...string) int64 {
		for _, key := range keys {
			if val := strings.TrimSpace(h.Get(key)); val != "" {
				if micros := costStringToMicros(val); micros != 0 {
					return micros
				}
			}
		}
		return 0
	}
	usage.TotalCostMicros = parseCostHeader(
		"X-Openrouter-Usage-Total-Cost",
		"X-Openrouter-Total-Cost",
		"X-Openrouter-Total-Cost-Usd",
		"X-Total-Cost",
	)
	return usage
}

func usageFromJSONBody(body []byte) UsageStats {
	type wrap struct {
		Usage map[string]any `json:"usage"`
	}
	var w wrap
	if err := json.Unmarshal(body, &w); err != nil {
		return UsageStats{}
	}
	if len(w.Usage) == 0 {
		return UsageStats{}
	}
	usage := UsageStats{}
	usage.PromptTokens = intFromAny(w.Usage["prompt_tokens"])
	usage.CompletionTokens = intFromAny(w.Usage["completion_tokens"])
	usage.TotalTokens = intFromAny(w.Usage["total_tokens"])
	if usage.TotalTokens == 0 {
		usage.TotalTokens = intFromAny(w.Usage["tokens"])
	}
	usage.TotalCostMicros = costFromAny(
		w.Usage["total_cost"],
		w.Usage["total_cost_usd"],
		w.Usage["cost"],
	)
	return usage
}

func intFromAny(v any) int {
	switch t := v.(type) {
	case nil:
		return 0
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(math.Round(t))
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return int(n)
		}
		if f, err := t.Float64(); err == nil {
			return int(math.Round(f))
		}
	case string:
		trimmed := strings.TrimSpace(t)
		if trimmed == "" {
			return 0
		}
		if n, err := strconv.Atoi(trimmed); err == nil {
			return n
		}
		if f, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return int(math.Round(f))
		}
	}
	return 0
}

func costFromAny(values ...any) int64 {
	for _, v := range values {
		switch t := v.(type) {
		case nil:
			continue
		case float64:
			if t == 0 {
				continue
			}
			return int64(math.Round(t * 1_000_000))
		case json.Number:
			if f, err := t.Float64(); err == nil {
				if f == 0 {
					continue
				}
				return int64(math.Round(f * 1_000_000))
			}
		case int:
			if t == 0 {
				continue
			}
			return int64(t) * 1_000_000
		case int64:
			if t == 0 {
				continue
			}
			return t * 1_000_000
		case string:
			if micros := costStringToMicros(t); micros != 0 {
				return micros
			}
		}
	}
	return 0
}

func costStringToMicros(s string) int64 {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0
	}
	// Remove leading currency symbol if present.
	trimmed = strings.TrimPrefix(trimmed, "$")
	if strings.HasSuffix(trimmed, "usd") {
		trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, "usd"))
	}
	if f, err := strconv.ParseFloat(trimmed, 64); err == nil {
		if f == 0 {
			return 0
		}
		return int64(math.Round(f * 1_000_000))
	}
	return 0
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
