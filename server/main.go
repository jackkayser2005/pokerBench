package main

import (
	"ai-thunderdome/server/agent"
	"ai-thunderdome/server/engine"
	"ai-thunderdome/server/judge"
	"ai-thunderdome/server/llm"
	"ai-thunderdome/server/store"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	mrand "math/rand"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/joho/godotenv"
)

//
// ===== pretty printing =====
//

var useColor bool
var debugState bool

const (
	colReset  = "\033[0m"
	colBold   = "\033[1m"
	colDim    = "\033[2m"
	colGreen  = "\033[32m"
	colRed    = "\033[31m"
	colYellow = "\033[33m"
	colBlue   = "\033[34m"
	colMag    = "\033[35m"
	colCyan   = "\033[36m"
)
const benchSystem = `
You are an objective poker engine playing heads-up no-limit Texas Hold'em.

Fundamental directives:
- Base every action on quantified equity, position, stack-to-pot ratio, and blocker effects.
- Keep language clinical; reason about ranges and EV without narrative or emotion.
- When to_call == 0 and raising is legal, mix probing/value raises with checks so your strategy stays balanced.
- When to_call > 0, select among call, raise, or fold only; never output an illegal check.

Sizing policy:
- Evaluate the full legal raise interval [min_to, max_to]; choose an integer raise_to that supports your line.
- Larger raises suit polarized value/bluff combinations; smaller sizes protect capped ranges or thin value.
- You may use the maximum legal size when EV calculations justify it.

Output format:
- Return exactly one option from legal_actions.
- For raises, include raise_to with a single integer inside [min_to, max_to]; otherwise omit an amount.
- Do not add commentary or explanations.
`

func c(code, s string) string {
	if !useColor {
		return s
	}
	return code + s + colReset
}
func bold(s string) string { return c(colBold, s) }
func dim(s string) string  { return c(colDim, s) }
func good(s string) string { return c(colGreen, s) }
func warn(s string) string { return c(colYellow, s) }
func bad(s string) string  { return c(colRed, s) }
func cyan(s string) string { return c(colCyan, s) }
func mag(s string) string  { return c(colMag, s) }
func blue(s string) string { return c(colBlue, s) }
func seatTag(seat engine.Seat) string {
	if seat == engine.SB {
		return cyan("SB")
	}
	return warn("BB")
}
func modelShort(m string) string {
	m = strings.TrimSpace(m)
	if len(m) <= 28 {
		return m
	}
	return m[:28]
}
func potTag(pot int) string { return dim(fmt.Sprintf("Pot=%d", pot)) }
func section(title string)  { fmt.Printf("\n%s %s %s\n", dim("──"), bold(title), dim("──")) }
func sub(title string)      { fmt.Printf("%s %s\n", dim("•"), bold(title)) }

//
// ===== bootstrap =====
//

// Tries: env var file, ./secrets/openai_api_key.txt, ./server/openai_api_key.txt,
// ./openai_api_key.txt, /app/server/openai_api_key.txt (in container), and /run/secrets/openai_api_key.
func loadAPIKeyFromSecret() {
	if os.Getenv("OPENAI_API_KEY") != "" {
		return
	}
	var candidates []string
	if p := os.Getenv("OPENAI_API_KEY_FILE"); strings.TrimSpace(p) != "" {
		candidates = append(candidates, p)
	}
	candidates = append(candidates,
		"./secrets/openai_api_key.txt",
		"server/openai_api_key.txt",
		"./server/openai_api_key.txt",
		"./openai_api_key.txt",
		"/app/server/openai_api_key.txt",
		"/run/secrets/openai_api_key",
	)
	for _, path := range candidates {
		if b, err := os.ReadFile(path); err == nil {
			key := strings.TrimSpace(string(b))
			if key != "" {
				os.Setenv("OPENAI_API_KEY", key)
				return
			}
		}
	}
}

func mustEnv(keys ...string) {
	for _, k := range keys {
		if os.Getenv(k) == "" {
			log.Fatalf("Missing required env var %s. Put it in .env (dev) or set it on the host (prod).", k)
		}
	}
}
func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
func atoiDef(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
func asBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

var stopFlag atomic.Bool

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	_ = godotenv.Load()

	// Load API key from a file if present (before mustEnv)
	loadAPIKeyFromSecret()

	useColor = (os.Getenv("NO_COLOR") == "") && (strings.TrimSpace(os.Getenv("USE_COLOR")) != "0")
	debugState = asBool(os.Getenv("DEBUG"))

	var migrate, duel bool
	var duelMatrix bool
	for _, a := range os.Args[1:] {
		switch a {
		case "--migrate":
			migrate = true
		case "--duel":
			duel = true
		case "--duel-matrix":
			duelMatrix = true
		}
	}

	// Only require the key when not doing a pure DB migrate
	if !migrate {
		mustEnv("OPENAI_API_KEY")
	}

	gracefulOnly := !asBool(os.Getenv("STOP_IMMEDIATE"))
	maxSeconds := atoiDef(os.Getenv("MAX_SECONDS"), 0)
	stopFile := os.Getenv("STOP_FILE")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchSignals(cancel)

	var deadline time.Time
	if maxSeconds > 0 {
		deadline = time.Now().Add(time.Duration(maxSeconds) * time.Second)
	}
	checkStop := func(allowImmediate bool) bool {
		select {
		case <-ctx.Done():
			stopFlag.Store(true)
		default:
		}
		if stopFlag.Load() {
			return true
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			stopFlag.Store(true)
			return true
		}
		if stopFile != "" {
			if _, err := os.Stat(stopFile); err == nil {
				stopFlag.Store(true)
				return true
			}
		}
		return false
	}

	if duel || duelMatrix {
		var db *store.DB
		if dsn := getenv("DATABASE_URL", ""); dsn != "" {
			p, err := store.Open(dsn)
			if err != nil {
				log.Printf("DB disabled (open failed): %v", err)
			} else {
				db = p
				defer db.Close(context.Background())
				if asBool(os.Getenv("AUTO_MIGRATE")) {
					if err := store.Migrate(context.Background(), db); err != nil {
						log.Printf("migrate failed (continuing without DB): %v", err)
						db = nil
					}
				}
			}
		}
		if duelMatrix {
			runDuelMatrix(checkStop, gracefulOnly, db)
		} else {
			runDuel(checkStop, gracefulOnly, db)
		}
		return
	}

	// server (optional)
	mustEnv("DATABASE_URL")
	dsn := getenv("DATABASE_URL", "postgres://poker:poker@localhost:5432/thunderdome?sslmode=disable")
	port := getenv("PORT", "8080")

	db, err := store.Open(dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close(context.Background())

	// Optional env-based auto-migrate for server mode
	if asBool(os.Getenv("AUTO_MIGRATE")) {
		if err := store.Migrate(context.Background(), db); err != nil {
			log.Fatal(err)
		}
		log.Println("migrated")
	}

	if migrate {
		if err := store.Migrate(context.Background(), db); err != nil {
			log.Fatal(err)
		}
		log.Println("migrated")
		return
	}

	r := Router(db)
	srv := &http.Server{Addr: ":" + port, Handler: r, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second}
	log.Printf("listening on http://localhost:%s (Ctrl+C to stop)", port)
	log.Fatal(srv.ListenAndServe())
}

func watchSignals(cancel context.CancelFunc) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
	stopFlag.Store(true)
	cancel()
}

//
// ===== players & stats =====
//

type Player struct {
	Label string
	Name  string
	Model string
	Bank  int
	Wins  int
}

func loadPlayers(startStack int) (a, b Player) {
	ma := os.Getenv("OPENAI_MODEL_A")
	mb := os.Getenv("OPENAI_MODEL_B")
	if ma == "" || mb == "" {
		ma = getenv("OPENAI_MODEL_SB", getenv("OPENAI_MODEL", ""))
		mb = getenv("OPENAI_MODEL_BB", getenv("OPENAI_MODEL", ""))
	}
	if ma == "" || mb == "" {
		log.Fatal("Provide OPENAI_MODEL_A and OPENAI_MODEL_B (or OPENAI_MODEL_SB/OPENAI_MODEL_BB)")
	}
	a = Player{Label: "A", Name: "A", Model: ma, Bank: startStack}
	b = Player{Label: "B", Name: "B", Model: mb, Bank: startStack}
	return
}

//
// ===== randomness =====
//

type seedStream struct{ state uint64 }

