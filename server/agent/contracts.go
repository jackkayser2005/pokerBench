package agent

import (
	"ai-thunderdome/server/engine"
	"encoding/json"
	"fmt"
)

type Observation struct {
	HandID     string         `json:"hand_id"`
	Seat       string         `json:"seat"`       // "SB" | "BB"
	Street     string         `json:"street"`     // preflop|flop|turn|river
	HoleCards  []string       `json:"hole_cards"` // e.g. ["As","Kd"]
	Board      []string       `json:"board"`      // 0..5 cards
	Stacks     map[string]int `json:"stacks"`     // {hero, villain} chips behind
	Blinds     map[string]int `json:"blinds"`     // {sb, bb, ante}
	Pot        int            `json:"pot"`
	ToCall     int            `json:"to_call"`
	MinRaiseTo int            `json:"min_raise_to"`  // absolute raise-to
	MaxRaiseTo int            `json:"max_raise_to"`  // absolute raise-to (all-in)
	Legal      []string       `json:"legal_actions"` // subset of fold/check/call/raise
	HistoryLen int            `json:"history_len"`
}

type ActionOut struct {
	Action  string             `json:"action"`           // fold|check|call|raise
	Amount  *int               `json:"amount,omitempty"` // required if raise
	Policy  map[string]float64 `json:"policy,omitempty"`
	Comment string             `json:"comment,omitempty"` // <=120 chars
}

// BuildObservation converts engine state into the JSON we send the model.
func BuildObservation(h *engine.Hand, seat engine.Seat) Observation {
	p := h.SB
	o := h.BB
	if seat == engine.BB {
		p, o = h.BB, h.SB
	}

	toCall := h.CurBet - p.Committed
	if toCall < 0 {
		toCall = 0
	}

	legal := []string{}
	for _, k := range h.Legal() {
		legal = append(legal, string(k))
	}

	return Observation{
		HandID:     h.ID,
		Seat:       string(seat),
		Street:     h.Street,
		HoleCards:  cardsToStr(p.Hole),
		Board:      cardsToStr(h.Board),
		Stacks:     map[string]int{"hero": p.Stack, "villain": o.Stack},
		Blinds:     map[string]int{"sb": h.Cfg.SB, "bb": h.Cfg.BB, "ante": 0},
		Pot:        h.Pot,
		ToCall:     toCall,
		MinRaiseTo: h.CurBet + h.MinRaise,
		MaxRaiseTo: p.Stack + p.Committed,
		Legal:      legal,
		HistoryLen: len(h.History),
	}
}

func cardsToStr(cs []engine.Card) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.String()
	}
	return out
}

// Validate the model's action against the observation.
func Validate(o Observation, a ActionOut) error {
	// legal action?
	ok := false
	for _, la := range o.Legal {
		if la == a.Action {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("illegal action %q (legals: %v)", a.Action, o.Legal)
	}

	// size checks if raise
	if a.Action == "raise" {
		if a.Amount == nil {
			return fmt.Errorf("raise requires amount")
		}
		if *a.Amount < o.MinRaiseTo || *a.Amount > o.MaxRaiseTo {
			return fmt.Errorf("raise amount %d out of bounds [%d, %d]", *a.Amount, o.MinRaiseTo, o.MaxRaiseTo)
		}
	}
	// if to_call==0 and model says "call", we’ll treat it as check (friendly)
	if o.ToCall == 0 && a.Action == "call" {
		a.Action = "check"
	}
	// comment length (soft)
	if len(a.Comment) > 120 {
		a.Comment = a.Comment[:120]
	}
	_, _ = json.Marshal(a) // sanity
	return nil
}
