package main

import (
	"ai-thunderdome/server/engine"
	"math"
	"math/rand"
	"sort"
)

type SeatStats struct {
	Hands      int
	VPIP       int
	PFR        int
	ThreeBet   int
	SawFlop    int
	CBet       int
	FacedCbet  int
	FoldToCbet int
	Calls      int
	Aggr       int
	WTSD       int
	WSD        int
	WWSF       int
	NetChips   int
}

func (s *SeatStats) AF() float64 {
	if s.Calls == 0 {
		if s.Aggr == 0 {
			return 0
		}
		return float64(s.Aggr)
	}
	return float64(s.Aggr) / float64(s.Calls)
}

func (s *SeatStats) BBPer100(bb int) float64 {
	h := s.Hands
	if h == 0 || bb <= 0 {
		return 0
	}
	return (float64(s.NetChips) / float64(bb)) / (float64(h) / 100.0)
}

type ModelStats struct {
	Overall SeatStats
	SB      SeatStats
	BB      SeatStats
}

func (m *ModelStats) seatBucket(seat engine.Seat) *SeatStats {
	if seat == engine.SB {
		return &m.SB
	}
	return &m.BB
}
func (m *ModelStats) addHand(seat engine.Seat) {
	m.Overall.Hands++
	m.seatBucket(seat).Hands++
}
func (m *ModelStats) addNet(seat engine.Seat, delta int) {
	m.Overall.NetChips += delta
	m.seatBucket(seat).NetChips += delta
}

// --------- CI helpers (for your paper/plots) ---------

// WilsonCI95 for Bernoulli win rate using wins/ties/total over mirrored pairs.
func WilsonCI95(wins, ties, total int) (low, hi float64) {
	if total <= 0 {
		return 0, 1
	}
	z := 1.96
	n := float64(total)
	p := (float64(wins) + 0.5*float64(ties)) / n
	den := 1 + (z*z)/n
	center := p + (z*z)/(2*n)
	half := z * math.Sqrt((p*(1-p))/n+(z*z)/(4*n*n))
	return (center - half) / den, (center + half) / den
}

// BootstrapCI95 for the mean of values (e.g., normalized chip margins).
func BootstrapCI95(vals []float64, B int) (low, hi float64) {
	n := len(vals)
	if n == 0 || B <= 1 {
		return 0, 0
	}
	res := make([]float64, B)
	for b := 0; b < B; b++ {
		sum := 0.0
		for i := 0; i < n; i++ {
			sum += vals[rand.Intn(n)]
		}
		res[b] = sum / float64(n)
	}
	sort.Float64s(res)
	l := int(0.025 * float64(B-1))
	h := int(0.975 * float64(B-1))
	return res[l], res[h]
}
