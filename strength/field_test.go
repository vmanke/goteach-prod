package strength

import (
	"math"
	"testing"

	"github.com/vmanke/goteach-prod/board"
)

func TestFieldKonstantesOwnershipBleibtKonstant(t *testing.T) {
	// Ist überall dasselbe Ownership, muss die gewichtete Mittelung genau
	// diesen Wert liefern — auch am Rand, wo weniger Nachbarschaft da ist.
	const size = 9

	own := make([]float64, size*size)

	for i := range own {
		own[i] = 0.7
	}

	field := Field(size, own, 3.0)

	for i, v := range field {
		if math.Abs(v-0.7) > 1e-9 {
			t.Fatalf("Punkt %d = %v, erwartet 0.7", i, v)
		}
	}
}

func TestFieldBleibtImWertebereich(t *testing.T) {
	const size = 19

	own := make([]float64, size*size)

	for i := range own {
		// Abwechselnd voll schwarz und voll weiß — der Extremfall.
		if i%2 == 0 {
			own[i] = 1.0
		} else {
			own[i] = -1.0
		}
	}

	for _, v := range Field(size, own, 3.0) {
		if v < -1.0 || v > 1.0 {
			t.Fatalf("Wert %v außerhalb [-1, 1]", v)
		}
	}
}

func TestFieldFolgtDerLokalenMehrheit(t *testing.T) {
	// Linke Bretthälfte schwarz, rechte weiß: Das Feld muss links positiv
	// und rechts negativ sein, mit einem Übergang in der Mitte.
	const size = 19

	own := make([]float64, size*size)

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if x < size/2 {
				own[y*size+x] = 1.0
			} else {
				own[y*size+x] = -1.0
			}
		}
	}

	field := Field(size, own, 3.0)
	mid := size / 2

	if got := field[mid*size+1]; got <= 0.5 {
		t.Fatalf("linke Hälfte = %v, deutlich positiv erwartet", got)
	}

	if got := field[mid*size+size-2]; got >= -0.5 {
		t.Fatalf("rechte Hälfte = %v, deutlich negativ erwartet", got)
	}
}

func TestFieldStimmtMitGroupFuerEinenStein(t *testing.T) {
	// Für eine Kette aus genau einem Stein ist Groups Multi-Source-Distanz
	// dieselbe Einzelquellendistanz, die Field benutzt. Beide Wege müssen
	// dann denselben Wert liefern — das verankert Field am bestehenden Maß.
	const size = 13

	own := make([]float64, size*size)

	for i := range own {
		own[i] = math.Sin(float64(i)) // beliebig, aber deterministisch
	}

	point := board.Point{X: 4, Y: 6}
	field := Field(size, own, 3.0)
	group := Group(size, own, []board.Point{point}, board.Black, 3.0)

	if diff := math.Abs(field[point.Y*size+point.X] - group); diff > 1e-9 {
		t.Fatalf("Field %v ≠ Group %v (Differenz %v)",
			field[point.Y*size+point.X], group, diff)
	}
}

func TestFieldLehntFalscheLaengeAb(t *testing.T) {
	if Field(9, make([]float64, 10), 3.0) != nil {
		t.Fatal("nil bei falscher Ownership-Länge erwartet")
	}
}
