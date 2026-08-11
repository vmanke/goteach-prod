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

		if r.Rose == nil || r.Rose.Played == "" {
			t.Fatalf("ROSE-Einstufung fehlt in Zug %d", r.Number)
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

// stubAnalyzer liefert Ergebnisse ohne MoveInfos: Result.Best() gibt dann nil
// zurück und BestMove bleibt leer. ScoreLead fällt pro Halbzug um 20 Punkte,
// damit der Merksatz-Zweig "PointsLost > 6" für Schwarz sicher greift.
type stubAnalyzer struct{}

func (stubAnalyzer) Close() error { return nil }

func (stubAnalyzer) AnalyzeGame(req katago.Request, turns []int) ([]*katago.Result, error) {
	out := make([]*katago.Result, 0, len(turns))

	for _, t := range turns {
		out = append(out, &katago.Result{
			TurnNumber: t,
			MoveInfos:  nil,
			RootInfo: katago.RootInfo{
				Winrate:   0.5,
				ScoreLead: -20.0 * float64(t),
				Visits:    1,
			},
			Ownership: make([]float64, req.Size*req.Size),
		})
	}

	return out, nil
}

// Ohne BestMove darf kein halber Satz entstehen ("... vergleichen Sie Ihren
// Zug mit .").
func TestLessonWithoutBestMove(t *testing.T) {
	g, err := board.ParseSGF(demoSGF)

	if err != nil {
		t.Fatal(err)
	}

	reports, err := Analyze(g, stubAnalyzer{}, Options{Visits: 1, To: 2})

	if err != nil {
		t.Fatal(err)
	}

	first := reports[0]

	if first.BestMove != "" {
		t.Fatalf("Testaufbau falsch: BestMove = %q, erwartet leer", first.BestMove)
	}

	if first.PointsLost <= 6 {
		t.Fatalf("Testaufbau falsch: PointsLost = %.2f, erwartet > 6",
			first.PointsLost)
	}

	for _, r := range reports {
		if strings.Contains(r.Text, "mit .") {
			t.Fatalf("abgeschnittener Satz in Zug %d:\n%s", r.Number, r.Text)
		}

		if strings.Contains(r.Text, "Engine-Erstwahl") {
			t.Fatalf("Engine-Erstwahl ohne BestMove in Zug %d:\n%s",
				r.Number, r.Text)
		}
	}
}

// passBestAnalyzer nennt "pass" als Engine-Erstwahl.
type passBestAnalyzer struct{}

func (passBestAnalyzer) Close() error { return nil }

func (passBestAnalyzer) AnalyzeGame(req katago.Request, turns []int) ([]*katago.Result, error) {
	out := make([]*katago.Result, 0, len(turns))

	for _, t := range turns {
		out = append(out, &katago.Result{
			TurnNumber: t,
			MoveInfos: []katago.MoveInfo{{
				Move:   "pass",
				Visits: 1,
				Order:  0,
				PV:     []string{"pass"},
			}},
			RootInfo:  katago.RootInfo{Winrate: 0.5, ScoreLead: 0, Visits: 1},
			Ownership: make([]float64, req.Size*req.Size),
		})
	}

	return out, nil
}

// Ist Pass die Engine-Erstwahl und wird gepasst, gilt der Zug als Treffer:
// kein redundanter "Engine-Erstwahl: pass"-Hinweis.
func TestPassMatchesEngineBest(t *testing.T) {
	g, err := board.ParseSGF("(;GM[1]FF[4]SZ[9]KM[7.5];B[];W[ee])")

	if err != nil {
		t.Fatal(err)
	}

	reports, err := Analyze(g, passBestAnalyzer{}, Options{Visits: 1})

	if err != nil {
		t.Fatal(err)
	}

	pass := reports[0]

	if !pass.Pass {
		t.Fatalf("Zug 1 ist kein Pass: %+v", pass)
	}

	if strings.Contains(pass.Text, "Engine-Erstwahl") {
		t.Fatalf("Pass deckt sich mit der Engine-Erstwahl, "+
			"Hinweis trotzdem gesetzt:\n%s", pass.Text)
	}

	if pass.Category != "ausgezeichnet" {
		t.Fatalf("Kategorie = %q, erwartet ausgezeichnet", pass.Category)
	}
}
