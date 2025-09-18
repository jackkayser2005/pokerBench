package main

import "math"

// Elo holds ratings for model A and B (per *mirrored pair*).
type Elo struct {
	A, B  float64 // ratings
	K     float64 // base K
	Games int     // mirrored pairs processed (pair-mode only)
	AccA  float64 // historical judge accuracy [0,1]
	AccB  float64 // historical judge accuracy [0,1]
}

func NewElo(start, k float64) Elo { return Elo{A: start, B: start, K: k, AccA: 0.5, AccB: 0.5} }

func (e Elo) expect() (ea, eb float64) {
	ea = 1.0 / (1.0 + math.Pow(10, (e.B-e.A)/400.0))
	return ea, 1.0 - ea
}

func (e *Elo) SetAccuracy(accA, accB float64) {
	if accA < 0 {
		accA = 0
	} else if accA > 1 {
		accA = 1
	}
	if accB < 0 {
		accB = 0
	} else if accB > 1 {
		accB = 1
	}
	e.AccA = accA
	e.AccB = accB
}

// Pair update → returns applied deltas (dA, dB).
// chipsA = A's net chips over mirrored pair; potSum = pot1+pot2; bb = big blind.
func (e *Elo) UpdateFromMirror(chipsA, potSum, bb int) (dA, dB float64) {
	ea, eb := e.expect()

	denom := float64(potSum)
	if denom <= 0 && bb > 0 {
		denom = 2 * float64(bb)
	}
	if denom <= 0 {
		denom = 1
	}

	// smooth mirrored score in [-1,1] so big margins don't explode the rating step
	scale := denom / 2.0
	if scale <= 0 {
		scale = 1
	}
	norm := math.Tanh(float64(chipsA) / (scale * 1.4))
	bias := clamp((e.AccA-e.AccB)*0.35, -0.2, 0.2)
	norm = clamp(norm+bias, -0.999, 0.999)
	sA := 0.5 + 0.5*norm
	sB := 1.0 - sA

	avgAcc := (e.AccA + e.AccB) * 0.5
	volAdj := clamp(0.85+0.3*avgAcc, 0.75, 1.15)
	kEff := e.K * mirrorWeight(potSum, bb) * volAdj * decay(e.Games)

	dA = kEff * (sA - ea)
	dB = kEff * (sB - eb)

	e.A += dA
	e.B += dB
	e.Games++

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

func mirrorWeight(potSum, bb int) float64 {
	if potSum <= 0 || bb <= 0 {
		return 1.0
	}
	pairBB := float64(potSum) / (2.0 * float64(bb))
	return clamp(pairBB, 0.6, 3.0)
}

func decay(games int) float64 {
	return 1.0 / (1.0 + 0.01*float64(games)) // slow anneal over pairs
}