func newSeedStream(base uint64) seedStream { return seedStream{state: base} }
func (s *seedStream) next() uint64 {
	s.state += 0x9E3779B97F4A7C15
	z := s.state
	z ^= z >> 30
	z *= 0xBF58476D1CE4E5B9
	z ^= z >> 27
	z *= 0x94D049BB133111EB
	z ^= z >> 31
	return z
}
func secureBaseSeed() uint64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return binary.LittleEndian.Uint64(b[:]) ^ uint64(time.Now().UnixNano()) ^ uint64(os.Getpid())
	}
	return uint64(time.Now().UnixNano()) ^ 0xA5A5A5A5A5A5A5A5
}
func deckSeedFromEnvOrCrypto() uint64 {
	if s := os.Getenv("DECK_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return uint64(v)
		}
	}
	return secureBaseSeed()
}

//
// ===== LLM call =====
//

func askAction(ctx context.Context, model string, legal []string, minRaiseTo, maxRaiseTo int, obs agent.Observation) (string, *int, error) {
	obsRaw, _ := json.Marshal(obs)
	// Probe hint line (toggle with ENCOURAGE_PROBE_ZERO=1). Default is to encourage mixing checks.
	probeEnv := strings.TrimSpace(os.Getenv("ENCOURAGE_PROBE_ZERO"))
	probeLine := "- Mix checks with occasional probe raises; do not always raise when to_call is 0."
	if probeEnv == "1" || strings.EqualFold(probeEnv, "true") || strings.EqualFold(probeEnv, "yes") {
		probeLine = "- If to_call is 0 and raise is legal, you may consider a small probe (min raise) sometimes, but do not overuse it."
	}
	user := fmt.Sprintf(
		`Given this observation JSON:
%s

Respond ONLY with a single compact JSON object:
{"action":"%s","amount":null|<integer>}
Rules:
- Allowed actions are exactly %v (nothing else).
- If action is "raise" or "bet", set "amount" to an integer between %d and %d (inclusive).
- If action is "fold", "call", or "check", use null for "amount".
- No extra keys. No prose. No markdown.
- Size raises intentionally: lean larger when extracting strong value and mix in smaller sizes for probes or thin value; keep amounts inside [%d, %d].
%s
- Do not be afraid to raise or fold; avoid extreme passivity or aggression.`,
		string(obsRaw),
		strings.Join(legal, `"|"`),
		legal, minRaiseTo, maxRaiseTo, minRaiseTo, maxRaiseTo,
		probeLine,
	)
	ctx2, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()

	// 1) Prefer tool/function calling first to force enum
	var maxTok *int
	if v := strings.TrimSpace(os.Getenv("OPENAI_MAX_OUTPUT_TOKENS")); v != "" {
		lc := strings.ToLower(v)
		if lc == "omit" || lc == "auto" {
			maxTok = nil
		} else if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxTok = &n
		}
	}
	useToolsEnv := strings.TrimSpace(os.Getenv("USE_TOOLS"))
	useTools := true
	if useToolsEnv != "" && !(useToolsEnv == "1" || strings.EqualFold(useToolsEnv, "true") || strings.EqualFold(useToolsEnv, "yes")) {
		useTools = false
	}
	if useTools {
		toolSystem := benchSystem + "\n\nYou are a poker agent. Your only job is to call the function \"pick_action\". Do not output anything else. Never explain or justify your choice. Always pick exactly one action from the provided list."
		act, amt, raw, err := llm.PingChooseAction(ctx2, model, toolSystem, user, legal, minRaiseTo, maxRaiseTo, llm.PingOptions{MaxOutputTokens: maxTok})
		if debugState {
			if raw != "" {
				log.Printf("tool args raw: %s", raw)
			}
		}
		if err == nil {
			// Normalize and validate
			act = strings.ToLower(strings.TrimSpace(act))
			if act == "bet" {
				act = "raise"
			}
			valid := false
			for _, a := range legal {
				if act == a {
					valid = true
					break
				}
			}
			if !valid {
				return "", nil, fmt.Errorf("illegal action %q not in %v", act, legal)
			}
			if act == "raise" {
				if amt == nil {
					return "", nil, fmt.Errorf("raise requires amount")
				}
				if *amt < minRaiseTo || *amt > maxRaiseTo {
					return "", nil, fmt.Errorf("amount %d outside [%d,%d]", *amt, minRaiseTo, maxRaiseTo)
				}
			} else {
				amt = nil
			}
			// Optional probe policy: flip check→min-raise with probability when to_call==0
			act, amt = applyZeroProbePolicy(act, amt, legal, minRaiseTo, obs.ToCall)
			return act, amt, nil
		}
		if debugState {
			log.Printf("tool-call fallback due to: %v", err)
		}
	}

	// 2) Fallback to structured outputs JSON schema
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": legal, "description": "One of the legal poker actions"},
			// amount is optional; we validate in code conditionally
			"amount": map[string]any{"type": []any{"integer", "null"}, "description": "Raise-to amount when action is raise; otherwise null", "minimum": minRaiseTo, "maximum": maxRaiseTo},
		},
		"required":             []string{"action"},
		"additionalProperties": false,
	}
	// Reasoning effort env (validate)
	re := strings.ToLower(strings.TrimSpace(os.Getenv("OPENAI_REASONING_EFFORT")))
	switch re {
	case "", "low", "medium", "high":
	default:
		re = ""
	}
	jsonSystem := benchSystem + "\n\nRespond ONLY with a minimal JSON object as specified. No prose, no markdown."
	text, err := llm.PingTextWithOpts(ctx2, model, jsonSystem, user, llm.PingOptions{ReasoningEffort: re, MaxOutputTokens: maxTok, StructuredSchemaName: "poker_action", StructuredSchema: schema, StructuredStrict: true})
	if debugState && text != "" {
		log.Printf("json raw: %s", text)
	}
	if err == nil {
		// tolerant parsing
		parsed := map[string]any{}
		if e := json.Unmarshal([]byte(text), &parsed); e != nil {
			if cleaned := extractJSONObject(text); cleaned != "" {
				if e2 := json.Unmarshal([]byte(cleaned), &parsed); e2 != nil {
					return "", nil, fmt.Errorf("bad JSON from model: %v\nraw=%s", e, text)
				}
			} else {
				return "", nil, fmt.Errorf("bad JSON from model: %v\nraw=%s", e, text)
			}
		}
		// coerce
		var act string
		if v, ok := parsed["action"].(string); ok {
			act = strings.ToLower(strings.TrimSpace(v))
		}
		if act == "bet" {
			act = "raise"
		}
		valid := false
		for _, a := range legal {
			if act == a {
				valid = true
				break
			}
		}
		if !valid {
			return "", nil, fmt.Errorf("illegal action %q not in %v", act, legal)
		}
		var amount *int
		if rawAmt, ok := parsed["amount"]; ok && rawAmt != nil {
			switch t := rawAmt.(type) {
			case float64:
				v := int(t)
				amount = &v
			case string:
				if n, e := strconv.Atoi(strings.TrimSpace(t)); e == nil {
					v := n
					amount = &v
				}
			case json.Number:
				if n, e := t.Int64(); e == nil {
					v := int(n)
					amount = &v
				}
			}
		}
		if act == "raise" {
			if amount == nil {
				return "", nil, fmt.Errorf("raise requires amount")
			}
			if *amount < minRaiseTo || *amount > maxRaiseTo {
				return "", nil, fmt.Errorf("amount %d outside [%d,%d]", *amount, minRaiseTo, maxRaiseTo)
			}
		} else {
			amount = nil
		}
		act, amount = applyZeroProbePolicy(act, amount, legal, minRaiseTo, obs.ToCall)
		return act, amount, nil
	}

	// 3) Fallback to legacy JSON mode (no schema)
	text2, err2 := llm.PingText(ctx2, model, jsonSystem, user)
	if debugState && text2 != "" {
		log.Printf("json(raw-object) raw: %s", text2)
	}
	if err2 == nil {
		// Try: JSON -> code-fence JSON -> YAML-ish -> NL heuristics
		// 3a) JSON
		parsed := map[string]any{}
		if e := json.Unmarshal([]byte(text2), &parsed); e == nil {
			if act, amount, ok := coerceActionMap(parsed, legal, minRaiseTo, maxRaiseTo, obs.ToCall); ok {
				act, amount = applyZeroProbePolicy(act, amount, legal, minRaiseTo, obs.ToCall)
				return act, amount, nil
			}
		}
		// 3b) code-fence JSON
		if cleaned := extractJSONObject(text2); cleaned != "" {
			parsed := map[string]any{}
			if e2 := json.Unmarshal([]byte(cleaned), &parsed); e2 == nil {
				if act, amount, ok := coerceActionMap(parsed, legal, minRaiseTo, maxRaiseTo, obs.ToCall); ok {
					act, amount = applyZeroProbePolicy(act, amount, legal, minRaiseTo, obs.ToCall)
					return act, amount, nil
				}
			}
		}
		// 3c) YAML fallback
		if act, amount, ok := parseYAMLish(text2, legal, minRaiseTo, maxRaiseTo, obs.ToCall); ok {
			act, amount = applyZeroProbePolicy(act, amount, legal, minRaiseTo, obs.ToCall)
			return act, amount, nil
		}
		// 3d) Natural language fallback
		if act, amount, ok := parseNLAction(text2, legal, minRaiseTo, maxRaiseTo, obs.ToCall); ok {
			act, amount = applyZeroProbePolicy(act, amount, legal, minRaiseTo, obs.ToCall)
			return act, amount, nil
		}
		// 3e) Last-ditch safe default
		if obs.ToCall == 0 && contains(legal, "check") {
			return "check", nil, nil
		}
		if contains(legal, "fold") {
			return "fold", nil, nil
		}
	}
	// If we couldn't salvage anything, propagate the earlier error if present
	if err2 != nil {
		return "", nil, err2
	}
	return "", nil, fmt.Errorf("could not derive a legal action from model output")
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func applyZeroProbePolicy(act string, amt *int, legal []string, minRaiseTo int, toCall int) (string, *int) {
	prob := probeProbFromEnv()
	if toCall != 0 {
		return act, amt
	}
	if contains(legal, "check") && act == "raise" {
		if prob <= 0 {
			return "check", nil
		}
		if mrand.Float64() >= prob {
			return "check", nil
		}
		if amt == nil {
			v := minRaiseTo
			amt = &v
		}
	}
	if contains(legal, "raise") && act == "check" {
		if prob > 0 && mrand.Float64() < prob {
			if amt == nil {
				v := minRaiseTo
				amt = &v
			}
			return "raise", amt
		}
	}
	return act, amt
}

