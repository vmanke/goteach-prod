package server

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/vmanke/goteach-prod/board"
)

// Die Beispielpartie wirbt für einen Dienst, dessen Zusage lautet: keine
// Behauptung ohne Zahl. Also werden die Zahlen der Seite hier nicht
// geglaubt, sondern nachgerechnet — mit dem Brett dieses Repos, unabhängig
// von dem Javascript, welches die Seite selbst mitbringt.

// partieMove ist ein Eintrag der Zugliste aus assets/partie.html.
type partieMove struct {
	Number   int
	Black    bool
	Point    string  // "" bei Pass
	Category int     // 0 ausgezeichnet … 4 grober Fehler
	Delta    float64 // Punktverlust, negativ
	Winrate  float64 // Gewinnchance des Ziehenden nach dem Zug
}

// blackWinrate rechnet die Gewinnchance auf die Sicht von Schwarz um —
// so, wie die Seite sie aufträgt.
func (z partieMove) blackWinrate() float64 {
	if z.Black {
		return z.Winrate
	}

	return 100 - z.Winrate
}

// moveLine passt auf einen Eintrag von M in assets/partie.html.
var moveLine = regexp.MustCompile(
	`\[(\d+),"([BW])",(null|"[A-N]\d+"),(\d),(-?\d+\.\d+),(\d+\.\d+),`)

// loadPartie liest die Zugliste aus dem eingebetteten Asset.
func loadPartie(t *testing.T) (moves []partieMove, page string) {
	t.Helper()

	raw, err := assetsFS.ReadFile("assets/partie.html")

	if err != nil {
		t.Fatalf("assets/partie.html: %v", err)
	}

	page = string(raw)
	block := page

	if _, after, ok := strings.Cut(page, "const M = ["); ok {
		block, _, _ = strings.Cut(after, "];")
	} else {
		t.Fatal("Zugliste (const M) nicht gefunden")
	}

	for _, m := range moveLine.FindAllStringSubmatch(block, -1) {
		nr, _ := strconv.Atoi(m[1])
		kat, _ := strconv.Atoi(m[4])
		delta, _ := strconv.ParseFloat(m[5], 64)
		chance, _ := strconv.ParseFloat(m[6], 64)
		point := ""

		if m[3] != "null" {
			point = strings.Trim(m[3], `"`)
		}

		moves = append(moves, partieMove{
			Number:   nr,
			Black:    m[2] == "B",
			Point:    point,
			Category: kat,
			Delta:    delta,
			Winrate:  chance,
		})
	}

	if len(moves) == 0 {
		t.Fatal("keine Züge gelesen")
	}

	return moves, page
}

// TestPartieMovesAreLegal spielt die Partie auf dem Brett dieses Repos
// nach. Ein besetzter Punkt, ein Selbstmord oder ein Ko-Verstoß fiele hier
// auf — die Zugliste wäre dann keine Partie.
func TestPartieMovesAreLegal(t *testing.T) {
	moves, page := loadPartie(t)

	if len(moves) != 126 {
		t.Fatalf("%d Züge gelesen, die Seite nennt 126", len(moves))
	}

	if !strings.Contains(page, "13×13, 126 Züge") {
		t.Error("Überschrift nennt nicht mehr 126 Züge")
	}

	b := board.New(13)

	for i, z := range moves {
		colour := board.White

		if z.Black {
			colour = board.Black
		}

		if expected := i%2 == 0; z.Black != expected {
			t.Fatalf("Zug %d: Schwarz=%v, an der Reihe war das Gegenteil", z.Number, z.Black)
		}

		if z.Point == "" {
			if err := b.Play(board.Move{Color: colour, Pass: true}); err != nil {
				t.Fatalf("Zug %d (Pass): %v", z.Number, err)
			}

			continue
		}

		p, pass, err := board.FromGTP(z.Point, 13)

		if err != nil || pass {
			t.Fatalf("Zug %d: Koordinate %q: %v", z.Number, z.Point, err)
		}

		if err := b.Play(board.Move{Color: colour, Point: p}); err != nil {
			t.Fatalf("Zug %d (%s %s): %v", z.Number, colour, z.Point, err)
		}
	}
}

