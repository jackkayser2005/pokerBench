package main

import "math"

// Elo holds ratings for model A and B (per *mirrored pair*).
type Elo struct {
	A, B  float64 // ratings
	K     float64 // base K
	Games int     // mirrored pairs processed (pair-mode only)
}

func NewElo(start, k float64) Elo { return Elo{A: start, B: start, K: k} }

func (e Elo) expect() (ea, eb float64) {
	ea = 1.0 / (1.0 + math.Pow(10, (e.B-e.A)/400.0))
	return ea, 1.0 - ea
}

// Pair update → returns applied deltas (dA, dB).
// chipsA = A's net chips over mirrored pair; potSum = pot1+pot2; bb = big blind.
func (e *Elo) UpdateFromMirror(chipsA, potSum, bb int) (dA, dB float64) {
	ea, eb := e.expect()

	// soft score from chip margin (normalized in BBs)
	lambdaBB := 6.0
	sA := 0.5 + 0.5*math.Tanh(float64(chipsA)/(lambdaBB*float64(bb)))
	sB := 1.0 - sA

	// effective K (tempered by pot, margin, and slow anneal over pairs)
	kEff := e.K * potScale(potSum, bb) * marginScale(chipsA, bb) * decay(e.Games)

	dA = kEff * (sA - ea)
	dB = kEff * (sB - eb)

	e.A += dA
	e.B += dB
	e.Games++ // counts mirrored pairs in pair-mode

	return dA, dB
}

// Hand update → returns applied deltas (dA, dB).
func (e *Elo) UpdateHand(sa, sb float64, pot, bb int, weightByPot bool) (dA, dB float64) {
	ea, eb := e.expect()
	k := e.K
	if weightByPot {
		k *= potScale(pot, bb)
	}
	dA = k * (sa - ea)
	dB = k * (sb - eb)
	e.A += dA
	e.B += dB
	return dA, dB
}

// ---- helpers ----

func clamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

func potScale(pot, bb int) float64 {
	if bb <= 0 || pot <= 0 {
		return 1.0
	}
	scale := float64(pot) / (2.0 * float64(bb)) // ~2bb baseline
	return clamp(scale, 0.5, 3.0)
}

func marginScale(chipsA, bb int) float64 {
	if bb <= 0 {
		return 1.0
	}
	m := math.Abs(float64(chipsA)) / float64(bb) // in BBs
	return 1.0 + 0.35*math.Tanh(m/8.0)           // ≤ ~1.35
}

func decay(games int) float64 {
	return 1.0 / (1.0 + 0.01*float64(games)) // slow anneal over pairs
}