// RAISE_ZERO_CALL_PROB (0..1) optionally flips a model "check" into a min-raise when to_call==0
func probeProbFromEnv() float64 {
	v := strings.TrimSpace(os.Getenv("RAISE_ZERO_CALL_PROB"))
	if v == "" {
		return 0.35
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		if f < 0 {
			return 0
		}
		if f > 1 {
			return 1
		}
		return f
	}
	return 0.35
}

//
// ===== lightweight hand description =====
//

func describe(hole [2]string, board []string) string {
	all := append([]string{}, hole[0], hole[1])
	all = append(all, board...)
	ranks := map[int]int{}
	suits := map[byte]int{}
	uniq := map[int]bool{}
	for _, c := range all {
		r, s := parseCard(c)
		ranks[r]++
		suits[s]++
		uniq[r] = true
	}
	var pairs []int
	trips := -1
	quads := -1
	for r, cnt := range ranks {
		switch cnt {
		case 4:
			quads = r
		case 3:
			trips = r
		case 2:
			pairs = append(pairs, r)
		}
	}
	sort.Ints(pairs)
	flushCnt := 0
	for _, c := range suits {
		if c > flushCnt {
			flushCnt = c
		}
	}
	st := straightInfo(uniq)
	switch {
	case quads != -1:
		return fmt.Sprintf("quads %s", rankName(quads))
	case trips != -1 && len(pairs) > 0:
		return fmt.Sprintf("full house %s over %s", rankName(trips), rankName(pairs[len(pairs)-1]))
	case flushCnt >= 5:
		return "flush"
	case st.made:
		return "straight"
	case trips != -1:
		return fmt.Sprintf("trips %s", rankName(trips))
	case len(pairs) >= 2:
		return fmt.Sprintf("two pair %s & %s", rankName(pairs[len(pairs)-1]), rankName(pairs[len(pairs)-2]))
	case len(pairs) == 1:
		if len(board) >= 3 {
			top := topRankOnBoard(board)
			if pairs[0] == top {
				return "top pair"
			}
		}
		return fmt.Sprintf("pair of %s", rankName(pairs[0]))
	default:
		return fmt.Sprintf("high card %s", rankName(holeHigh(hole)))
	}
}

type straightState struct{ made, openEnd, gutshot bool }

func straightInfo(uniq map[int]bool) straightState {
	vals := []int{}
	for r := range uniq {
		vals = append(vals, r)
		if r == 14 {
			vals = append(vals, 1)
		}
	}
	sort.Ints(vals)
	vals = uniqueInts(vals)
	run, best := 1, 1
	gap1 := false
	for i := 1; i < len(vals); i++ {
		d := vals[i] - vals[i-1]
		if d == 1 {
			run++
			if run > best {
				best = run
			}
		} else if d == 2 {
			gap1 = true
			run = 1
		} else if d != 0 {
			run = 1
		}
	}
	made := best >= 5
	open := !made && hasConsecutive(vals, 4)
	gut := !made && (gap1 || hasGutshot(vals))
	return straightState{made: made, openEnd: open, gutshot: gut}
}
func hasConsecutive(vals []int, need int) bool {
	run := 1
	for i := 1; i < len(vals); i++ {
		if vals[i]-vals[i-1] == 1 {
			run++
			if run >= need {
				return true
			}
		} else if vals[i] != vals[i-1] {
			run = 1
		}
	}
	return false
}
func hasGutshot(vals []int) bool {
	for i := 0; i+3 < len(vals); i++ {
		a, b, c, d := vals[i], vals[i+1], vals[i+2], vals[i+3]
		if b == a+1 && c == a+3 && d == a+4 {
			return true
		}
	}
	return false
}
func uniqueInts(in []int) []int {
	if len(in) == 0 {
		return in
	}
	out := []int{in[0]}
	for i := 1; i < len(in); i++ {
		if in[i] != in[i-1] {
			out = append(out, in[i])
		}
	}
	return out
}
func parseCard(s string) (rank int, suit byte) {
	if len(s) < 2 {
		return 0, 'x'
	}
	r := s[0]
	switch r {
	case 'A':
		rank = 14
	case 'K':
		rank = 13
	case 'Q':
		rank = 12
	case 'J':
		rank = 11
	case 'T':
		rank = 10
	default:
		rank = int(r - '0')
	}
	suit = s[len(s)-1]
	return
}
func rankName(v int) string {
	switch v {
	case 14:
		return "A"
	case 13:
		return "K"
	case 12:
		return "Q"
	case 11:
		return "J"
	case 10:
		return "T"
	default:
		return fmt.Sprintf("%d", v)
	}
}
func holeHigh(hole [2]string) int {
	r1, _ := parseCard(hole[0])
	r2, _ := parseCard(hole[1])
	if r1 > r2 {
		return r1
	}
	return r2
}
func topRankOnBoard(board []string) int {
	best := 0
	for _, b := range board {
		r, _ := parseCard(b)
		if r > best {
			best = r
		}
	}
	return best
}

//
// ===== action tallies (per player) =====
//

type ActionTally struct {
	Check int
	Call  int
	Raise int
	Fold  int
}

func addAction(t map[string]*ActionTally, label, act string) {
	if t[label] == nil {
		t[label] = &ActionTally{}
	}
	switch act {
	case "check":
		t[label].Check++
	case "call":
		t[label].Call++
	case "raise":
		t[label].Raise++
	case "fold":
		t[label].Fold++
	}
}

//
// ===== hand runner =====
//

type contrib struct {
	total int
	stre  int
}

func actionStrings(h *engine.Hand) []string {
	kinds := h.Legal()
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		switch k {
		case engine.Fold:
			out = append(out, "fold")
		case engine.Call:
			out = append(out, "call")
		case engine.Check:
			out = append(out, "check")
		case engine.Raise:
			out = append(out, "raise")
		}
	}
	// Optional nudge: when to_call == 0 and both check and raise are legal,
	// place "raise" before "check" to counter models defaulting to the first enum.
	// Enable by setting RAISE_FIRST_ZERO_CALL=1 (or true/yes).
	if len(out) >= 2 {
		committed := 0
		if h.ToAct == engine.SB {
			committed = h.SB.Committed
		} else {
			committed = h.BB.Committed
		}
		toCall := h.CurBet - committed
		if toCall == 0 {
			rf := strings.TrimSpace(os.Getenv("RAISE_FIRST_ZERO_CALL"))
			if rf == "1" || strings.EqualFold(rf, "true") || strings.EqualFold(rf, "yes") {
				idxCheck, idxRaise := -1, -1
				for i, a := range out {
					if a == "check" {
						idxCheck = i
					}
					if a == "raise" {
						idxRaise = i
					}
				}
				if idxCheck >= 0 && idxRaise >= 0 && idxRaise > idxCheck {
					out[idxCheck], out[idxRaise] = out[idxRaise], out[idxCheck]
				}
			}
		}
	}
	return out
}

