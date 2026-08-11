package teaching

import (
	"strings"
	"testing"

	"github.com/vmanke/goteach-prod/board"
	"github.com/vmanke/goteach-prod/shapes"
)

// mkBoard baut ein 9×9-Brett aus GTP-Koordinaten (ohne Regelprüfung —
// die Fixtures sind konstruierte Stellungen, keine Partien).
func mkBoard(t *testing.T, black, white []string) *board.Board {
	t.Helper()

	b := board.New(9)

	place := func(coords []string, c board.Color) {
		for _, s := range coords {
			p, pass, err := board.FromGTP(s, 9)

			if err != nil || pass {
				t.Fatalf("Fixture-Koordinate %q: %v", s, err)
			}

			if err := b.SetStone(p, c); err != nil {
				t.Fatal(err)
			}
		}
	}

	place(black, board.Black)
	place(white, board.White)

	return b
}

// play führt einen Zug regelkonform auf einer Kopie aus.
func play(t *testing.T, b *board.Board, mv board.Move) *board.Board {
	t.Helper()

	ab := b.Clone()

	if err := ab.Play(mv); err != nil {
		t.Fatal(err)
	}

	return ab
}

// uniformOwn liefert ein konstantes Ownership-Feld (Schwarz-Sicht).
func uniformOwn(v float64) []float64 {
	own := make([]float64, 81)

	for i := range own {
		own[i] = v
	}

	return own
}

func mustMove(t *testing.T, c board.Color, gtp string) board.Move {
	t.Helper()

	p, pass, err := board.FromGTP(gtp, 9)

	if err != nil || pass {
		t.Fatalf("Koordinate %q: %v", gtp, err)
	}

	return board.Move{Color: c, Point: p}
}

// atariBoard: schwarze Kette E4/E5 mit genau einer Freiheit (E6), zuletzt
// von Weiß E3 bedrängt.
func atariBoard(t *testing.T) (*board.Board, board.Move) {
	t.Helper()

	bb := mkBoard(t,
		[]string{"E4", "E5"},
		[]string{"D5", "F5", "D4", "F4", "E3"})

	return bb, mustMove(t, board.White, "E3")
}

// Atari ignoriert: Der Zug in die leere Ecke ist E, die Erstwahl E6 ist R,
// und die Stellung gilt als dringend.
func TestRoseAtariIgnored(t *testing.T) {
	bb, prev := atariBoard(t)
	mv := mustMove(t, board.Black, "A9")
	ab := play(t, bb, mv)
	own := uniformOwn(0)

	facts, d := assessRose(mv, &prev, 9, bb, ab, own, own, 3.0,
		"E6", false, 40, 20)

	if !facts.Urgent {
		t.Fatal("Atari auf dem Brett, aber Urgent == false")
	}

	if facts.Played != "E" || facts.Best != "R" {
		t.Fatalf("Played/Best = %s/%s, erwartet E/R", facts.Played, facts.Best)
	}

	top := topFinding(d.findings)

	if top == nil || top.bucket != roseR || !top.atari {
		t.Fatalf("R-Atari-Befund fehlt: %+v", d.findings)
	}

	if d.prevCoord != "E3" {
		t.Fatalf("prevCoord = %q, erwartet E3", d.prevCoord)
	}
}

// Atari beantwortet: Herausziehen auf E6 ist R und lindert die Not.
func TestRoseAtariAnswered(t *testing.T) {
	bb, prev := atariBoard(t)
	mv := mustMove(t, board.Black, "E6")
	ab := play(t, bb, mv)
	own := uniformOwn(0)

	facts, d := assessRose(mv, &prev, 9, bb, ab, own, own, 3.0,
		"E6", true, 40, 20)

	if facts.Played != "R" {
		t.Fatalf("Played = %s, erwartet R", facts.Played)
	}

	if !d.answered {
		t.Fatal("E6 zieht die Kette heraus, answered == false")
	}
}

// Pass unter Atari: Stufe E, aber die Dringlichkeit bleibt gemeldet.
func TestRosePassUnderAtari(t *testing.T) {
	bb, prev := atariBoard(t)
	mv := board.Move{Color: board.Black, Pass: true}
	own := uniformOwn(0)

	facts, d := assessRose(mv, &prev, 9, bb, bb, own, own, 3.0,
		"", false, 40, 20)

	if facts.Played != "E" || !facts.Urgent {
		t.Fatalf("Pass unter Atari: Played=%s Urgent=%t, erwartet E/true",
			facts.Played, facts.Urgent)
	}

	if top := topFinding(d.findings); top == nil || top.bucket != roseR {
		t.Fatalf("R-Befund fehlt trotz Atari: %+v", d.findings)
	}
}

// Schwache Gegnerkette: Der Angriffszug daneben ist O.
func TestRoseWeakEnemyChainIsO(t *testing.T) {
	bb := mkBoard(t,
		[]string{"F7", "F6", "H7", "H6"},
		[]string{"G7", "G6"})
	prev := mustMove(t, board.White, "G6")
	mv := mustMove(t, board.Black, "G5")
	ab := play(t, bb, mv)

	// +0.3 aus Schwarz-Sicht: die weiße Kette ist schwach (−0.3), die
	// schwarzen Ketten sind mit +0.3 über der Schwäche-Schwelle.
	own := uniformOwn(0.3)

	facts, d := assessRose(mv, &prev, 9, bb, ab, own, own, 3.0,
		"", false, 40, 20)

	if facts.Played != "O" {
		t.Fatalf("Played = %s, erwartet O", facts.Played)
	}

	if facts.Urgent {
		t.Fatal("kein R-Befund konstruiert, Urgent trotzdem true")
	}

	top := topFinding(d.findings)

	if top == nil || top.bucket != roseO || top.color != "Weiß" {
		t.Fatalf("O-Befund zur weißen Kette fehlt: %+v", d.findings)
	}
}

