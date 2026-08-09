package teaching

import (
	"math"
	"testing"

	"github.com/vmanke/goteach-prod/board"
	"github.com/vmanke/goteach-prod/katago"
)

// denseAnalyzer erzeugt Ownership als Summe über *alle* Steine statt über den
// jeweils nächstgelegenen.
//
// Der Unterschied ist für diese Tests wesentlich. katago.Mock benutzt die
// Distanz zum nächsten Stein; auf einem dicht besetzten Brett sättigt dieses
// Feld und ändert sich mit einem weiteren Stein kaum noch — die Salienzspuren
// bestehen dann aus ein bis vier Ausschlägen und tragen keine Zeitreihe.
// KataGos Ownership ist dagegen eine Netzausgabe über das ganze Brett, die
// sich mit jedem Zug überall ein wenig verschiebt. Genau das bildet dieser
// Analyzer nach; geprüft wird hier die Strangbildung, nicht das Mock-Modell.
type denseAnalyzer struct{}

func (denseAnalyzer) Close() error { return nil }

func (d denseAnalyzer) AnalyzeGame(req katago.Request, turns []int) (
	[]*katago.Result, error) {

	out := make([]*katago.Result, 0, len(turns))

	for _, turn := range turns {
		b := board.New(req.Size)

		for i, m := range req.Moves {
			if i >= turn {
				break
			}

			colour := board.Black

			if m[0] == "W" {
				colour = board.White
			}

			point, pass, err := board.FromGTP(m[1], req.Size)

			if err != nil || pass {
				continue
			}

			if err := b.SetStone(point, colour); err != nil {
				return nil, err
			}
		}

		out = append(out, d.evaluate(req, b, turn))
	}

	return out, nil
}

func (denseAnalyzer) evaluate(req katago.Request, b *board.Board,
	turn int) *katago.Result {

	n := req.Size * req.Size
	own := make([]float64, n)
	var total float64

	for y := 0; y < req.Size; y++ {
		for x := 0; x < req.Size; x++ {
			var sum float64

			for _, colour := range []board.Color{board.Black, board.White} {
				sign := 1.0

				if colour == board.White {
					sign = -1.0
				}

				for _, s := range b.Stones(colour) {
					d := math.Abs(float64(s.X-x)) + math.Abs(float64(s.Y-y))
					sum += sign * math.Exp(-d/3.5)
				}
			}

			own[y*req.Size+x] = math.Tanh(sum)
			total += own[y*req.Size+x]
		}
	}

	return &katago.Result{
		TurnNumber: turn,
		RootInfo: katago.RootInfo{
			Winrate:   0.5 + 0.45*math.Tanh(total/40.0),
			ScoreLead: 0.5*total - req.Komi,
			Visits:    1,
		},
		Ownership: own,
	}
}

// syntheticGame baut eine Partie, deren Steine sich nie berühren: Schwarz
// steht auf Punkten mit (x+y) mod 4 == 0, Weiß auf (x+y) mod 4 == 2.
// Benachbarte Punkte unterscheiden sich in x+y um genau eins, also kann
// keine der beiden Mengen mit sich selbst oder der anderen zusammenstoßen —
// es wird nie geschlagen, jeder Zug ist legal.
//
// Die Züge laufen zuerst in der oberen linken, dann in der unteren rechten
// Bretthälfte. Diese zeitliche Trennung ist der Punkt: Formen derselben
// Gegend sollen miteinander laufen, Formen verschiedener Gegenden nicht.
func syntheticGame(t *testing.T, size, count int) *board.Game {
	t.Helper()

	g := &board.Game{Size: size, Komi: 7.5, Rules: "chinese"}

	var early, late []board.Move

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var colour board.Color

			switch (x + y) % 4 {
			case 0:
				colour = board.Black
			case 2:
				colour = board.White
			default:
				continue
			}

			move := board.Move{Color: colour, Point: board.Point{X: x, Y: y}}

			if x+y < size {
				early = append(early, move)
			} else {
				late = append(late, move)
			}
		}
	}

	ordered := append(alternate(early), alternate(late)...)

	if len(ordered) > count {
		ordered = ordered[:count]
	}

	g.Moves = ordered

	return g
}

// alternate ordnet Züge so, dass die Farben sich abwechseln — sonst wäre es
// keine Partie, sondern eine Aufstellung.
func alternate(moves []board.Move) []board.Move {
	var black, white []board.Move

	for _, m := range moves {
		if m.Color == board.Black {
			black = append(black, m)
		} else {
			white = append(white, m)
		}
	}

	var out []board.Move

	for i := 0; i < len(black) || i < len(white); i++ {
		if i < len(black) {
			out = append(out, black[i])
		}

		if i < len(white) {
			out = append(out, white[i])
		}
	}

	return out
}

func analyzeSynthetic(t *testing.T, moves int) *GameReport {
	t.Helper()

	g := syntheticGame(t, 19, moves)
	report, err := AnalyzeGame(g, denseAnalyzer{}, Options{Visits: 10, Tau: 3.0})

	if err != nil {
		t.Fatalf("AnalyzeGame: %v", err)
	}

	return report
}