// Runs a single hand and returns: winner seat, pot, deltaSB, deltaBB, aborted.
func playHandMatch(
	ctx context.Context,
	h *engine.Hand,
	sbP, bbP *Player,
	checkStop func(allowImmediate bool) bool,
	gracefulOnly bool,
	tallies map[string]*ActionTally, // keyed by "A" / "B"
	db *store.DB, matchID int64, pairIndex int,
) (engine.Seat, int, int, int, bool) {
	section(fmt.Sprintf("Hand %s", blue(h.ID)))

	// Header
	fmt.Printf("%s %s  %s %s  %s\n",
		bold("Seats:"),
		fmt.Sprintf("%s(%s)", seatTag(engine.SB), dim(modelShort(sbP.Model))),
		bold("vs"),
		fmt.Sprintf("%s(%s)", seatTag(engine.BB), dim(modelShort(bbP.Model))),
		fmt.Sprintf(" | %s %d  %s %d", cyan("SB"), sbP.Bank, warn("BB"), bbP.Bank),
	)
	fmt.Printf("%s %s %s  | %s %s\n",
		bold("Holes:"),
		seatTag(engine.SB), fmt.Sprintf("%s %s", h.SB.Hole[0], h.SB.Hole[1]),
		seatTag(engine.BB), fmt.Sprintf("%s %s", h.BB.Hole[0], h.BB.Hole[1]),
	)
	fmt.Printf("%s %s:%d %s:%d  | %s\n\n",
		bold("Blinds:"),
		"SB", h.Cfg.SB, "BB", h.Cfg.BB, potTag(h.Pot),
	)

	// Track start banks so we can return deltas later
	startSB := sbP.Bank
	startBB := bbP.Bank

	// Contributions
	sbC := contrib{total: h.Cfg.SB, stre: h.Cfg.SB}
	bbC := contrib{total: h.Cfg.BB, stre: h.Cfg.BB}

	streets := []string{"preflop", "flop", "turn", "river"}
	var winner engine.Seat

	for i, s := range streets {
		sub(strings.ToUpper(s))

		// deal street & reset street contributions
		if i > 0 {
			h.NextStreet()
			sbC.stre, bbC.stre = 0, 0
			switch s {
			case "flop":
				fmt.Printf("%s %s %s %s\n", bold("Board:"), h.Board[0], h.Board[1], h.Board[2])
			case "turn":
				fmt.Printf("%s %s %s %s %s\n", bold("Board:"), h.Board[0], h.Board[1], h.Board[2], h.Board[3])
			case "river":
				fmt.Printf("%s %s %s %s %s %s\n", bold("Board:"), h.Board[0], h.Board[1], h.Board[2], h.Board[3], h.Board[4])
			}
		}

		// action seat (SB preflop; BB postflop)
		seat := engine.SB
		if s != "preflop" {
			seat = engine.BB
		}

		prevWasCheck := false
		const maxActionsPerStreet = 20

		for j := 0; j < maxActionsPerStreet; j++ {
			// termination between actions
			if checkStop(false) && !gracefulOnly {
				fmt.Println(bad("** Termination requested (immediate). Aborting hand without payout. **"))
				return engine.Seat(""), sbC.total + bbC.total, 0, 0, true
			}

			// observation + legal
			obs := agent.BuildObservation(h, seat)
			legal := actionStrings(h)

			// bounds for raises
			actor := h.SB
			curLabel := sbP.Label
			curModel := sbP.Model
			if seat == engine.BB {
				actor = h.BB
				curLabel = bbP.Label
				curModel = bbP.Model
			}
			minTo := h.CurBet + h.MinRaise
			if minTo < h.Cfg.BB { // preflop guard
				minTo = h.Cfg.BB
			}
			maxTo := actor.Committed + actor.Stack

			// cancel model call if hard stop flips during wait
			textCtx, cancel := context.WithCancel(context.Background())
			go func() {
				for {
					select {
					case <-textCtx.Done():
						return
					default:
						if stopFlag.Load() && !gracefulOnly {
							cancel()
							return
						}
						time.Sleep(50 * time.Millisecond)
					}
				}
			}()

			act, amtPtr, err := askAction(textCtx, curModel, legal, minTo, maxTo, obs)
			cancel()
			if err != nil {
				toCallFB := h.CurBet - actor.Committed
				if toCallFB < 0 {
					toCallFB = 0
				}
				if !errors.Is(err, context.Canceled) {
					log.Printf("LLM fallback for %s (%s): %v (legal=%v to_call=%d)", curLabel, curModel, err, legal, toCallFB)
				}
				if debugState {
					fmt.Println(warn("model action error fallback"), err)
				}
				choose := func(want string) bool {
					for _, a := range legal {
						if a == want {
							return true
						}
					}
					return false
				}
				if toCallFB > 0 {
					// if to_call > 0: CALL -> FOLD -> RAISE(min) -> CHECK
					if choose("call") {
						act = "call"
						amtPtr = nil
					} else if choose("fold") {
						act = "fold"
						amtPtr = nil
					} else if choose("raise") {
						act = "raise"
						if amtPtr == nil {
							amt := minTo
							amtPtr = &amt
						}
					} else if choose("check") {
						act = "check"
						amtPtr = nil
					} else {
						act = "check"
						amtPtr = nil
					}
				} else {
					// if to_call == 0: prefer check, then raise
					if choose("check") {
						act = "check"
						amtPtr = nil
					} else if choose("raise") {
						act = "raise"
						if amtPtr == nil {
							amt := minTo
							amtPtr = &amt
						}
					} else if choose("call") {
						act = "call"
						amtPtr = nil
					} else if choose("fold") {
						act = "fold"
						amtPtr = nil
					} else {
						act = "check"
						amtPtr = nil
					}
				}
			}

			// Testing hook: force non-check actions if requested
			if pref := strings.ToLower(strings.TrimSpace(os.Getenv("FORCE_NONCHECK"))); pref != "" && act == "check" {
				contains := func(s string) bool {
					for _, a := range legal {
						if a == s {
							return true
						}
					}
					return false
				}
				switched := false
				switch pref {
				case "raise":
					if contains("raise") {
						act = "raise"
						if amtPtr == nil {
							amt := minTo
							amtPtr = &amt
						}
						switched = true
					}
				case "fold":
					if contains("fold") {
						act = "fold"
						amtPtr = nil
						switched = true
					}
				case "any":
					if contains("raise") {
						act = "raise"
						if amtPtr == nil {
							amt := minTo
							amtPtr = &amt
						}
						switched = true
					} else if contains("call") {
						act = "call"
						amtPtr = nil
						switched = true
					} else if contains("fold") {
						act = "fold"
						amtPtr = nil
						switched = true
					}
				}
				if switched && debugState {
					fmt.Println(warn("forced non-check (test)"))
				}
			}

			// current street contribution + toCall
			myStre := &sbC.stre
			if seat == engine.BB {
				myStre = &bbC.stre
			}
			toCall := h.CurBet - *myStre
			if toCall < 0 {
				toCall = 0
			}
			// optional DB action logger (per step)
			logStep := func(action string, amount *int) {
				if db == nil || matchID == 0 {
					return
				}
				boardNow := make([]string, 0, len(h.Board))
				for _, c := range h.Board {
					boardNow = append(boardNow, c.String())
				}
				sbStack, bbStack := h.SB.Stack, h.BB.Stack
				sbCom, bbCom := h.SB.Committed, h.BB.Committed
				sbHole := []string{}
				bbHole := []string{}
				if len(h.SB.Hole) == 2 {
					sbHole = []string{h.SB.Hole[0].String(), h.SB.Hole[1].String()}
				}
				if len(h.BB.Hole) == 2 {
					bbHole = []string{h.BB.Hole[0].String(), h.BB.Hole[1].String()}
				}
				_ = db.InsertActionLog(context.Background(), matchID, pairIndex, h.ID, s, curLabel, action, amount,
					h.Pot, h.CurBet, toCall, minTo, maxTo, sbStack, bbStack, sbCom, bbCom, boardNow, sbHole, bbHole)
			}

			// logging adornments
			hole := [2]string{h.SB.Hole[0].String(), h.SB.Hole[1].String()}
			if seat == engine.BB {
				hole = [2]string{h.BB.Hole[0].String(), h.BB.Hole[1].String()}
			}
			boardNow := make([]string, 0, len(h.Board))
			for _, c := range h.Board {
				if c.String() != "" {
					boardNow = append(boardNow, c.String())
				}
			}
			desc := describe(hole, boardNow)
			tag := fmt.Sprintf("%s(%s)", seatTag(seat), dim(modelShort(curModel)))

			rem := func() int {
				if seat == engine.SB {
					return h.SB.Stack
				}
				return h.BB.Stack
			}
			apply := func(kind engine.ActionKind, amount int) error {
				err := h.Apply(kind, amount)
				if debugState {
					fmt.Printf("%s DBG: kind=%v amount=%d | CurBet=%d SBCom=%d BBCom=%d SBStack=%d BBStack=%d %s\n",
						dim("["), kind, amount, h.CurBet, h.SB.Committed, h.BB.Committed, h.SB.Stack, h.BB.Stack, dim("]"))
				}
				return err
			}

			applied := false
			switch act {
			case "fold":
				if err := apply(engine.Fold, 0); err == nil {
					logStep("fold", nil)
					fmt.Printf("  %s %s — %s. %s\n", tag, bold("folds"), desc, dim(fmt.Sprintf("Remaining: %d", rem())))
					addAction(tallies, curLabel, "fold")
					if seat == engine.SB {
						winner = engine.BB
					} else {
						winner = engine.SB
					}
					goto PAYOUT
				}
			case "check":
				if err := apply(engine.Check, 0); err == nil {
					logStep("check", nil)
					fmt.Printf("  %s %s — %s. %s\n", tag, bold("checks"), desc, dim(fmt.Sprintf("Remaining: %d", rem())))
					addAction(tallies, curLabel, "check")
					if prevWasCheck {
						goto NEXT_STREET
					}
					prevWasCheck = true
					applied = true
				}
			case "call":
				if err := apply(engine.Call, 0); err == nil {
					amtTmp := toCall
					logStep("call", &amtTmp)
					if seat == engine.SB {
						sbC.total += toCall
						sbC.stre += toCall
					} else {
						bbC.total += toCall
						bbC.stre += toCall
					}
					fmt.Printf("  %s %s %s — %s. %s\n", tag, bold("calls"), good(fmt.Sprintf("%d", toCall)), desc, dim(fmt.Sprintf("Remaining: %d", rem())))
					addAction(tallies, curLabel, "call")
					goto NEXT_STREET
				}
			case "raise":
				raiseTo := minTo
				if amtPtr != nil {
					raiseTo = *amtPtr
				}
				if raiseTo < minTo {
					raiseTo = minTo
				}
				if raiseTo > maxTo {
					raiseTo = maxTo
				}
				if err := apply(engine.Raise, raiseTo); err == nil {
					amtTmp := raiseTo
					logStep("raise", &amtTmp)
					needed := raiseTo - *myStre
					if needed < 0 {
						needed = 0
					}
					if seat == engine.SB {
						sbC.total += needed
						sbC.stre += needed
					} else {
						bbC.total += needed
						bbC.stre += needed
					}
					fmt.Printf("  %s %s %s — %s. %s\n", tag, bold("raises to"), good(fmt.Sprintf("%d", raiseTo)), desc, dim(fmt.Sprintf("Remaining: %d", rem())))
					addAction(tallies, curLabel, "raise")
					prevWasCheck = false
					applied = true
				}
			}

			// one-shot fallback if illegal
			if !applied {
				if debugState {
					fmt.Println(warn("illegal model action; falling back"))
				}
				fallback := actionStrings(h)
				tried := false
				for _, a := range []string{"check", "call", "fold"} {
					for _, l := range fallback {
						if a == l {
							switch a {
							case "check":
								if apply(engine.Check, 0) == nil {
									logStep("check", nil)
									fmt.Printf("  %s %s — %s. %s\n", tag, bold("checks"), desc, dim(fmt.Sprintf("Remaining: %d", rem())))
									addAction(tallies, curLabel, "check")
									if prevWasCheck {
										goto NEXT_STREET
									}
									prevWasCheck = true
									tried = true
								}
							case "call":
								if apply(engine.Call, 0) == nil {
									amtTmp := toCall
									logStep("call", &amtTmp)
									if seat == engine.SB {
										sbC.total += toCall
										sbC.stre += toCall
									} else {
										bbC.total += toCall
										bbC.stre += toCall
									}
									fmt.Printf("  %s %s %s — %s. %s\n", tag, bold("calls"), good(fmt.Sprintf("%d", toCall)), desc, dim(fmt.Sprintf("Remaining: %d", rem())))
									addAction(tallies, curLabel, "call")
									tried = true
									goto NEXT_STREET
								}
							case "fold":
								if apply(engine.Fold, 0) == nil {
									logStep("fold", nil)
									fmt.Printf("  %s %s — %s. %s\n", tag, bold("folds"), desc, dim(fmt.Sprintf("Remaining: %d", rem())))
									addAction(tallies, curLabel, "fold")
									if seat == engine.SB {
										winner = engine.BB
									} else {
										winner = engine.SB
									}
									goto PAYOUT
								}
							}
						}
					}
					if tried {
						break
					}
				}
				// last resort: min-raise
				for _, l := range fallback {
					if l == "raise" {
						rt := minTo
						if apply(engine.Raise, rt) == nil {
							amtTmp := rt
							logStep("raise", &amtTmp)
							needed := rt - *myStre
							if needed < 0 {
								needed = 0
							}
							if seat == engine.SB {
								sbC.total += needed
								sbC.stre += needed
							} else {
								bbC.total += needed
								bbC.stre += needed
							}
							fmt.Printf("  %s %s %s — %s. %s\n", tag, bold("raises to"), good(fmt.Sprintf("%d", rt)), desc, dim(fmt.Sprintf("Remaining: %d", rem())))
							addAction(tallies, curLabel, "raise")
							break
						}
					}
				}
			}

			// pass action
			if seat == engine.SB {
				seat = engine.BB
			} else {
				seat = engine.SB
			}
			continue

		NEXT_STREET:
			break
		}
	}

	// showdown & payout
	winner = h.Showdown()

PAYOUT:
	pot := sbC.total + bbC.total

	// river sanity if no folds
	folded := h.SB.Folded || h.BB.Folded
	if !folded && len(h.Board) == 5 {
		sbScore, bbScore := h.Scores()
		exp := engine.Seat("")
		if sbScore > bbScore {
			exp = engine.SB
		} else if bbScore > sbScore {
			exp = engine.BB
		}
		sbD, bbD := h.EvalDebug()
		fmt.Printf("%s SB[%d]: %s  |  BB[%d]: %s\n", dim("Eval check →"), sbScore, sbD, bbScore, bbD)
		if exp != "" && exp != winner {
			fmt.Printf("%s winner disagrees with raw scores; overriding to %s\n", bad("EVAL MISMATCH:"), seatLabel(exp))
			winner = exp
		}
	}

	// exact chip flow (incl. split)
	if winner == engine.SB {
		sbP.Bank += pot - sbC.total
		bbP.Bank -= bbC.total
		sbP.Wins++
	} else if winner == engine.BB {
		bbP.Bank += pot - bbC.total
		sbP.Bank -= sbC.total
		bbP.Wins++
	} else {
		half := pot / 2
		rem := pot % 2
		sbP.Bank += half + rem - sbC.total
		bbP.Bank += half - bbC.total
	}

	// logs
	winModel := sbP.Model
	if winner == engine.BB {
		winModel = bbP.Model
	}
	if len(h.Board) >= 5 && h.Board[4].String() != "" && winner != "" {
		fmt.Printf("%s %s %s | %s %s %s %s %s %s | %s\n",
			good("Showdown →"), seatTag(winner), good(fmt.Sprintf("(%s)", winModel)),
			bold("Board:"), h.Board[0], h.Board[1], h.Board[2], h.Board[3], h.Board[4],
			potTag(pot),
		)
	} else if winner == "" {
		fmt.Printf("%s %s %s %s %s\n", good("Showdown →"), bold("Tie."), bold("Board:"), h.Board[0], h.Board[1])
	} else {
		fmt.Printf("%s %s %s | %s\n", good("Winner by fold →"), seatTag(winner), good(fmt.Sprintf("(%s)", winModel)), potTag(pot))
	}
	fmt.Printf("%s %s:%d  %s:%d\n\n", bold("Seat banks →"), cyan("SB"), sbP.Bank, warn("BB"), bbP.Bank)

	// deltas (this is what your callers use)
	deltaSB := sbP.Bank - startSB
	deltaBB := bbP.Bank - startBB

	return winner, pot, deltaSB, deltaBB, false
}