// Schwache eigene Kette verstärkt: Der Zug ist S und hat geholfen.
func TestRoseReinforceOwnBaseIsS(t *testing.T) {
	bb := mkBoard(t,
		[]string{"C3", "C4"},
		[]string{"C5", "B3", "D3"})
	prev := mustMove(t, board.White, "C5")
	mv := mustMove(t, board.Black, "B4")
	ab := play(t, bb, mv)

	// −0.1: die schwarze Kette ist schwach; die weißen Einzelsteine sind
	// als Leichtgewichte ohnehin keine Befunde.
	own := uniformOwn(-0.1)

	facts, d := assessRose(mv, &prev, 9, bb, ab, own, own, 3.0,
		"", false, 40, 20)

	if facts.Played != "S" {
		t.Fatalf("Played = %s, erwartet S", facts.Played)
	}

	if !d.helpedS {
		t.Fatal("B4 erhöht die Freiheiten der Basis, helpedS == false")
	}
}

// Leere Gegend ohne Befunde: E mit offenem Umfeld; weit weg vom Vorzug
// ist der Zug zugleich Tenuki.
func TestRoseEmptyAreaIsE(t *testing.T) {
	bb := mkBoard(t, []string{"E5"}, []string{"G7"})
	prev := mustMove(t, board.White, "G7")
	mv := mustMove(t, board.Black, "C3")
	ab := play(t, bb, mv)
	own := uniformOwn(0)

	facts, d := assessRose(mv, &prev, 9, bb, ab, own, own, 3.0,
		"", false, 20, 3)

	if facts.Played != "E" || facts.Urgent {
		t.Fatalf("Played=%s Urgent=%t, erwartet E/false",
			facts.Played, facts.Urgent)
	}

	if len(d.findings) != 0 {
		t.Fatalf("keine Befunde erwartet: %+v", d.findings)
	}

	if !d.openArea || !d.tenuki {
		t.Fatalf("openArea=%t tenuki=%t, erwartet true/true",
			d.openArea, d.tenuki)
	}

	if d.phase != "Eröffnung" {
		t.Fatalf("Phase = %q, erwartet Eröffnung (Zug 3/20)", d.phase)
	}
}

// Neues leeres Dreieck des Ziehenden wird als Formfehler erkannt.
func TestRoseEmptyTriangleIsShapeFault(t *testing.T) {
	bb := mkBoard(t, []string{"E5", "E4"}, nil)
	mv := mustMove(t, board.Black, "D4")
	ab := play(t, bb, mv)
	own := uniformOwn(0)

	_, d := assessRose(mv, nil, 9, bb, ab, own, own, 3.0,
		"", false, 40, 20)

	if d.shapeBad == nil || d.shapeBad.Name != "leeres Dreieck" {
		t.Fatalf("leeres Dreieck nicht erkannt: %+v", d.shapeBad)
	}
}

// tacticOn ordnet ein gelesenes Motiv der richtigen Kette zu und
// übergeht Schnapp (der warnt vor dem Schlagen, er begründet keinen
// Angriff).
func TestTacticOn(t *testing.T) {
	tactics := []shapes.Instance{
		{Name: "Schnapp", Color: "Weiß", Points: []string{"Q10"}},
		{Name: "Leiter", Color: "Weiß", Points: []string{"Q10", "R10"},
			Teaching: "Die Leiter läuft für Schwarz."},
	}

	got := tacticOn(tactics, "Weiß", "Q10")

	if got == nil || got.Name != "Leiter" {
		t.Fatalf("tacticOn = %+v, erwartet die Leiter", got)
	}

	if tacticOn(tactics, "Schwarz", "Q10") != nil {
		t.Fatal("Farbfilter greift nicht")
	}

	if tacticOn(tactics, "Weiß", "A1") != nil {
		t.Fatal("Punktfilter greift nicht")
	}
}

// Der Lehrsatz eines gelesenen Motivs wandert wörtlich in den Befund —
// er stammt aus exakter Variantensuche und ist damit belegt.
func TestDemandSentenceUsesTacticTeaching(t *testing.T) {
	f := &roseFinding{
		bucket: roseO,
		rep:    "Q10",
		color:  "Weiß",
		tactic: &shapes.Instance{
			Name:     "Leiter",
			Color:    "Weiß",
			Teaching: "Die Leiter läuft für Schwarz — jeder Fluchtzug endet im Atari.",
		},
	}

	got := demandSentence(f, "")

	want := "Die weiße Kette um Q10 stand in einem Leiter: die Leiter läuft " +
		"für Schwarz — jeder Fluchtzug endet im Atari."

	if got != want {
		t.Fatalf("Befundsatz:\n%q\nerwartet:\n%q", got, want)
	}
}

// Ein Befund ohne Motiv nennt die Zahlen, die ihn tragen — und verzichtet
// auf Etikett und Wertung.
func TestDemandSentenceCarriesItsNumbers(t *testing.T) {
	f := &roseFinding{
		bucket:   roseO,
		rep:      "Q10",
		color:    "Weiß",
		libs:     3,
		strength: -0.001,
	}

	got := demandSentence(f, "")

	if !strings.Contains(got, "3 Freiheiten") {
		t.Errorf("Freiheiten fehlen: %q", got)
	}

	if strings.Contains(got, "-0.00") {
		t.Errorf("negative Null im Befund: %q", got)
	}

	for _, banned := range []string{"O wie", "schwach", "dringlichste"} {
		if strings.Contains(got, banned) {
			t.Errorf("Befund trägt %q: %q", banned, got)
		}
	}
}
