package main

import "math"

// --- Glicko-2 constants & helpers (paper values) ---
const (
	g2Scale = 173.7178          // rating scale between r<->mu
	q       = math.Ln10 / 400.0 // q = ln(10)/400
	pi2     = math.Pi * math.Pi
)

// Glicko2 holds the public “1500-scale” values (not mu/phi).
type Glicko2 struct {
	Rating     float64 // r   (default 1500)
	RD         float64 // RD  (default 350)
	Volatility float64 // sigma (default 0.06)
	Games      int     // number of rating-period updates applied
}

// NewGlicko2 returns a fresh player at the standard defaults.
func NewGlicko2() *Glicko2 {
	return &Glicko2{Rating: 1500, RD: 350, Volatility: 0.06}
}

// NewGlicko2With lets you seed specific starting values.
func NewGlicko2With(r, rd, sigma float64) *Glicko2 {
	return &Glicko2{Rating: r, RD: rd, Volatility: sigma}
}

// Copy makes a shallow copy (handy when you need snapshots).
func (g *Glicko2) Copy() *Glicko2 {
	cp := *g
	return &cp
}

// --- internal conversions r/RD <-> mu/phi ---
func toMuPhi(r, rd float64) (mu, phi float64)   { return (r - 1500.0) / g2Scale, rd / g2Scale }
func fromMuPhi(mu, phi float64) (r, rd float64) { return mu*g2Scale + 1500.0, phi * g2Scale }

// g(phi_j) and E(mu, mu_j, phi_j)
func g(phi float64) float64 { return 1.0 / math.Sqrt(1.0+3.0*q*q*phi*phi/pi2) }
func gExp(mu, muj, phij float64) float64 {
	return 1.0 / (1.0 + math.Exp(-g(phij)*(mu-muj)))
}

// OpponentResult represents one opponent’s aggregate result across
// a rating period. S must be in [0,1]: 1=win, 0=loss, 0.5=tie, or
// any convex score mapping you choose (e.g., margin→S).
type OpponentResult struct {
	Opp *Glicko2
	S   float64
}

// Age applies the “no games this period” step: RD grows due to volatility.
// (Paper step: phi* = sqrt(phi^2 + sigma^2). Rating stays the same.)
func (a *Glicko2) Age() {
	muA, phiA := toMuPhi(a.Rating, a.RD)
	phiStar := math.Sqrt(phiA*phiA + a.Volatility*a.Volatility)
	a.Rating, a.RD = fromMuPhi(muA, phiStar)
	a.Games++
}

// UpdateBatch is the canonical Glicko-2 rating-period update (multiple opponents).
// Provide all opponents (with their ratings/RDs as they were at the START of the period)
// and the aggregate score S vs each of them. tau≈0.5 is typical.
func (a *Glicko2) UpdateBatch(results []OpponentResult, tau float64) {
	// No games: just age.
	if len(results) == 0 {
		a.Age()
		return
	}

	muA, phiA := toMuPhi(a.Rating, a.RD)

	// Sum terms across opponents per the Glicko-2 paper.
	var sumG2E float64 // Σ g^2 * E * (1-E)
	var sumGSE float64 // Σ g * (S - E)
	for _, r := range results {
		muB, phiB := toMuPhi(r.Opp.Rating, r.Opp.RD)
		gB := g(phiB)
		Eab := gExp(muA, muB, phiB)
		sumG2E += (gB * gB) * Eab * (1.0 - Eab)
		sumGSE += gB * (r.S - Eab)
	}
	v := 1.0 / (q * q * sumG2E)
	delta := v * q * sumGSE

	// If delta is effectively zero, skip volatility root-finding but still shrink RD.
	if math.Abs(delta) < 1e-12 {
		phiStar := math.Sqrt(phiA*phiA + a.Volatility*a.Volatility)
		phiNew := 1.0 / math.Sqrt(1.0/(phiStar*phiStar)+1.0/v)
		muNew := muA + (phiNew*phiNew)*q*sumGSE
		a.Rating, a.RD = fromMuPhi(muNew, phiNew)
		a.Games++
		return
	}

	// Solve for new volatility (sigma') via the f(x)=0 root finder.
	a2 := math.Log(a.Volatility * a.Volatility)
	f := func(x float64) float64 {
		ex := math.Exp(x)
		num := ex * (delta*delta - phiA*phiA - v - ex)
		den := 2.0 * (phiA*phiA + v + ex) * (phiA*phiA + v + ex)
		return (num / den) - (x-a2)/(tau*tau)
	}

	A := a2
	var B float64
	if delta*delta > phiA*phiA+v {
		B = math.Log(delta*delta - phiA*phiA - v)
	} else {
		k := 1.0
		for f(a2-k) < 0 && k < 1e6 {
			k *= 2.0
		}
		B = a2 - k
	}
	fA := f(A)
	fB := f(B)

	// Illinois/secant-style iteration with guards.
	for it := 0; it < 60 && math.Abs(B-A) > 1e-6; it++ {
		C := A + (A-B)*fA/(fB-fA)
		fC := f(C)
		if math.IsNaN(fC) || math.IsInf(fC, 0) {
			break
		}
		if fC*fB < 0 {
			A = B
			fA = fB
		}
		B = C
		fB = fC
	}

	newVol := math.Exp(B / 2.0)
	phiStar := math.Sqrt(phiA*phiA + newVol*newVol)
	phiNew := 1.0 / math.Sqrt(1.0/(phiStar*phiStar)+1.0/v)
	muNew := muA + (phiNew*phiNew)*q*sumGSE

	a.Rating, a.RD = fromMuPhi(muNew, phiNew)
	a.Volatility = newVol
	a.Games++
}

// UpdatePair is a convenience wrapper for a single-opponent period.
// (Effectively a special case of UpdateBatch with len=1.)
func (a *Glicko2) UpdatePair(b *Glicko2, S float64, tau float64) {
	a.UpdateBatch([]OpponentResult{{Opp: b, S: S}}, tau)
}

// --- Optional helpers for mapping chip margins → scores S in [0,1] ---

// ScoreFromWL returns S for pure outcomes: win=1, tie=0.5, loss=0.
func ScoreFromWL(win bool, tie bool) float64 {
	if tie {
		return 0.5
	}
	if win {
		return 1.0
	}
	return 0.0
}

// ScoreFromMargin maps a normalized chip margin m=(chipsA/effStack) to S in [0,1]
// using a tanh curve. k controls steepness (e.g., k=1.0 mildly compresses).
// If effStack<=0 it returns 0.5.
func ScoreFromMargin(chipsA int, effStack, k float64) float64 {
	if effStack <= 0 {
		return 0.5
	}
	m := float64(chipsA) / effStack
	return 0.5 + 0.5*math.Tanh(k*m)
}