//
// ===== small helpers for duel =====
//

func strptr(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

// companyLabel returns a descriptive vendor label for the current LLM base.
func companyLabel() string {
	if v := strings.TrimSpace(os.Getenv("LLM_COMPANY")); v != "" {
		return v
	}
	base := strings.ToLower(strings.TrimSpace(os.Getenv("OPENAI_API_BASE")))
	if base == "" {
		base = strings.ToLower(strings.TrimSpace(os.Getenv("OPENAI_BASE_URL")))
	}
	switch {
	case base == "":
		return "OpenAI"
	case strings.Contains(base, "openrouter"):
		return "OpenRouter"
	case strings.Contains(base, "together"):
		return "Together"
	case strings.Contains(base, "groq"):
		return "Groq"
	case strings.Contains(base, "azure"):
		return "Azure OpenAI"
	case strings.Contains(base, "mistral"):
		return "Mistral"
	case strings.Contains(base, "perplexity"):
		return "Perplexity"
	case strings.Contains(base, "gemini") || strings.Contains(base, "google"):
		return "Google"
	case strings.Contains(base, "openai"):
		return "OpenAI"
	default:
		return "LLM"
	}
}

// extractJSONObject tries to pull the first top-level {...} block from text,
// removing common code fences like ```json ... ```.
func extractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	// Remove leading/trailing code fences
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		// strip optional language id
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSuffix(s, "```")
	}
	// Find first '{' and matching last '}'
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	// naive last brace
	end := strings.LastIndexByte(s, '}')
	if end <= start {
		return ""
	}
	cand := strings.TrimSpace(s[start : end+1])
	return cand
}

