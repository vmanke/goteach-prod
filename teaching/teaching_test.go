package teaching

import (
	"strings"
	"testing"

	"github.com/vmanke/goteach-prod/board"
	"github.com/vmanke/goteach-prod/katago"
)

const demoSGF = "(;GM[1]FF[4]SZ[19]KM[7.5]RU[Chinese]" +
	";B[pd];W[dp];B[pq];W[dd];B[qk];W[nc];B[pf];W[jd];B[cf];W[ch])"

func TestAnalyzeWithMock(t *testing.T) {
	g, err := board.ParseSGF(demoSGF)

	if err != nil {
		t.Fatal(err)
	}

	reports, err := Analyze(g, katago.Mock{}, Options{Visits: 1, Tau: 3.0})

	if err != nil {
		t.Fatal(err)
	}

	if len(reports) != len(g.Moves) {
		t.Fatalf("%d Reports, erwartet %d", len(reports), len(g.Moves))
	}

	for _, r := range reports {
		if r.Category == "" || r.Text == "" {
			t.Fatalf("unvollständiger Report: %+v", r)
		}

		if !strings.Contains(r.Text, "Merksatz:") {
			t.Fatalf("Merksatz fehlt in Zug %d", r.Number)
		}

		if r.WinrateBefore < 0 || r.WinrateBefore > 1 {
			t.Fatalf("Winrate außerhalb [0,1]: %f", r.WinrateBefore)
		}
	}

	// Eigene Kette des gesetzten Steins muss als Effekt auftauchen.
	found := false

	for _, e := range reports[0].Effects {
		if e.Color == "Schwarz" && e.Rep == "Q16" {
			found = true
		}
	}

	if !found {
		t.Fatalf("Effekt der gesetzten Kette fehlt: %+v", reports[0].Effects)
	}
}

func TestAnalyzeRange(t *testing.T) {
	g, err := board.ParseSGF(demoSGF)

	if err != nil {
		t.Fatal(err)
	}

	reports, err := Analyze(g, katago.Mock{},
		Options{Visits: 1, From: 3, To: 5})

	if err != nil {
		t.Fatal(err)
	}

	if len(reports) != 3 || reports[0].Number != 3 || reports[2].Number != 5 {
		t.Fatalf("Bereichsauswahl falsch: %+v", reports)
	}
}
