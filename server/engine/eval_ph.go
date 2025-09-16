package engine

import (
	poker "github.com/paulhankin/poker"
)

// Library-based hand rank. Smaller score = stronger hand.
type handRank struct{ score int16 }

func better(a, b handRank) bool { return a.score > b.score }

// Convert our engine.Card -> library card.
func toPH(c Card) poker.Card {
	var s poker.Suit
	switch c.Suit {
	case 'c':
		s = poker.Club
	case 'd':
		s = poker.Diamond
	case 'h':
		s = poker.Heart
	case 's':
		s = poker.Spade
	default:
		s = poker.Club
	}
	// Our ranks: 2..14 (Ace=14). Library: 1..13 (Ace=1).
	var r poker.Rank
	if c.Rank == 14 {
		r = poker.Rank(1)
	} else {
		r = poker.Rank(c.Rank)
	}
	card, _ := poker.MakeCard(s, r)
	return card
}

func best5of7(cards []Card) handRank {
	n := len(cards)
	pcs := make([]poker.Card, n)
	for i, c := range cards {
		pcs[i] = toPH(c)
	}
	switch n {
	case 7:
		var a7 [7]poker.Card
		copy(a7[:], pcs)
		return handRank{score: poker.Eval7(&a7)}
	case 5:
		var a5 [5]poker.Card
		copy(a5[:], pcs)
		return handRank{score: poker.Eval5(&a5)}
	case 3:
		var a3 [3]poker.Card
		copy(a3[:], pcs)
		return handRank{score: poker.Eval3(&a3)}
	default:
		// 6 (or 4) cards: choose best 5.
		return handRank{score: bestOfFiveSubsets(pcs)}
	}
}

func bestOfFiveSubsets(pcs []poker.Card) int16 {
	n := len(pcs)
	if n < 5 {
		var a5 [5]poker.Card
		copy(a5[:n], pcs)
		return poker.Eval5(&a5) // shouldn't happen in normal flow
	}
	best := int16(32767)
	choose := [5]int{}
	var five [5]poker.Card
	var rec func(start, k int)
	rec = func(start, k int) {
		if k == 5 {
			for i := 0; i < 5; i++ {
				five[i] = pcs[choose[i]]
			}
			score := poker.Eval5(&five)
			if score < best {
				best = score
			}
			return
		}
		for i := start; i <= n-(5-k); i++ {
			choose[k] = i
			rec(i+1, k+1)
		}
	}
	rec(0, 0)
	return best
}

// Scores returns raw library scores for SB and BB (smaller is better).
func (h *Hand) Scores() (int, int) {
	sb := best5of7(append(append([]Card{}, h.SB.Hole...), h.Board...))
	bb := best5of7(append(append([]Card{}, h.BB.Hole...), h.Board...))
	return int(sb.score), int(bb.score)
}

// EvalDebug returns poker.Describe() strings for both players (7-card view).
func (h *Hand) EvalDebug() (sbDesc string, bbDesc string) {
	toSlice := func(cs []Card) []poker.Card {
		out := make([]poker.Card, len(cs))
		for i, c := range cs {
			out[i] = toPH(c)
		}
		return out
	}
	sbAll := append(append([]Card{}, h.SB.Hole...), h.Board...)
	bbAll := append(append([]Card{}, h.BB.Hole...), h.Board...)
	if d, err := poker.Describe(toSlice(sbAll)); err == nil {
		sbDesc = d
	}
	if d, err := poker.Describe(toSlice(bbAll)); err == nil {
		bbDesc = d
	}
	return
}
