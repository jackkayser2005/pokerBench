package main

import (
	"math"
	"testing"
)

func TestUpdateFromMirrorWeightToggle(t *testing.T) {
	potSum := 1000
	bb := 100
	chipsA := 220
	winsA, winsB := 1.4, 0.6
	foldA, foldB := 0.3, -0.1

	weighted := NewElo(1500, 32, true)
	unweighted := NewElo(1500, 32, false)

	dAWeighted, _ := weighted.UpdateFromMirror(chipsA, potSum, bb, winsA, winsB, foldA, foldB)
	dAUnweighted, _ := unweighted.UpdateFromMirror(chipsA, potSum, bb, winsA, winsB, foldA, foldB)

	if dAUnweighted == 0 {
		t.Fatalf("expected non-zero unweighted delta")
	}

	gotRatio := dAWeighted / dAUnweighted
	wantRatio := mirrorWeight(potSum, bb)

	if math.Abs(gotRatio-wantRatio) > 1e-9 {
		t.Fatalf("ratio mismatch: got %.6f want %.6f", gotRatio, wantRatio)
	}
}
