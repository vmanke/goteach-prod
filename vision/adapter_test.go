package vision

import (
	"strings"
	"testing"

	"github.com/vmanke/goteach-prod/board"
)

// Ungültige Brettgrößen aus fremdem JSON müssen als Fehler zurückkommen und
// dürfen niemals in board.New paniken (das paniert außerhalb 2..25).
func TestFromJSONRejectsInvalidSize(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"leere Zeilen ohne size", `{"rows":[]}`},
		{"size 0 explizit", `{"size":0,"rows":[]}`},
		{"size 1", `{"size":1,"rows":["X"]}`},
		{"size 26", `{"size":26,"rows":[]}`},
		{"negative size", `{"size":-5,"rows":[]}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Panic statt Fehler: %v", r)
				}
			}()

			p, err := FromJSON([]byte(tc.json))

			if err == nil {
				t.Fatalf("kein Fehler für %s, Position %+v", tc.json, p)
			}

			if !strings.Contains(err.Error(), "Brettgröße") {
				t.Fatalf("unerwartete Fehlermeldung: %v", err)
			}
		})
	}
}

// Board() wird auch auf direkt konstruierten Positionen aufgerufen; auch dort
// darf eine ungültige Größe keinen Panic auslösen.
func TestBoardRejectsInvalidSizeWithoutFromJSON(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Panic statt Fehler: %v", r)
		}
	}()

	if _, err := (&Position{Size: 0}).Board(); err == nil {
		t.Fatal("Board() akzeptierte Size 0")
	}

	if _, err := (&Position{Size: 99}).Board(); err == nil {
		t.Fatal("Board() akzeptierte Size 99")
	}
}

// Der gültige Pfad bleibt unberührt.
func TestFromJSONValidPosition(t *testing.T) {
	rows := make([]string, 19)

	for i := range rows {
		rows[i] = strings.Repeat(".", 19)
	}

	// Ein schwarzer und ein weißer Stein.
	rows[3] = "..." + "X" + strings.Repeat(".", 15)
	rows[15] = strings.Repeat(".", 15) + "O" + "..."

	p := &Position{Size: 19, Rows: rows, Komi: 7.5}
	b, err := p.Board()

	if err != nil {
		t.Fatal(err)
	}

	if got := b.Get(board.Point{X: 3, Y: 3}); got != board.Black {
		t.Fatalf("(3,3) = %v, erwartet Schwarz", got)
	}

	if got := b.Get(board.Point{X: 15, Y: 15}); got != board.White {
		t.Fatalf("(15,15) = %v, erwartet Weiß", got)
	}

	g, err := p.Game()

	if err != nil {
		t.Fatal(err)
	}

	if len(g.Setup) != 2 || g.Komi != 7.5 {
		t.Fatalf("Game falsch: %d Setup-Steine, Komi %.1f", len(g.Setup), g.Komi)
	}
}
