package teaching

import (
	"testing"

	"github.com/vmanke/goteach-prod/board"
	"github.com/vmanke/goteach-prod/katago"
)

// collectStream sammelt die einzeln ausgelieferten Züge ein.
func collectStream(t *testing.T, g *board.Game, an katago.Analyzer,
	opt Options) ([]MoveReport, *GameReport) {

	t.Helper()

	var got []MoveReport

	report, err := AnalyzeStream(g, an, opt, StreamHandler{
		Move: func(rep *MoveReport) error {
			got = append(got, *rep)

			return nil
		},
	})

	if err != nil {
		t.Fatal(err)
	}

	return got, report
}

// Der Strom muss Zug für Zug dasselbe liefern wie der Sammelweg — sonst
// hinge die Lehre daran, auf welchem Weg sie entstanden ist.
func TestStreamMatchesBatch(t *testing.T) {
	g, err := board.ParseSGF(demoSGF)

	if err != nil {
		t.Fatal(err)
	}

	opt := Options{Visits: 1, Tau: 3.0}
	batch, err := Analyze(g, katago.Mock{}, opt)

	if err != nil {
		t.Fatal(err)
	}

	streamed, report := collectStream(t, g, katago.Mock{}, opt)

	if len(streamed) != len(batch) {
		t.Fatalf("%d Züge gestreamt, %d im Sammelweg", len(streamed), len(batch))
	}

	for i := range batch {
		a, b := batch[i], streamed[i]

		if a.Number != b.Number || a.Coord != b.Coord || a.Category != b.Category {
			t.Fatalf("Zug %d weicht ab:\n%+v\n%+v", i+1, a, b)
		}

		if a.Text != b.Text {
			t.Fatalf("Text zu Zug %d weicht ab:\n%q\n%q", i+1, a.Text, b.Text)
		}

		if a.PointsLost != b.PointsLost || a.WinrateBefore != b.WinrateBefore {
			t.Fatalf("Zahlen zu Zug %d weichen ab:\n%+v\n%+v", i+1, a, b)
		}
	}

	// Der Rückgabewert trägt am Ende die vollständige Partie.
	if len(report.Moves) != len(batch) {
		t.Fatalf("GameReport hat %d Züge, erwartet %d",
			len(report.Moves), len(batch))
	}
}

// shuffledAnalyzer liefert die Stellungen absichtlich in verkehrter
// Reihenfolge — genau das darf die echte Engine tun, weil sie antwortet,
// wie sie fertig wird.
type shuffledAnalyzer struct{ katago.Mock }

func (s shuffledAnalyzer) AnalyzeGameStream(req katago.Request, turns []int,
	emit func(*katago.Result) error) error {

	results, err := s.Mock.AnalyzeGame(req, turns)

	if err != nil {
		return err
	}

	for i := len(results) - 1; i >= 0; i-- {
		if err := emit(results[i]); err != nil {
			return err
		}
	}

	return nil
}

// Auch bei verkehrt eintreffenden Stellungen müssen die Züge in
// Zugreihenfolge und mit unverändertem Text herauskommen.
func TestStreamOrdersOutOfOrderResults(t *testing.T) {
	g, err := board.ParseSGF(demoSGF)

	if err != nil {
		t.Fatal(err)
	}

	opt := Options{Visits: 1, Tau: 3.0}
	batch, err := Analyze(g, katago.Mock{}, opt)

	if err != nil {
		t.Fatal(err)
	}

	streamed, _ := collectStream(t, g, shuffledAnalyzer{}, opt)

	if len(streamed) != len(batch) {
		t.Fatalf("%d Züge gestreamt, %d erwartet", len(streamed), len(batch))
	}

	for i := range streamed {
		if streamed[i].Number != i+1 {
			t.Fatalf("Zug an Position %d trägt Nummer %d — nicht in Reihenfolge",
				i, streamed[i].Number)
		}

		if streamed[i].Text != batch[i].Text {
			t.Fatalf("Text zu Zug %d weicht ab:\n%q\n%q",
				i+1, batch[i].Text, streamed[i].Text)
		}
	}
}

// Der Zugbereich gilt im Strom wie im Sammelweg.
func TestStreamRespectsMoveRange(t *testing.T) {
	g, err := board.ParseSGF(demoSGF)

	if err != nil {
		t.Fatal(err)
	}

	streamed, _ := collectStream(t, g, katago.Mock{},
		Options{Visits: 1, From: 3, To: 5})

	if len(streamed) != 3 {
		t.Fatalf("%d Züge, erwartet 3", len(streamed))
	}

	if streamed[0].Number != 3 || streamed[2].Number != 5 {
		t.Fatalf("Bereich falsch: %d..%d",
			streamed[0].Number, streamed[len(streamed)-1].Number)
	}
}

// Ein Fehler des Empfängers bricht die Analyse ab, statt weiterzurechnen.
func TestStreamStopsOnHandlerError(t *testing.T) {
	g, err := board.ParseSGF(demoSGF)

	if err != nil {
		t.Fatal(err)
	}

	seen := 0
	_, err = AnalyzeStream(g, katago.Mock{}, Options{Visits: 1}, StreamHandler{
		Move: func(*MoveReport) error {
			seen++

			if seen == 2 {
				return errHandlerStop
			}

			return nil
		},
	})

	if err == nil {
		t.Fatal("Fehler des Empfängers wurde verschluckt")
	}

	if seen != 2 {
		t.Fatalf("%d Züge geliefert, erwartet Abbruch nach 2", seen)
	}
}

var errHandlerStop = errStop("Empfänger bricht ab")

type errStop string

func (e errStop) Error() string { return string(e) }
