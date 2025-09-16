package engine

import "fmt"

type Config struct{ SB, BB, StartStack int }

type Player struct {
	Seat      Seat
	Stack     int
	Committed int
	Hole      []Card
	Folded    bool
	AllIn     bool
}

type Hand struct {
	ID       string
	Cfg      Config
	Deck     []Card
	Board    []Card
	Pot      int
	Street   string
	SB, BB   *Player
	ToAct    Seat
	CurBet   int
	MinRaise int
	History  []Action
}

func NewHand(id string, cfg Config, deck []Card) *Hand {
	h := &Hand{
		ID: id, Cfg: cfg, Deck: deck, Street: "preflop",
		SB: &Player{Seat: SB, Stack: cfg.StartStack},
		BB: &Player{Seat: BB, Stack: cfg.StartStack},
	}
	h.postBlinds()
	h.dealHole()
	h.ToAct = SB        // HU preflop: SB first
	h.MinRaise = cfg.BB // postflop increment; preflop min to is set on first raise
	return h
}

func (h *Hand) postBlinds() { h.bet(h.SB, h.Cfg.SB); h.bet(h.BB, h.Cfg.BB) }
func (h *Hand) dealHole()   { h.SB.Hole = []Card{h.pop(), h.pop()}; h.BB.Hole = []Card{h.pop(), h.pop()} }
func (h *Hand) pop() Card   { c := h.Deck[0]; h.Deck = h.Deck[1:]; return c }

func (h *Hand) bet(p *Player, amt int) {
	if amt >= p.Stack {
		amt = p.Stack
		p.AllIn = true
	}
	p.Stack -= amt
	p.Committed += amt
	if p.Committed > h.CurBet {
		h.CurBet = p.Committed
	}
	h.Pot += amt
}

func (h *Hand) other(p *Player) *Player {
	if p.Seat == SB {
		return h.BB
	}
	return h.SB
}
func (h *Hand) actor() *Player {
	if h.ToAct == SB {
		return h.SB
	}
	return h.BB
}

func (h *Hand) Legal() []ActionKind {
	a := h.actor()
	if a.Folded || a.AllIn {
		return nil
	}
	var out []ActionKind
	toCall := h.CurBet - a.Committed
	if toCall == 0 {
		out = append(out, Check)
	} else {
		out = append(out, Fold, Call)
	}
	if !a.AllIn && !h.other(a).AllIn {
		out = append(out, Raise)
	}
	return out
}

func (h *Hand) Apply(kind ActionKind, amount int) error {
	a := h.actor()
	switch kind {
	case Fold:
		a.Folded = true
		h.History = append(h.History, Action{Seat: a.Seat, Kind: Fold})
		return nil
	case Check:
		if h.CurBet-a.Committed != 0 {
			return fmt.Errorf("cannot check")
		}
		h.History = append(h.History, Action{Seat: a.Seat, Kind: Check})
	case Call:
		to := h.CurBet - a.Committed
		if to < 0 {
			to = 0
		}
		h.bet(a, to)
		h.History = append(h.History, Action{Seat: a.Seat, Kind: Call, Amount: to})
	case Raise:
		if amount < h.CurBet+h.MinRaise {
			return fmt.Errorf("min raise to %d", h.CurBet+h.MinRaise)
		}
		prevCur := h.CurBet
		raise := amount - a.Committed
		h.bet(a, raise)
		h.MinRaise = amount - prevCur // amount minus previous CurBet
		h.History = append(h.History, Action{Seat: a.Seat, Kind: Raise, Amount: amount})
	}
	h.ToAct = h.other(a).Seat
	return nil
}

func (h *Hand) bettingRoundDone() bool {
	if h.SB.Folded || h.BB.Folded || h.SB.AllIn || h.BB.AllIn {
		return true
	}
	needSB := h.CurBet - h.SB.Committed
	needBB := h.CurBet - h.BB.Committed
	if needSB == 0 && needBB == 0 {
		n := len(h.History)
		if n >= 2 && h.History[n-1].Kind != Raise && h.History[n-2].Kind != Raise {
			return true
		}
	}
	return false
}

func (h *Hand) NextStreet() {
	switch h.Street {
	case "preflop":
		h.Board = append(h.Board, h.pop(), h.pop(), h.pop())
		h.Street = "flop"
	case "flop":
		h.Board = append(h.Board, h.pop())
		h.Street = "turn"
	case "turn":
		h.Board = append(h.Board, h.pop())
		h.Street = "river"
	}
	h.CurBet = 0
	h.SB.Committed = 0
	h.BB.Committed = 0
	h.MinRaise = h.Cfg.BB
	h.ToAct = BB // postflop in HU
}

func (h *Hand) Done() bool {
	return (h.Street == "river" && h.bettingRoundDone()) || h.SB.Folded || h.BB.Folded || h.SB.AllIn || h.BB.AllIn
}

func (h *Hand) Showdown() Seat {
	if h.SB.Folded {
		return BB
	}
	if h.BB.Folded {
		return SB
	}
	sb := best5of7(append(append([]Card{}, h.SB.Hole...), h.Board...))
	bb := best5of7(append(append([]Card{}, h.BB.Hole...), h.Board...))
	switch {
	case better(sb, bb):
		return SB
	case better(bb, sb):
		return BB
	default:
		return "" // tie
	}
}