// TestPartieCapturesMatch prüft die Fußnote der Seite: neun
// Schlag-Ereignisse, 17 Steine.
func TestPartieCapturesMatch(t *testing.T) {
	moves, page := loadPartie(t)
	b := board.New(13)

	events := 0
	before := 0

	for _, z := range moves {
		colour := board.White

		if z.Black {
			colour = board.Black
		}

		move := board.Move{Color: colour, Pass: z.Point == ""}

		if !move.Pass {
			p, _, err := board.FromGTP(z.Point, 13)

			if err != nil {
				t.Fatalf("Zug %d: %v", z.Number, err)
			}

			move.Point = p
		}

		if err := b.Play(move); err != nil {
			t.Fatalf("Zug %d: %v", z.Number, err)
		}

		total := b.Captured[board.Black] + b.Captured[board.White]

		if total > before {
			events++
			before = total
		}
	}

	if events != 9 {
		t.Errorf("%d Schlag-Ereignisse, die Seite nennt neun", events)
	}

	if total := b.Captured[board.Black] + b.Captured[board.White]; total != 17 {
		t.Errorf("%d geschlagene Steine, die Seite nennt 17", total)
	}

	// Schwarz schlägt 15 weiße Steine, Weiß zwei schwarze.
	if b.Captured[board.White] != 15 || b.Captured[board.Black] != 2 {
		t.Errorf("geschlagen: Weiß %d, Schwarz %d — erwartet 15 und 2",
			b.Captured[board.White], b.Captured[board.Black])
	}

	if !strings.Contains(page, "17 geschlagene Steine") {
		t.Error("die Fußnote nennt nicht mehr 17 geschlagene Steine")
	}
}

// TestPartieCaptionFigures rechnet jede Zahl der Bildunterschrift aus
// der Zugliste nach. Ohne diesen Test wäre die Unterschrift eine
// Behauptung — und genau davon handelt der Dienst.
func TestPartieCaptionFigures(t *testing.T) {
	moves, page := loadPartie(t)

	_, rest, ok := strings.Cut(page, "function drawCaption()")

	if !ok {
		t.Fatal("Bildunterschrift nicht gefunden")
	}

	caption, _, _ := strings.Cut(rest, "}")

	// Tiefpunkt von Schwarz bis Zug 64.
	low := moves[0]

	for _, z := range moves {
		if z.Number <= 64 && z.blackWinrate() < low.blackWinrate() {
			low = z
		}
	}

	checkFigure(t, caption, "Tiefpunkt", fmt.Sprintf("%.1f", low.blackWinrate()), "75.0")

	if low.Number != 15 {
		t.Errorf("Tiefpunkt bei Zug %d, die Unterschrift nennt Zug 15", low.Number)
	}

	// Von Zug 19 bis 64 keine Delle unter 98 Prozent.
	lowest := 100.0

	for _, z := range moves {
		if z.Number >= 19 && z.Number <= 64 {
			lowest = math.Min(lowest, z.blackWinrate())
		}
	}

	if lowest < 98 {
		t.Errorf("zwischen Zug 19 und 64 fällt Schwarz auf %.1f Prozent — "+
			"die Unterschrift behauptet über 98", lowest)
	}

	// Erster Zug, nach welchem Schwarz hinten liegt, und die Zahl der
	// Führungswechsel danach.
	firstBehind, swings := 0, 0

	for i, z := range moves {
		if firstBehind == 0 && z.blackWinrate() < 50 {
			firstBehind = z.Number
		}

		if i > 0 && (moves[i-1].blackWinrate() >= 50) != (z.blackWinrate() >= 50) {
			swings++
		}
	}

	if firstBehind != 77 {
		t.Errorf("Schwarz fällt erstmals bei Zug %d zurück, die Unterschrift nennt 77",
			firstBehind)
	}

	checkFigure(t, caption, "Führungswechsel", strconv.Itoa(swings), "13")

	// Die beiden Züge, welche die Führung ein letztes Mal drehen.
	for _, nr := range []int{114, 115} {
		z := moves[nr-1]

		if fmt.Sprintf("%.1f", math.Abs(z.Delta)) != "1.3" {
			t.Errorf("Zug %d kostet %.1f Punkte, die Unterschrift nennt 1.3",
				nr, math.Abs(z.Delta))
		}

		if moves[nr-2].blackWinrate() >= 50 == (z.blackWinrate() >= 50) {
			t.Errorf("Zug %d dreht die Führung nicht", nr)
		}
	}

	if !strings.Contains(caption, "1.3 Punkte") {
		t.Error("die Unterschrift nennt die 1.3 Punkte nicht mehr")
	}
}

// checkFigure stellt sicher, dass der nachgerechnete Wert im Text steht und dem
// erwarteten entspricht — schlägt also an, wenn der Text geändert wird,
// ohne neu zu rechnen, und ebenso umgekehrt.
func checkFigure(t *testing.T, text, what, computed, expected string) {
	t.Helper()

	if computed != expected {
		t.Errorf("%s: nachgerechnet %s, im Test erwartet %s", what, computed, expected)
	}

	if !strings.Contains(text, computed) {
		t.Errorf("%s: %s steht nicht mehr in der Bildunterschrift", what, computed)
	}
}
