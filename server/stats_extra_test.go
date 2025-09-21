package main

import (
	"math"
	mrand "math/rand"
	"testing"
)

func TestPairedBootstrapCI(t *testing.T) {
	deltas := []float64{-5, -2.5, 0, 2.5, 5}
	mrand.Seed(123)
	mean, lo, hi := PairedBootstrapCI(deltas, 2000, 0.05)
	if math.Abs(mean-0) > 1e-9 {
		t.Fatalf("expected mean 0, got %f", mean)
	}
	if !(lo < mean && mean < hi) {
		t.Fatalf("expected mean inside interval, got lo=%f hi=%f mean=%f", lo, hi, mean)
	}
	if hi-lo <= 0 {
		t.Fatalf("expected positive width interval")
	}
}

func TestPairedBootstrapCIEmpty(t *testing.T) {
	mean, lo, hi := PairedBootstrapCI(nil, 1000, 0.05)
	if mean != 0 || lo != 0 || hi != 0 {
		t.Fatalf("expected zeros, got %f %f %f", mean, lo, hi)
	}
}

func TestKendallTau(t *testing.T) {
	a := []string{"a", "b", "c", "d"}
	b := []string{"a", "b", "c", "d"}
	if v := KendallTau(a, b); math.Abs(v-1) > 1e-9 {
		t.Fatalf("expected tau=1, got %f", v)
	}
	c := []string{"d", "c", "b", "a"}
	if v := KendallTau(a, c); math.Abs(v+1) > 1e-9 {
		t.Fatalf("expected tau=-1, got %f", v)
	}
	d := []string{"a", "c", "e", "b"}
	if v := KendallTau(a, d); v >= 1 || v <= -1 {
		t.Fatalf("expected tau in (-1,1), got %f", v)
	}
	e := []string{"z"}
	if v := KendallTau(a, e); v != 0 {
		t.Fatalf("expected 0 for insufficient overlap, got %f", v)
	}
}
