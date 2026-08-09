package vision

import (
	"strings"
	"testing"

	"github.com/vmanke/goteach-prod/board"
)

func TestFromJSONLeitetGroesseAusZeilenAb(t *testing.T) {
	p, err := FromJSON([]byte(`{"rows":["...","...","..."]}`))

	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}

	if p.Size != 3 {
		t.Fatalf("Size = %d, erwartet 3", p.Size)
	}
}

func TestFromJSONPrueftZeilenzahl(t *testing.T) {
	_, err := FromJSON([]byte(`{"size":9,"rows":["........."]}`))

	if err == nil {
		t.Fatal("Fehler erwartet: 1 Zeile bei size 9")
	}

	if !strings.Contains(err.Error(), "erwartet") {
		t.Fatalf("unerwartete Meldung: %v", err)
	}
}

func TestFromJSONMeldetKaputtesJSON(t *testing.T) {
	if _, err := FromJSON([]byte(`{`)); err == nil {
		t.Fatal("Fehler bei kaputtem JSON erwartet")
	}
}

func TestBoardAkzeptiertBeideZeichensaetze(t *testing.T) {
	// 'X'/'B' schwarz, 'O'/'W' weiß, groß wie klein.
	p := &Position{Size: 4, Rows: []string{"XB..", "OW..", "xb..", "ow.."}}
	b, err := p.Board()

	if err != nil {
		t.Fatalf("Board: %v", err)
	}

	black := []board.Point{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 0, Y: 2}, {X: 1, Y: 2}}
	white := []board.Point{{X: 0, Y: 1}, {X: 1, Y: 1}, {X: 0, Y: 3}, {X: 1, Y: 3}}

	for _, pt := range black {
		if got := b.Get(pt); got != board.Black {
			t.Errorf("%v = %v, erwartet Schwarz", pt, got)
		}
	}

	for _, pt := range white {
		if got := b.Get(pt); got != board.White {
			t.Errorf("%v = %v, erwartet Weiß", pt, got)
		}
	}

	if got := b.Get(board.Point{X: 3, Y: 0}); got != board.Empty {
		t.Errorf("(3,0) = %v, erwartet leer", got)
	}
}

func TestBoardIgnoriertLeerzeichen(t *testing.T) {
	p := &Position{Size: 3, Rows: []string{". . .", "X . O", ". . ."}}

	if _, err := p.Board(); err != nil {
		t.Fatalf("Board: %v", err)
	}
}

func TestBoardMeldetFalscheZeilenlaenge(t *testing.T) {
	p := &Position{Size: 3, Rows: []string{"...", "..", "..."}}

	if _, err := p.Board(); err == nil {
		t.Fatal("Fehler bei zu kurzer Zeile erwartet")
	}
}

func TestBoardMeldetUnbekanntesZeichen(t *testing.T) {
	p := &Position{Size: 3, Rows: []string{"...", ".?.", "..."}}
	_, err := p.Board()

	if err == nil {
		t.Fatal("Fehler bei unbekanntem Zeichen erwartet")
	}

	if !strings.Contains(err.Error(), "unbekanntes Zeichen") {
		t.Fatalf("unerwartete Meldung: %v", err)
	}
}

func TestZeileNullIstDieObersteBrettzeile(t *testing.T) {
	// Ein einzelner Stein oben links muss auf A19 landen, nicht auf A1 —
	// dieselbe Ordnung, in der KataGo Ownership liefert.
	rows := make([]string, 19)

	for i := range rows {
		rows[i] = strings.Repeat(".", 19)
	}

	rows[0] = "X" + strings.Repeat(".", 18)

	p := &Position{Size: 19, Rows: rows}
	b, err := p.Board()

	if err != nil {
		t.Fatalf("Board: %v", err)
	}

	stones := b.Stones(board.Black)

	if len(stones) != 1 {
		t.Fatalf("%d schwarze Steine, erwartet 1", len(stones))
	}

	if got := board.ToGTP(stones[0], 19); got != "A19" {
		t.Fatalf("Stein auf %s, erwartet A19", got)
	}
}

func TestGameLiefertSetupOhneZuege(t *testing.T) {
	p := &Position{Size: 3, Rows: []string{"X..", ".O.", "..."}, Komi: 6.5}
	g, err := p.Game()

	if err != nil {
		t.Fatalf("Game: %v", err)
	}

	if g.Size != 3 || g.Komi != 6.5 {
		t.Fatalf("Size %d Komi %v", g.Size, g.Komi)
	}

	if len(g.Moves) != 0 {
		t.Fatalf("%d Züge, erwartet 0", len(g.Moves))
	}

	if len(g.Setup) != 2 {
		t.Fatalf("%d Setup-Steine, erwartet 2", len(g.Setup))
	}

	// Positions muss die Stellung materialisieren können.
	positions, err := g.Positions()

	if err != nil {
		t.Fatalf("Positions: %v", err)
	}

	if len(positions) != 1 {
		t.Fatalf("%d Stellungen, erwartet 1", len(positions))
	}

	if got := positions[0].Get(board.Point{X: 0, Y: 0}); got != board.Black {
		t.Fatalf("(0,0) = %v, erwartet Schwarz", got)
	}
}
