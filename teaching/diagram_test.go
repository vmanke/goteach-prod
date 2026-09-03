package teaching

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/vmanke/goteach-prod/board"
)

func TestAusschnittWirdAnDerKanteVerlaengert(t *testing.T) {
	cases := []struct {
		coord  string
		margin int
		want   crop
	}{
		// Mitten im Brett: ±4 ohne Beschnitt.
		{"K10", 4, crop{x0: 5, x1: 13, y0: 5, y1: 13}},
		// Ecke oben rechts: rechts und oben beschnitten, auf sieben
		// Linien nach links und unten verlängert.
		{"S19", 4, crop{x0: 12, x1: 18, y0: 0, y1: 6}},
		// Unten in der Mitte: nur die Zeilen sind beschnitten.
		{"G2", 4, crop{x0: 2, x1: 10, y0: 12, y1: 18}},
		// Linker Rand: die Spalten reichen bis G.
		{"A8", 4, crop{x0: 0, x1: 6, y0: 7, y1: 15}},
	}

	for _, tc := range cases {
		p, _, err := board.FromGTP(tc.coord, 19)

		if err != nil {
			t.Fatal(err)
		}

		if got := cropAround(19, []board.Point{p}, tc.margin); got != tc.want {
			t.Errorf("%s: %+v, erwartet %+v", tc.coord, got, tc.want)
		}
	}
}

func TestAusschnittUmMehrereSaatpunkte(t *testing.T) {
	seeds := []board.Point{{X: 0, Y: 18}, {X: 3, Y: 10}} // A1 und D9

	got := cropAround(19, seeds, 2)
	want := crop{x0: 0, x1: 6, y0: 8, y1: 18}

	if got != want {
		t.Fatalf("%+v, erwartet %+v", got, want)
	}

	if full := cropAround(19, nil, 2); full != (crop{0, 18, 0, 18}) {
		t.Fatalf("ohne Saat: %+v", full)
	}
}

func TestTitelzeilenBrechenAnDerBreite(t *testing.T) {
	narrow := 2*svgMargin + 6*svgCell // sieben Spalten
	wide := 2*svgMargin + 8*svgCell   // neun Spalten

	cases := []struct {
		width    int
		segments []string
		want     []string
	}{
		{narrow, []string{"Baustelle 2", "C2 für Schwarz", "Züge 105 bis 205"},
			[]string{"Baustelle 2", "C2 für Schwarz", "Züge 105 bis 205"}},
		{narrow, []string{"Baustelle 6", "R19 für Weiß", "Züge 206 bis 237"},
			[]string{"Baustelle 6, R19 für Weiß", "Züge 206 bis 237"}},
		{wide, []string{"Baustelle 1", "G2 für Schwarz", "Züge 61 bis 101"},
			[]string{"Baustelle 1, G2 für Schwarz", "Züge 61 bis 101"}},
		{narrow, []string{"Erzählstrang 3", "unten links", "Züge 4 bis 280"},
			[]string{"Erzählstrang 3", "unten links, Züge 4 bis 280"}},
		{2*svgMargin + 11*svgCell, []string{"Erzählstrang 2", "unten links", "Züge 5 bis 285"},
			[]string{"Erzählstrang 2, unten links, Züge 5 bis 285"}},
	}

	for _, tc := range cases {
		got := titleSegments(tc.segments, tc.width)

		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%v bei %d px: %v, erwartet %v", tc.segments, tc.width, got, tc.want)
		}
	}
}

func TestDiagrammIstWohlgeformtUndMassgenau(t *testing.T) {
	b := board.New(19)
	s19, _, _ := board.FromGTP("S19", 19)
	r19, _, _ := board.FromGTP("R19", 19)
	p18, _, _ := board.FromGTP("P18", 19)

	if err := b.SetStone(s19, board.Black); err != nil {
		t.Fatal(err)
	}

	if err := b.SetStone(p18, board.White); err != nil {
		t.Fatal(err)
	}

	d := diagram{
		size:     19,
		crop:     cropAround(19, []board.Point{r19}, 4),
		title:    []string{"Baustelle 6", "R19 für Weiß", "Züge 206 bis 237"},
		board:    b,
		labels:   map[board.Point]int{s19: 237, p18: 199},
		ring:     &r19,
		ringText: "R19",
		crosses:  []board.Point{p18},
	}

	svg := d.svg()

	if err := xml.Unmarshal([]byte(svg), new(struct{})); err != nil {
		t.Fatalf("SVG nicht wohlgeformt: %v", err)
	}

	// Sieben Spalten, sieben Zeilen, zwei Titelzeilen.
	for _, want := range []string{
		`width="216" height="252"`,
		`>Baustelle 6, R19 für Weiß<`,
		`>Züge 206 bis 237<`,
		`fill="#fff">237<`,
		`fill="#111">199<`,
		`fill="#d00">R19<`,
		`fill="none" stroke="#d00" stroke-width="2"`,
		`stroke="#d00" stroke-width="2"/><line`,
		`>N<`, `>T<`, `>19<`, `>13<`,
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("SVG ohne %q", want)
		}
	}

	if strings.Contains(svg, `>M<`) || strings.Contains(svg, `>12<`) {
		t.Error("SVG zeigt Linien außerhalb des Ausschnitts")
	}
}

func TestZugnummernFolgenDerStellung(t *testing.T) {
	g := &board.Game{Size: 9}
	a1, _, _ := board.FromGTP("A1", 9)
	b1, _, _ := board.FromGTP("B1", 9)
	a2, _, _ := board.FromGTP("A2", 9)
	e5, _, _ := board.FromGTP("E5", 9)

	// Weiß A1 wird von Schwarz mit B1 und A2 geschlagen; danach setzt Weiß
	// erneut auf A1 (Zug 5), diesmal ohne Schlag, weil E5 dazwischen liegt.
	g.Moves = []board.Move{
		{Color: board.White, Point: a1},
		{Color: board.Black, Point: b1},
		{Color: board.White, Point: e5},
		{Color: board.Black, Point: a2},
	}

	positions, err := g.Positions()

	if err != nil {
		t.Fatal(err)
	}

	final := positions[4]

	if final.Get(a1) != board.Empty {
		t.Fatal("A1 wurde nicht geschlagen")
	}

	labels := moveLabels(g, 4, final, func(int) bool { return true })

	if _, ok := labels[a1]; ok {
		t.Error("geschlagener Stein trägt eine Nummer")
	}

	if labels[b1] != 2 || labels[a2] != 4 || labels[e5] != 3 {
		t.Errorf("Nummern %v", labels)
	}

	only := moveLabels(g, 4, final, func(n int) bool { return n == 2 })

	if len(only) != 1 || only[b1] != 2 {
		t.Errorf("Auswahl %v", only)
	}
}
