package teaching

import (
	"strings"
	"testing"

	"github.com/vmanke/goteach-prod/board"
)

// boardPoint ist die Testabkürzung für GTP-Koordinaten auf 19×19.
func boardPoint(coord string) (board.Point, bool, error) {
	return board.FromGTP(coord, 19)
}

func TestBilanzZaehltGefangeneUndToteSteine(t *testing.T) {
	g := &board.Game{Size: 9, Komi: 6.5, Rules: "japanese", Result: "W+3.5"}
	b := board.New(9)

	// Schwarz A1 und B1 auf weißem Gebiet, Weiß J9 auf schwarzem Gebiet.
	for _, s := range []struct {
		coord  string
		colour board.Color
	}{
		{"A1", board.Black}, {"B1", board.Black}, {"E5", board.Black},
		{"J9", board.White}, {"D4", board.White},
	} {
		p, _, err := board.FromGTP(s.coord, 9)

		if err != nil {
			t.Fatal(err)
		}

		if err := b.SetStone(p, s.colour); err != nil {
			t.Fatal(err)
		}
	}

	b.Captured[board.White] = 3 // Schwarz schlug drei weiße Steine
	b.Captured[board.Black] = 1

	ownership := make([]float64, 81)

	for i := range ownership {
		ownership[i] = -0.9 // alles weiß
	}

	set := func(coord string, v float64) {
		p, _, _ := board.FromGTP(coord, 9)
		ownership[p.Y*9+p.X] = v
	}

	set("E5", 0.8)
	set("J9", 0.7)
	set("D4", -0.2)

	bilanz := buildBilanz(g, b, 40, ownership, -4.2)

	if bilanz.PrisonersByBlack != 3 || bilanz.PrisonersByWhite != 1 {
		t.Fatalf("Gefangene %d/%d", bilanz.PrisonersByBlack, bilanz.PrisonersByWhite)
	}

	if got := strings.Join(bilanz.DeadBlack, ","); got != "A1,B1" {
		t.Fatalf("tote schwarze Steine %q", got)
	}

	if got := strings.Join(bilanz.DeadWhite, ","); got != "J9" {
		t.Fatalf("tote weiße Steine %q", got)
	}

	if bilanz.Margin != 3.5 {
		t.Fatalf("Margin %v", bilanz.Margin)
	}

	for _, want := range []string{
		"Bis Zug 40 schlug Schwarz 3 weiße und Weiß 1 schwarze Steine",
		"1 weiße und 2 schwarze Steine tot",
		"Gefangene im Sinne der Serverzählung damit 4 für Schwarz und 3 für Weiß",
		"Engine-Schätzung der Endstellung W+4.2",
		"Ergebnis laut SGF W+3.5, Abweichung 0.7 Punkte.",
	} {
		if !strings.Contains(bilanz.Text, want) {
			t.Errorf("Bilanztext ohne %q:\n%s", want, bilanz.Text)
		}
	}

	if len(bilanz.deadPoints(9)) != 3 {
		t.Fatalf("%d tote Punkte", len(bilanz.deadPoints(9)))
	}
}

func TestBilanzOhneZahlImErgebnis(t *testing.T) {
	g := &board.Game{Size: 9, Result: "B+R"}
	b := board.New(9)
	bilanz := buildBilanz(g, b, 12, make([]float64, 81), 0.03)

	if bilanz.Margin != 0 {
		t.Fatalf("Margin %v bei Aufgabe", bilanz.Margin)
	}

	if !strings.HasSuffix(bilanz.Text, "Ergebnis laut SGF B+R.") {
		t.Fatalf("Text: %s", bilanz.Text)
	}

	if !strings.Contains(bilanz.Text, "Endstellung 0.0 (scoreLead") {
		t.Fatalf("Gleichstand nicht als 0.0 geschrieben: %s", bilanz.Text)
	}

	g.Result = ""
	bilanz = buildBilanz(g, b, 12, make([]float64, 81), 12.26)

	if strings.Contains(bilanz.Text, "SGF") || !strings.Contains(bilanz.Text, "B+12.3") {
		t.Fatalf("Text ohne Ergebnis: %s", bilanz.Text)
	}
}