// coerceActionMap converts a generic parsed map into (action, amount) with validation.
func coerceActionMap(parsed map[string]any, legal []string, minRaiseTo, maxRaiseTo int, toCall int) (string, *int, bool) {
	var act string
	if v, ok := parsed["action"].(string); ok {
		act = strings.ToLower(strings.TrimSpace(v))
	}
	if act == "bet" {
		act = "raise"
	}
	if !contains(legal, act) {
		return "", nil, false
	}
	var amount *int
	if rawAmt, ok := parsed["amount"]; ok && rawAmt != nil {
		switch t := rawAmt.(type) {
		case float64:
			v := int(t)
			amount = &v
		case string:
			if n, e := strconv.Atoi(strings.TrimSpace(t)); e == nil {
				v := n
				amount = &v
			}
		case json.Number:
			if n, e := t.Int64(); e == nil {
				v := int(n)
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
	// Friendly normalization: call->check when toCall==0
	if toCall == 0 && act == "call" {
		act = "check"
	}
	return act, amount, true
}

// parseYAMLish pulls action/amount from a simple YAML-like text.
func parseYAMLish(s string, legal []string, minRaiseTo, maxRaiseTo int, toCall int) (string, *int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil, false
	}
	lines := strings.Split(s, "\n")
	var action string
	var amount *int
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		t = strings.TrimPrefix(t, "- ")
		if i := strings.Index(t, ":"); i > 0 {
			k := strings.ToLower(strings.TrimSpace(t[:i]))
			v := strings.TrimSpace(t[i+1:])
			switch k {
			case "action":
				action = strings.ToLower(strings.Trim(v, "\"'` "))
				if action == "bet" {
					action = "raise"
				}
			case "amount":
				if strings.EqualFold(v, "null") || v == "" {
					amount = nil
				} else if n, err := strconv.Atoi(v); err == nil {
					vv := n
					amount = &vv
				}
			}
		}
	}
	if !contains(legal, action) || action == "" {
		return "", nil, false
	}
	if action == "raise" {
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
	if toCall == 0 && action == "call" {
		action = "check"
	}
	return action, amount, true
}

// parseNLAction uses simple keyword + number heuristics to extract an action from prose.
func parseNLAction(s string, legal []string, minRaiseTo, maxRaiseTo int, toCall int) (string, *int, bool) {
	ls := strings.ToLower(s)
	// Prioritize raise/bet if a number is present
	var num *int
	// find first integer in text
	start := -1
	for i := 0; i < len(ls); i++ {
		if ls[i] >= '0' && ls[i] <= '9' {
			start = i
			break
		}
	}
	if start >= 0 {
		j := start
		for j < len(ls) && ls[j] >= '0' && ls[j] <= '9' {
			j++
		}
		if n, err := strconv.Atoi(ls[start:j]); err == nil {
			num = &n
		}
	}
	// action keywords
	if strings.Contains(ls, "raise") || strings.Contains(ls, "bet") {
		if contains(legal, "raise") {
			amt := minRaiseTo
			if num != nil {
				amt = *num
			}
			if amt < minRaiseTo || amt > maxRaiseTo {
				return "", nil, false
			}
			return "raise", &amt, true
		}
	}
	if strings.Contains(ls, "call") {
		if toCall == 0 && contains(legal, "check") {
			return "check", nil, true
		}
		if contains(legal, "call") {
			return "call", nil, true
		}
	}
	if strings.Contains(ls, "check") && contains(legal, "check") {
		return "check", nil, true
	}
	if strings.Contains(ls, "fold") && contains(legal, "fold") {
		return "fold", nil, true
	}
	return "", nil, false
}

// companyForModel returns a vendor label for a specific model string.
// For OpenRouter bases, it derives the vendor from the "vendor/model" prefix.
func companyForModel(model string) string {
	// If user overrides company, keep it global.
	if v := strings.TrimSpace(os.Getenv("LLM_COMPANY")); v != "" {
		return v
	}
	base := strings.ToLower(strings.TrimSpace(os.Getenv("OPENAI_API_BASE")))
	if base == "" {
		base = strings.ToLower(strings.TrimSpace(os.Getenv("OPENAI_BASE_URL")))
	}
	if strings.Contains(base, "openrouter") {
		m := strings.TrimSpace(model)
		if idx := strings.Index(m, "/"); idx > 0 {
			vend := strings.ToLower(strings.TrimSpace(m[:idx]))
			switch vend {
			case "openai":
				return "OpenAI"
			case "anthropic":
				return "Anthropic"
			case "google", "gemini":
				return "Google"
			case "meta-llama", "meta", "llama":
				return "Meta"
			case "deepseek":
				return "DeepSeek"
			case "x-ai", "xai":
				return "xAI"
			case "mistral":
				return "Mistral"
			case "perplexity":
				return "Perplexity"
			case "together":
				return "Together"
			case "groq":
				return "Groq"
			case "cohere":
				return "Cohere"
			case "fireworks", "fireworks_ai", "fireworks-ai":
				return "Fireworks"
			default:
				// Title-case fallback
				if len(vend) > 0 {
					return strings.ToUpper(vend[:1]) + vend[1:]
				}
			}
		}
		return "Model"
	}
	// Non-OpenRouter: use base-detected provider label.
	return companyLabel()
}
func tallyCounts(x *ActionTally) (chk, call, raise, fold int) {
	if x == nil {
		return
	}
	return x.Check, x.Call, x.Raise, x.Fold
}

// ===== duel runner =====
func runDuel(checkStop func(bool) bool, gracefulOnly bool, db *store.DB) {
	section("DUEL")

	sb := atoiDef(os.Getenv("SB"), 50)
	bb := atoiDef(os.Getenv("BB"), 100)
	startStack := atoiDef(os.Getenv("START_STACK"), 10000)
	cfg := engine.Config{SB: sb, BB: bb, StartStack: startStack}

	// mirrored seeds: N pairs → 2N hands
	seeds := atoiDef(os.Getenv("DUEL_SEEDS"), 5)
	if seeds <= 0 {
		seeds = (atoiDef(os.Getenv("DUEL_HANDS"), 10) + 1) / 2
	}

	// players
	a, b := loadPlayers(startStack)
	var statsA, statsB ModelStats
	tallies := map[string]*ActionTally{} // keyed by "A"/"B"

	// Elo/Glicko defaults
	eloStart := float64(atoiDef(os.Getenv("ELO_START"), 1500))
	eloK := float64(atoiDef(os.Getenv("ELO_K"), 24))
	eloPerHand := asBool(os.Getenv("ELO_PER_HAND"))
	eloWeightPot := asBool(os.Getenv("ELO_WEIGHT_BY_POT"))
	elo := NewElo(eloStart, eloK)

	gA := NewGlicko2()
	gB := NewGlicko2()
	tau := 0.5

	// CI bookkeeping across pairs
	var pairWinsA, pairTies, pairTotal int
	var margins []float64

	// seed stream
	base := deckSeedFromEnvOrCrypto()
	sm := newSeedStream(base)

	log.Printf("Match seed base: %d (mirrored pairs=%d)", base, seeds)
	fmt.Println(dim("Ctrl+C → graceful stop by default. Set STOP_IMMEDIATE=1 for hard stop."))

	boardStr := func(bd []engine.Card) string {
		if len(bd) < 5 {
			return ""
		}
		return fmt.Sprintf("%s %s %s %s %s", bd[0], bd[1], bd[2], bd[3], bd[4])
	}

	// ---- DB: register bots, seed ratings (if present), create match, write start point
	var matchID int64
	var botAID, botBID int64
	accA, accB := 0.5, 0.5
	if db != nil {
		companyA, companyB := companyForModel(a.Model), companyForModel(b.Model)
		rePtr := strptr(os.Getenv("OPENAI_REASONING_EFFORT"))

		// upsert bots
		idA, err := db.UpsertBot(context.Background(), a.Model, companyA, rePtr)
		if err != nil {
			log.Printf("UpsertBot(A) failed: %v (disabling DB this run)", err)
			db = nil
		} else {
			botAID = idA
		}
		if db != nil {
			idB, err := db.UpsertBot(context.Background(), b.Model, companyB, rePtr)
			if err != nil {
				log.Printf("UpsertBot(B) failed: %v (disabling DB this run)", err)
				db = nil
			} else {
				botBID = idB
			}
		}

		if db != nil && botAID != 0 {
			if good, total, err := db.GetJudgeAccuracy(context.Background(), botAID); err != nil {
				log.Printf("GetJudgeAccuracy(A) failed: %v", err)
			} else if total > 0 {
				accA = float64(good) / float64(total)
			}
		}
		if db != nil && botBID != 0 {
			if good, total, err := db.GetJudgeAccuracy(context.Background(), botBID); err != nil {
				log.Printf("GetJudgeAccuracy(B) failed: %v", err)
			} else if total > 0 {
				accB = float64(good) / float64(total)
			}
		}

		// try to seed career ratings
		if db != nil {
			if eA, grA, rdA, sgA, _, _, err := db.GetOrInitRatings(context.Background(), botAID); err == nil {
				if eB, grB, rdB, sgB, _, _, err2 := db.GetOrInitRatings(context.Background(), botBID); err2 == nil {
					elo.A, elo.B = eA, eB
					gA.Rating, gA.RD, gA.Volatility = grA, rdA, sgA
					gB.Rating, gB.RD, gB.Volatility = grB, rdB, sgB
					log.Printf("Seeding ratings → A: Elo=%.1f Glicko=%.1f/%.0f σ=%.3f | B: Elo=%.1f Glicko=%.1f/%.0f σ=%.3f",
						elo.A, gA.Rating, gA.RD, gA.Volatility, elo.B, gB.Rating, gB.RD, gB.Volatility)
				}
			}
		}

		// create match + start rating point
		if db != nil {
			id, err := db.CreateMatch(context.Background(), sb, bb, startStack, seeds, int64(base), eloStart, eloK, eloPerHand, eloWeightPot)
			if err != nil {
				log.Printf("CreateMatch failed: %v (disabling DB this run)", err)
				db = nil
			} else {
				matchID = id
				if err := db.InsertRatingPoint(context.Background(), matchID, "start", nil,
					elo.A, elo.B,
					gA.Rating, gA.RD, gA.Volatility,
					gB.Rating, gB.RD, gB.Volatility,
				); err != nil {
					log.Printf("InsertRatingPoint(start) failed: %v", err)
				}
			}
		}
	}

	elo.SetAccuracy(accA, accB)

	// ---- loop pairs
	for i := 0; i < seeds; i++ {
		if stopFlag.Load() && gracefulOnly {
			fmt.Println(warn("Termination requested (graceful). Ending match after previous hand."))
			break
		}

		seed := int64(sm.next())
		fmt.Printf("%s starting pair %d/%d (seed=%d)\n", dim("▶"), i+1, seeds, seed)

		// Hand 1: A=SB, B=BB
		deck1 := engine.NewDeck(seed)
		h1 := engine.NewHand(fmt.Sprintf("duel-%dA", i+1), cfg, deck1)
		statsA.addHand(engine.SB)
		statsB.addHand(engine.BB)
		w1, pot1, dSB1, dBB1, aborted := playHandMatch(context.Background(), h1, &a, &b, checkStop, gracefulOnly, tallies, db, matchID, i+1)
		if aborted {
			fmt.Println(bad("Match aborted by user (immediate)."))
			break
		}
		statsA.addNet(engine.SB, dSB1)
		statsB.addNet(engine.BB, dBB1)
		boardA := boardStr(h1.Board)

		if eloPerHand {
			sa1, sb1 := handScore(w1, true) // A sat SB
			dA, dB := elo.UpdateHand(sa1, sb1, pot1, bb, eloWeightPot)
			fmt.Printf("%s %sA → A:%.1f (%+.1f) | B:%.1f (%+.1f)\n",
				mag("Elo (hand)"), bold(fmt.Sprintf("seed %d", i+1)),
				elo.A, dA, elo.B, dB)
		}

		// Hand 2: swap seats, same deck
		deck2 := engine.NewDeck(seed)
		h2 := engine.NewHand(fmt.Sprintf("duel-%dB", i+1), cfg, deck2)
		statsA.addHand(engine.BB)
		statsB.addHand(engine.SB)
		w2, pot2, dSB2, dBB2, aborted2 := playHandMatch(context.Background(), h2, &b, &a, checkStop, gracefulOnly, tallies, db, matchID, i+1)
		if aborted2 {
			fmt.Println(bad("Match aborted by user (immediate)."))
			break
		}
		statsA.addNet(engine.BB, dBB2)
		statsB.addNet(engine.SB, dSB2)
		boardB := boardStr(h2.Board)

		// mirrored board sanity
		if boardA != "" && boardB != "" {
			if boardA == boardB {
				fmt.Println(dim("Mirror check ✓ same board: " + boardA))
			} else {
				fmt.Println(bad("Mirror check ✗ boards differ: A=" + boardA + " | B=" + boardB))
			}
		}

		// optional hand-level Elo second hand
		if eloPerHand {
			sa2, sb2 := handScore(w2, false) // A sat BB here
			dA, dB := elo.UpdateHand(sa2, sb2, pot2, bb, eloWeightPot)
			fmt.Printf("%s %sB → A:%.1f (%+.1f) | B:%.1f (%+.1f)\n",
				mag("Elo (hand)"), bold(fmt.Sprintf("seed %d", i+1)),
				elo.A, dA, elo.B, dB)
		}

		// ----- pair-level updates
		chipsA := dSB1 + dBB2
		pairPot := pot1 + pot2

		// Elo pair update (tempered)
		if !eloPerHand {
			dA, dB := elo.UpdateFromMirror(chipsA, pairPot, bb)
			fmt.Printf("%s %s → chipsA=%+d potSum=%d  |  A:%.1f (%+.1f)  B:%.1f (%+.1f)\n",
				mag("Elo (pair)"), bold(fmt.Sprintf("seed %d", i+1)),
				chipsA, pairPot, elo.A, dA, elo.B, dB)
		}

		// Glicko-2 per pair (use normalized chip margin → S via tanh)
		effStack := float64(cfg.StartStack)
		if effStack <= 0 {
			effStack = float64(100 * bb)
		}
		m := float64(chipsA) / effStack
		S := 0.5 + 0.5*math.Tanh(m)

		oldA := *gA
		oldB := *gB
		gA.UpdatePair(&oldB, S, tau)
		gB.UpdatePair(&oldA, 1.0-S, tau)
		fmt.Printf("%s %s → A:r=%.1f RD=%.0f σ=%.3f | B:r=%.1f RD=%.0f σ=%.3f\n",
			mag("Glicko2 (pair)"), bold(fmt.Sprintf("seed %d", i+1)),
			gA.Rating, gA.RD, gA.Volatility, gB.Rating, gB.RD, gB.Volatility)

		// CI bookkeeping
		pairTotal++
		switch {
		case chipsA > 0:
			pairWinsA++
		case chipsA == 0:
			pairTies++
		}
		margins = append(margins, m)

		// rating point row
		if db != nil && matchID != 0 {
			idx := i + 1
			if err := db.InsertRatingPoint(
				context.Background(), matchID, "after_pair", &idx,
				elo.A, elo.B,
				gA.Rating, gA.RD, gA.Volatility,
				gB.Rating, gB.RD, gB.Volatility,
			); err != nil {
				log.Printf("InsertRatingPoint(pair %d) failed: %v", idx, err)
			}
		}

		// conservation + bust
		total := a.Bank + b.Bank
		fmt.Printf("%s seed %d → %s:%d  %s:%d  | %s %d %s\n",
			dim("After"), i+1, bold("A bank"), a.Bank, bold("B bank"), b.Bank,
			dim("total chips"), total, dim("(conserved)"))

		if a.Bank <= 0 || b.Bank <= 0 {
			fmt.Println(warn("Bank reached zero; ending match."))
			break
		}
		fmt.Printf("%s finished pair %d/%d\n", dim("✓"), i+1, seeds)
		fmt.Println(dim(strings.Repeat("—", 36)))
	}

	// ----- summary
	sum := a.Bank + b.Bank
	fmt.Printf("\n%s A bank:%d (wins=%d) | B bank:%d (wins=%d) | Total:%d\n",
		bold("RESULTS →"), a.Bank, a.Wins, b.Bank, b.Wins, sum)
	fmt.Printf("%s A:%.1f | B:%.1f (pairs=%d)\n",
		bold("Elo final →"), elo.A, elo.B, elo.Games)

	lo, hi := WilsonCI95(pairWinsA, pairTies, pairTotal)
	fmt.Printf("%s pairs=%d → A win-prob 95%% CI=[%.3f, %.3f]\n",
		bold("CI (Wilson) →"), pairTotal, lo, hi)

	blo, bhi := BootstrapCI95(margins, 1000)
	fmt.Printf("%s normalized margin mean 95%% CI=[%.4f, %.4f]\n",
		bold("CI (bootstrap) →"), blo, bhi)

	fmt.Printf("%s A:r=%.1f RD=%.0f | B:r=%.1f RD=%.0f (pairs=%d)\n",
		bold("Glicko2 final →"), gA.Rating, gA.RD, gB.Rating, gB.RD, gA.Games)

	fmt.Printf("%s dealt:%d net:%d | SB dealt:%d net:%d | BB dealt:%d net:%d\n",
		bold("Stats A →"),
		statsA.Overall.Hands, statsA.Overall.NetChips,
		statsA.SB.Hands, statsA.SB.NetChips,
		statsA.BB.Hands, statsA.BB.NetChips)
	fmt.Printf("%s dealt:%d net:%d | SB dealt:%d net:%d | BB dealt:%d net:%d\n",
		bold("Stats B →"),
		statsB.Overall.Hands, statsB.Overall.NetChips,
		statsB.SB.Hands, statsB.SB.NetChips,
		statsB.BB.Hands, statsB.BB.NetChips)

	printTallies(tallies, a, b)

	// ----- DB: final point, participants/tallies, career ratings, close
	if db != nil && matchID != 0 {
		if err := db.InsertRatingPoint(
			context.Background(), matchID, "end", nil,
			elo.A, elo.B,
			gA.Rating, gA.RD, gA.Volatility,
			gB.Rating, gB.RD, gB.Volatility,
		); err != nil {
			log.Printf("InsertRatingPoint(end) failed: %v", err)
		}

		rePtr := strptr(os.Getenv("OPENAI_REASONING_EFFORT"))
		aChk, aCall, aRaise, aFold := tallyCounts(tallies[a.Label])
		bChk, bCall, bRaise, bFold := tallyCounts(tallies[b.Label])

		handsA := statsA.Overall.Hands
		handsB := statsB.Overall.Hands
		netA := a.Bank - startStack
		netB := b.Bank - startStack

		if err := db.InsertParticipantsAndTallies(
			context.Background(), matchID,
			// A
			"A", botAID, a.Model, companyLabel(), rePtr, startStack, a.Bank, a.Wins,
			handsA, statsA.SB.Hands, statsA.BB.Hands, netA,
			// B
			"B", botBID, b.Model, companyLabel(), rePtr, startStack, b.Bank, b.Wins,
			handsB, statsB.SB.Hands, statsB.BB.Hands, netB,
			// tallies
			aChk, aCall, aRaise, aFold,
			bChk, bCall, bRaise, bFold,
		); err != nil {
			log.Printf("InsertParticipantsAndTallies failed: %v", err)
		}

		var judgeGoodA, judgeTotalA, judgeGoodB, judgeTotalB int
		if db != nil && matchID != 0 {
			if err := judge.EvaluateMatchMC(context.Background(), db, matchID); err != nil {
				log.Printf("MCJudge failed for match %d: %v", matchID, err)
			} else {
				log.Printf("MCJudge complete for match %d", matchID)
				if accMap, err := db.MatchJudgeAccuracy(context.Background(), matchID); err != nil {
					log.Printf("MatchJudgeAccuracy failed for match %d: %v", matchID, err)
				} else {
					if acc, ok := accMap[botAID]; ok {
						judgeGoodA, judgeTotalA = acc.Good, acc.Total
					}
					if acc, ok := accMap[botBID]; ok {
						judgeGoodB, judgeTotalB = acc.Good, acc.Total
					}
				}
			}
		}

		// persist career ratings, hands, and judge accuracy
		if err := db.UpdateBotRatings(context.Background(), botAID, elo.A, gA.Rating, gA.RD, gA.Volatility, 1, handsA, judgeGoodA, judgeTotalA); err != nil {
			log.Printf("UpdateBotRatings(A) failed: %v", err)
		}
		if err := db.UpdateBotRatings(context.Background(), botBID, elo.B, gB.Rating, gB.RD, gB.Volatility, 1, handsB, judgeGoodB, judgeTotalB); err != nil {
			log.Printf("UpdateBotRatings(B) failed: %v", err)
		}
		if err := db.SyncJudgeAccuracy(context.Background(), botAID, botBID); err != nil {
			log.Printf("SyncJudgeAccuracy failed: %v", err)
		}

		if err := db.CompleteMatch(context.Background(), matchID); err != nil {
			log.Printf("CompleteMatch failed: %v", err)
		} else {
			log.Printf("match %d persisted.", matchID)
		}
	}
}

// runDuelMatrix runs pairwise duels for all models listed in OPENAI_MODELS (comma-separated).
// Example: OPENAI_MODELS="gpt-4o-mini,gpt-5-mini,gpt-4.1-mini-2025-04-14"
func runDuelMatrix(checkStop func(bool) bool, gracefulOnly bool, db *store.DB) {
	raw := strings.TrimSpace(os.Getenv("OPENAI_MODELS"))
	if raw == "" {
		log.Println("OPENAI_MODELS is empty; supply a comma-separated list to use --duel-matrix.")
		return
	}
	parts := []string{}
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) < 2 {
		log.Println("Need at least two models in OPENAI_MODELS for --duel-matrix.")
		return
	}

	for i := 0; i < len(parts); i++ {
		for j := i + 1; j < len(parts); j++ {
			if stopFlag.Load() && gracefulOnly {
				log.Println("Stop requested; ending matrix loop.")
				return
			}
			a := parts[i]
			b := parts[j]
			log.Printf("Matrix duel: A=%s vs B=%s\n", a, b)
			// Set envs for this duel run
			os.Setenv("OPENAI_MODEL_A", a)
			os.Setenv("OPENAI_MODEL_B", b)
			runDuel(checkStop, gracefulOnly, db)
		}
	}
}

//
// ===== misc helpers =====
//

func printTallies(t map[string]*ActionTally, a, b Player) {
	if len(t) == 0 {
		return
	}
	fmt.Println()
	fmt.Println(bold("Action mix by player:"))
	labels := []string{"A", "B"}
	modelOf := map[string]string{"A": a.Model, "B": b.Model}
	for _, lbl := range labels {
		x := t[lbl]
		if x == nil {
			continue
		}
		total := x.Check + x.Call + x.Raise + x.Fold
		p := func(n int) string {
			if total == 0 {
				return "0%"
			}
			return fmt.Sprintf("%.0f%%", 100.0*float64(n)/float64(total))
		}
		fmt.Printf("  %s (%s) → check:%d(%s)  call:%d(%s)  raise:%d(%s)  fold:%d(%s)  | total:%d\n",
			lbl, dim(modelShort(modelOf[lbl])),
			x.Check, p(x.Check),
			x.Call, p(x.Call),
			x.Raise, p(x.Raise),
			x.Fold, p(x.Fold),
			total,
		)
	}
}

func seatLabel(s engine.Seat) string {
	if s == engine.SB {
		return "SB"
	}
	if s == engine.BB {
		return "BB"
	}
	return ""
}
func stackOf(h *engine.Hand, seat engine.Seat) int {
	if seat == engine.SB {
		return h.SB.Stack
	}
	return h.BB.Stack
}
func handScore(w engine.Seat, aWasSB bool) (sa, sb float64) {
	switch w {
	case engine.SB:
		if aWasSB {
			return 1, 0
		}
		return 0, 1
	case engine.BB:
		if aWasSB {
			return 0, 1
		}
		return 1, 0
	default:
		return 0.5, 0.5
	}
}
