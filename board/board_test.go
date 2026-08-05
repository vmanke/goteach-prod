package board

import (
	"testing"
)

func mustPlay(t *testing.T, b *Board, c Color, x, y int) {
	t.Helper()

	if err := b.Play(Move{Color: c, Point: Point{x, y}}); err != nil {
		t.Fatalf("Zug %s (%d,%d): %v", c, x, y, err)
	}
}

func TestCapture(t *testing.T) {
	b := New(5)

	// Weiß auf (1,1), Schwarz umzingelt.
	mustPlay(t, b, White, 1, 1)
	mustPlay(t, b, Black, 0, 1)
	mustPlay(t, b, Black, 2, 1)
	mustPlay(t, b, Black, 1, 0)
	mustPlay(t, b, Black, 1, 2)

	if got := b.Get(Point{1, 1}); got != Empty {
		t.Fatalf("weißer Stein nicht geschlagen, Zustand %v", got)
	}

	if b.Captured[White] != 1 {
		t.Fatalf("Captured[White] = %d, erwartet 1", b.Captured[White])
	}
}

func TestSuicideForbidden(t *testing.T) {
	b := New(5)

	mustPlay(t, b, Black, 0, 1)
	mustPlay(t, b, Black, 1, 0)
	mustPlay(t, b, Black, 1, 1)

	// Weiß in die Ecke (0,0) ohne Freiheit = Selbstmord.
	if err := b.Play(Move{Color: White, Point: Point{0, 0}}); err == nil {
		t.Fatal("Selbstmordzug wurde fälschlich erlaubt")
	}

	if b.Get(Point{0, 0}) != Empty {
		t.Fatal("Brett nach illegalem Zug verändert")
	}
}

func TestSimpleKo(t *testing.T) {
	b := New(5)

	// Klassische Ko-Form:
	//   . X O .
	//   X . . O   → Schwarz (2,1)? Aufbau explizit:
	mustPlay(t, b, Black, 1, 0)
	mustPlay(t, b, White, 2, 0)
	mustPlay(t, b, Black, 0, 1)
	mustPlay(t, b, White, 3, 1)
	mustPlay(t, b, Black, 1, 2)
	mustPlay(t, b, White, 2, 2)

	// Weiß setzt den Ko-Stein auf (2,1)? Nein: Schwarz schlägt zuerst.
	mustPlay(t, b, White, 1, 1)

	// Schwarz schlägt (1,1) durch Zug auf (2,1).
	mustPlay(t, b, Black, 2, 1)

	if b.Get(Point{1, 1}) != Empty {
		t.Fatal("Ko-Schlag fehlgeschlagen")
	}

	// Sofortiger Rückschlag durch Weiß auf (1,1) ist Ko-verboten.
	if err := b.Play(Move{Color: White, Point: Point{1, 1}}); err == nil {
		t.Fatal("Ko-Rückschlag wurde fälschlich erlaubt")
	}

	// Nach einem Zug anderswo ist der Rückschlag wieder legal.
	mustPlay(t, b, White, 4, 4)
	mustPlay(t, b, Black, 4, 0)
	mustPlay(t, b, White, 1, 1)
}

const demoSGF = "(;GM[1]FF[4]SZ[19]KM[7.5]RU[Chinese]" +
	";B[pd];W[dp];B[pq];W[dd];B[qk];W[nc];B[pf];W[jd];B[cf];W[ch])"

func TestParseSGF(t *testing.T) {
	g, err := ParseSGF(demoSGF)

	if err != nil {
		t.Fatal(err)
	}

	if g.Size != 19 || g.Komi != 7.5 || g.Rules != "chinese" {
		t.Fatalf("Metadaten falsch: %+v", g)
	}

	if len(g.Moves) != 10 {
		t.Fatalf("%d Züge geparst, erwartet 10", len(g.Moves))
	}

	if got := ToGTP(g.Moves[0].Point, 19); got != "Q16" {
		t.Fatalf("Zug 1 = %s, erwartet Q16", got)
	}

	if _, err := g.Positions(); err != nil {
		t.Fatal(err)
	}
}

func TestGTPRoundTrip(t *testing.T) {
	p := Point{X: 3, Y: 3}
	s := ToGTP(p, 19)

	if s != "D16" {
		t.Fatalf("ToGTP = %s, erwartet D16", s)
	}

	q, pass, err := FromGTP(s, 19)

	if err != nil || pass || q != p {
		t.Fatalf("FromGTP(%s) = %v/%v/%v", s, q, pass, err)
	}
}
