package strength

import (
	"math"
	"testing"

	"github.com/vmanke/goteach-prod/board"
)

func TestGroupExtremes(t *testing.T) {
	size := 5
	n := size * size
	stones := []board.Point{{X: 2, Y: 2}}

	allBlack := make([]float64, n)
	allWhite := make([]float64, n)

	for i := range allBlack {
		allBlack[i] = 1.0
		allWhite[i] = -1.0
	}

	if s := Group(size, allBlack, stones, board.Black, 3.0); math.Abs(s-1.0) > 1e-9 {
		t.Fatalf("Schwarz auf +1-Feld: Stärke %f, erwartet 1", s)
	}

	if s := Group(size, allWhite, stones, board.White, 3.0); math.Abs(s-1.0) > 1e-9 {
		t.Fatalf("Weiß auf −1-Feld: Stärke %f, erwartet 1", s)
	}

	if s := Group(size, allWhite, stones, board.Black, 3.0); math.Abs(s+1.0) > 1e-9 {
		t.Fatalf("Schwarz auf −1-Feld: Stärke %f, erwartet −1", s)
	}
}

// Kleines tau gewichtet die unmittelbare Umgebung stärker.
func TestGroupTauLocality(t *testing.T) {
	size := 7
	n := size * size
	own := make([]float64, n)

	// Positiv nur direkt am Stein (3,3), Rest stark negativ.
	for i := range own {
		own[i] = -1.0
	}

	center := 3*size + 3
	own[center] = 1.0
	own[center-1] = 1.0
	own[center+1] = 1.0

	stones := []board.Point{{X: 3, Y: 3}}
	narrow := Group(size, own, stones, board.Black, 0.5)
	wide := Group(size, own, stones, board.Black, 5.0)

	if !(narrow > wide) {
		t.Fatalf("Lokalität verletzt: tau=0.5 → %f, tau=5.0 → %f", narrow, wide)
	}
}
