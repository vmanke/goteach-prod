package teaching

import (
	"sort"
	"testing"

	"github.com/vmanke/goteach-prod/katago"
)

// countingAnalyzer zählt, welche Turns mit welcher Visit-Zahl angefragt
// wurden — die Rückkopplung ist genau dann richtig, wenn die zweite Runde
// nur die Stellungen der stärksten Stränge berührt.
type countingAnalyzer struct {
	inner  denseAnalyzer
	rounds []round
}

type round struct {
	visits int
	turns  []int
}

func (c *countingAnalyzer) Close() error { return nil }

func (c *countingAnalyzer) AnalyzeGame(req katago.Request, turns []int) (
	[]*katago.Result, error) {

	copied := append([]int(nil), turns...)
	sort.Ints(copied)
	c.rounds = append(c.rounds, round{visits: req.MaxVisits, turns: copied})

	return c.inner.AnalyzeGame(req, turns)
}

func TestRueckkopplungRechnetNurDieStaerkstenStraengeNach(t *testing.T) {
	g := syntheticGame(t, 19, 140)
	counter := &countingAnalyzer{}

	report, err := AnalyzeGame(g, counter, Options{
		Visits:       10,
		Tau:          3.0,
		RefineVisits: 100,
		RefineTop:    2,
	})

	if err != nil {
		t.Fatalf("AnalyzeGame: %v", err)
	}

	if len(counter.rounds) != 2 {
		t.Fatalf("%d Analyse-Runden, erwartet 2", len(counter.rounds))
	}

	first, second := counter.rounds[0], counter.rounds[1]

	if first.visits != 10 || second.visits != 100 {
		t.Fatalf("Visits %d dann %d, erwartet 10 dann 100",
			first.visits, second.visits)
	}

	// Die zweite Runde muss deutlich kleiner sein — sonst wäre nichts
	// gespart und die Rückkopplung sinnlos.
	if len(second.turns) >= len(first.turns) {
		t.Fatalf("zweite Runde umfasst %d von %d Stellungen — keine Ersparnis",
			len(second.turns), len(first.turns))
	}

	// Jede nachgerechnete Stellung muss zu einem der beiden stärksten
	// Stränge gehören.
	allowed := map[int]bool{}

	top := report.Strands

	if len(top) > 2 {
		top = top[:2]
	}

	for _, s := range top {
		for _, number := range s.Moves {
			allowed[number-1] = true
			allowed[number] = true
		}
	}

	for _, turn := range second.turns {
		if !allowed[turn] {
			t.Errorf("Turn %d nachgerechnet, gehört aber zu keinem Top-Strang", turn)
		}
	}
}

func TestOhneRefineVisitsBleibtEsBeiEinerRunde(t *testing.T) {
	g := syntheticGame(t, 19, 80)
	counter := &countingAnalyzer{}

	if _, err := AnalyzeGame(g, counter, Options{Visits: 10, Tau: 3.0}); err != nil {
		t.Fatalf("AnalyzeGame: %v", err)
	}

	if len(counter.rounds) != 1 {
		t.Fatalf("%d Runden, erwartet 1 ohne RefineVisits", len(counter.rounds))
	}
}

func TestVerfeinerungAendertDieStrangBilanz(t *testing.T) {
	// Die Bilanz eines Strangs muss nach der zweiten Runde weiterhin exakt
	// die Summe seiner Züge sein — sonst hätte die Verfeinerung die Zahlen
	// und die Zuordnung auseinanderlaufen lassen.
	g := syntheticGame(t, 19, 140)

	report, err := AnalyzeGame(g, denseAnalyzer{}, Options{
		Visits:       10,
		Tau:          3.0,
		RefineVisits: 60,
	})

	if err != nil {
		t.Fatalf("AnalyzeGame: %v", err)
	}

	byNumber := map[int]*MoveReport{}

	for i := range report.Moves {
		byNumber[report.Moves[i].Number] = &report.Moves[i]
	}

	for _, s := range report.Strands {
		expected := map[string]float64{}

		for _, number := range s.Moves {
			if rep := byNumber[number]; rep != nil {
				expected[rep.Player] += rep.PointsLost
			}
		}

		for player, want := range expected {
			if diff := s.PointsLost[player] - want; diff > 1e-9 || diff < -1e-9 {
				t.Errorf("Strang %d, %s: %v statt %v",
					s.ID, player, s.PointsLost[player], want)
			}
		}
	}
}
