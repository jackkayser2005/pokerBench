package main

import (
	"math"
	mrand "math/rand"
	"sort"
)

func PairedBootstrapCI(deltas []float64, B int, alpha float64) (mean, lo, hi float64) {
	n := len(deltas)
	if n == 0 || B <= 0 {
		return 0, 0, 0
	}
	if alpha <= 0 || alpha >= 1 {
		alpha = 0.05
	}
	sum := 0.0
	for _, v := range deltas {
		sum += v
	}
	mean = sum / float64(n)
	if B == 1 {
		return mean, mean, mean
	}
	samples := make([]float64, B)
	for b := 0; b < B; b++ {
		acc := 0.0
		for i := 0; i < n; i++ {
			acc += deltas[mrand.Intn(n)]
		}
		samples[b] = acc / float64(n)
	}
	sort.Float64s(samples)
	lowerIdx := int(math.Round((alpha / 2.0) * float64(B-1)))
	upperIdx := int(math.Round((1.0 - alpha/2.0) * float64(B-1)))
	if lowerIdx < 0 {
		lowerIdx = 0
	}
	if upperIdx >= B {
		upperIdx = B - 1
	}
	lo = samples[lowerIdx]
	hi = samples[upperIdx]
	return
}

func KendallTau(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	posA := make(map[string]int, len(a))
	for i, v := range a {
		if _, ok := posA[v]; !ok {
			posA[v] = i
		}
	}
	posB := make(map[string]int, len(b))
	for i, v := range b {
		if _, ok := posB[v]; !ok {
			posB[v] = i
		}
	}
	order := make([]string, 0, len(a))
	seen := make(map[string]struct{}, len(a))
	for _, v := range a {
		if _, ok := posB[v]; !ok {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		order = append(order, v)
	}
	if len(order) < 2 {
		return 0
	}
	totalPairs := float64(len(order) * (len(order) - 1) / 2)
	if totalPairs == 0 {
		return 0
	}
	concordant := 0
	discordant := 0
	for i := 0; i < len(order)-1; i++ {
		for j := i + 1; j < len(order); j++ {
			ai := posA[order[i]]
			aj := posA[order[j]]
			bi := posB[order[i]]
			bj := posB[order[j]]
			diffA := ai - aj
			diffB := bi - bj
			prod := diffA * diffB
			if prod > 0 {
				concordant++
			} else if prod < 0 {
				discordant++
			}
		}
	}
	return float64(concordant-discordant) / totalPairs
}