func TestAnalyzeGameLiefertZuegeUndStraenge(t *testing.T) {
	report := analyzeSynthetic(t, 140)

	if len(report.Moves) != 140 {
		t.Fatalf("%d Zug-Reports, erwartet 140", len(report.Moves))
	}

	if len(report.Strands) == 0 {
		t.Fatal("keine Erzählstränge gefunden")
	}

	for _, s := range report.Strands {
		if s.ID == 0 {
			t.Error("Strang ohne ID")
		}

		if len(s.Moves) < minStrandMoves {
			t.Errorf("Strang %d hat nur %d Züge", s.ID, len(s.Moves))
		}

		if s.Text == "" {
			t.Errorf("Strang %d ohne Lehrtext", s.ID)
		}

		if len(s.Shapes) == 0 {
			t.Errorf("Strang %d ohne benannte Formen", s.ID)
		}

		if s.FromMove > s.ToMove {
			t.Errorf("Strang %d: Bereich %d..%d verkehrt", s.ID, s.FromMove, s.ToMove)
		}
	}
}

func TestJederZugGehoertHoechstensEinemStrang(t *testing.T) {
	report := analyzeSynthetic(t, 140)
	owner := map[int]int{}

	for _, s := range report.Strands {
		for _, number := range s.Moves {
			if previous, taken := owner[number]; taken {
				t.Fatalf("Zug %d gehört zu Strang %d und %d",
					number, previous, s.ID)
			}

			owner[number] = s.ID
		}
	}

	if len(owner) == 0 {
		t.Fatal("kein einziger Zug wurde zugeordnet")
	}

	// Die Zuordnung ist bewusst nicht vollständig: Züge weitab jeder
	// erkannten Form gehören zu keiner Geschichte.
	if len(owner) > len(report.Moves) {
		t.Fatalf("%d zugeordnete Züge bei %d Zügen", len(owner), len(report.Moves))
	}
}

func TestStrangBilanzStimmtMitDenZuegen(t *testing.T) {
	report := analyzeSynthetic(t, 140)

	byNumber := map[int]*MoveReport{}

	for i := range report.Moves {
		byNumber[report.Moves[i].Number] = &report.Moves[i]
	}

	for _, s := range report.Strands {
		expected := map[string]float64{}

		for _, number := range s.Moves {
			rep := byNumber[number]

			if rep == nil {
				t.Fatalf("Strang %d verweist auf unbekannten Zug %d", s.ID, number)
			}

			expected[rep.Player] += rep.PointsLost
		}

		for player, want := range expected {
			got := s.PointsLost[player]

			if diff := got - want; diff > 1e-9 || diff < -1e-9 {
				t.Errorf("Strang %d, %s: Bilanz %v, erwartet %v",
					s.ID, player, got, want)
			}
		}
	}
}

func TestStraengeSindReproduzierbar(t *testing.T) {
	first := analyzeSynthetic(t, 140)
	second := analyzeSynthetic(t, 140)

	if len(first.Strands) != len(second.Strands) {
		t.Fatalf("%d vs %d Stränge", len(first.Strands), len(second.Strands))
	}

	for i := range first.Strands {
		a, b := first.Strands[i], second.Strands[i]

		if a.ID != b.ID || a.Area != b.Area || a.Text != b.Text {
			t.Fatalf("Strang %d weicht ab:\n%q\n%q", i, a.Text, b.Text)
		}

		if len(a.Moves) != len(b.Moves) {
			t.Fatalf("Strang %d: %d vs %d Züge", i, len(a.Moves), len(b.Moves))
		}
	}
}

func TestAnalyzeBleibtOhneStraenge(t *testing.T) {
	// Die alte Schnittstelle darf sich nicht verändert haben.
	g := syntheticGame(t, 19, 40)
	reports, err := Analyze(g, katago.Mock{}, Options{Visits: 10})

	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if len(reports) != 40 {
		t.Fatalf("%d Reports, erwartet 40", len(reports))
	}
}

func TestGegendenWerdenBenannt(t *testing.T) {
	cases := []struct {
		name   string
		points []board.Point
		want   string
	}{
		{"oben links", []board.Point{{X: 2, Y: 2}, {X: 3, Y: 3}}, "oben links"},
		{"unten rechts", []board.Point{{X: 16, Y: 16}, {X: 15, Y: 15}}, "unten rechts"},
		{"Zentrum", []board.Point{{X: 9, Y: 9}, {X: 10, Y: 10}}, "im Zentrum"},
		{"rechter Rand", []board.Point{{X: 17, Y: 9}, {X: 16, Y: 10}}, "am rechten Rand"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := areaName(19, tc.points); got != tc.want {
				t.Fatalf("areaName = %q, erwartet %q", got, tc.want)
			}
		})
	}
}

func TestWeitVerteilterStrangWirdSoBenannt(t *testing.T) {
	region := []board.Point{{X: 1, Y: 1}, {X: 17, Y: 17}}

	if got := areaName(19, region); got != "über das ganze Brett verteilt" {
		t.Fatalf("areaName = %q", got)
	}
}
