package shapes

import (
	"strings"
	"testing"

	"github.com/vmanke/goteach-prod/board"
)

// build baut ein Brett aus einer Textdarstellung: '.' leer, 'X' schwarz,
// 'O' weiß. Zeile 0 ist oben, wie überall im Projekt.
func build(t *testing.T, rows ...string) *board.Board {
	t.Helper()

	b := board.New(len(rows))

	for y, row := range rows {
		if len(row) != len(rows) {
			t.Fatalf("Zeile %d hat Länge %d, erwartet %d", y, len(row), len(rows))
		}

		for x := 0; x < len(row); x++ {
			var c board.Color

			switch row[x] {
			case '.':
				continue
			case 'X':
				c = board.Black
			case 'O':
				c = board.White
			default:
				t.Fatalf("unbekanntes Zeichen %q", row[x])
			}

			if err := b.SetStone(board.Point{X: x, Y: y}, c); err != nil {
				t.Fatalf("SetStone: %v", err)
			}
		}
	}

	return b
}

func names(instances []Instance) []string {
	out := make([]string, len(instances))

	for i, inst := range instances {
		out[i] = inst.Name + "(" + inst.Color + ")@" + strings.Join(inst.Points, ",")
	}

	return out
}

func has(instances []Instance, name string) bool {
	for _, inst := range instances {
		if inst.Name == name {
			return true
		}
	}

	return false
}

func count(instances []Instance, name string) int {
	n := 0

	for _, inst := range instances {
		if inst.Name == name {
			n++
		}
	}

	return n
}

func TestLeeresDreieckWirdErkannt(t *testing.T) {
	b := build(t,
		".....",
		".XX..",
		".X...",
		".....",
		".....",
	)

	found := Find(b)

	if !has(found, "leeres Dreieck") {
		t.Fatalf("leeres Dreieck nicht gefunden: %v", names(found))
	}
}

func TestVollesQuadratIstKeinLeeresDreieck(t *testing.T) {
	// Der vierte Punkt ist besetzt — dann ist es kein leeres Dreieck mehr.
	b := build(t,
		".....",
		".XX..",
		".XX..",
		".....",
		".....",
	)

	if has(Find(b), "leeres Dreieck") {
		t.Fatal("volles Quadrat darf kein leeres Dreieck sein")
	}
}

func TestFormenWerdenInAllenSymmetrienErkannt(t *testing.T) {
	// Der Springerzug ist die unsymmetrischste Form im Katalog: Er muss in
	// allen acht Lagen und in beiden Farben gleichermaßen auffallen.
	layouts := [][]string{
		{".....", "..X..", ".....", ".X...", "....."},
		{".....", ".X...", ".....", "..X..", "....."},
		{".....", "...X.", ".X...", ".....", "....."},
		{".....", ".X...", "...X.", ".....", "....."},
	}

	for i, rows := range layouts {
		for _, colour := range []string{"X", "O"} {
			swapped := make([]string, len(rows))

			for j, row := range rows {
				swapped[j] = strings.ReplaceAll(row, "X", colour)
			}

			b := build(t, swapped...)

			if !has(Find(b), "Kleiner Springerzug") {
				t.Errorf("Layout %d Farbe %s: Springerzug nicht erkannt", i, colour)
			}
		}
	}
}

func TestBambusverbindungUndTigermaul(t *testing.T) {
	bamboo := build(t,
		".....",
		".XX..",
		".....",
		".XX..",
		".....",
	)

	if !has(Find(bamboo), "Bambusverbindung") {
		t.Errorf("Bambusverbindung nicht erkannt: %v", names(Find(bamboo)))
	}

	tiger := build(t,
		".....",
		".X.X.",
		"..X..",
		".....",
		".....",
	)

	if !has(Find(tiger), "Tigermaul") {
		t.Errorf("Tigermaul nicht erkannt: %v", names(Find(tiger)))
	}
}

func TestKreuzschnittGehoertKeinerFarbe(t *testing.T) {
	b := build(t,
		".....",
		".XO..",
		".OX..",
		".....",
		".....",
	)

	found := Find(b)

	if !has(found, "Kreuzschnitt") {
		t.Fatalf("Kreuzschnitt nicht erkannt: %v", names(found))
	}

	// Genau einmal, nicht zweimal mit vertauschten Rollen.
	if got := count(found, "Kreuzschnitt"); got != 1 {
		t.Fatalf("%d Kreuzschnitte, erwartet 1: %v", got, names(found))
	}

	for _, inst := range found {
		if inst.Name == "Kreuzschnitt" && inst.Color != bothColours {
			t.Fatalf("Kreuzschnitt trägt Farbe %q, erwartet %q",
				inst.Color, bothColours)
		}
	}
}

func TestLeeresBrettHatKeineFormen(t *testing.T) {
	b := build(t, ".....", ".....", ".....", ".....", ".....")

	if found := Find(b); len(found) != 0 {
		t.Fatalf("%d Formen auf leerem Brett: %v", len(found), names(found))
	}
}

func TestLeiterFaengt(t *testing.T) {
	// Schwarzer Stein am linken Rand, von Weiß auf zwei Freiheiten gesetzt.
	// Er kann nur die Randlinie entlang laufen und geht in der Ecke unter —
	// die Leiter in ihrer knappsten Form.
	b := build(t,
		".......",
		".......",
		"XO.....",
		".......",
		".......",
		".......",
		".......",
	)

	if !Ladder(b, board.Point{X: 0, Y: 2}, DefaultDepth) {
		t.Fatal("Leiter sollte fangen")
	}
}

func TestLeiterScheitertAmAusbruchstein(t *testing.T) {
	// Dieselbe Ausgangslage, aber eigene Steine stehen im Verlauf: Die
	// laufende Kette gewinnt Freiheiten und die Leiter läuft tot. Ohne
	// diesen Gegentest könnte die Routine schlicht immer "gefangen" sagen.
	b := build(t,
		".......",
		".......",
		"XO.....",
		"XX.....",
		"XX.....",
		"XX.....",
		".......",
	)

	if Ladder(b, board.Point{X: 0, Y: 2}, DefaultDepth) {
		t.Fatal("Leiter darf mit eigenen Steinen im Verlauf nicht fangen")
	}
}

func TestKetteMitVielenFreiheitenIstKeineLeiter(t *testing.T) {
	b := build(t,
		".......",
		".......",
		"...X...",
		".......",
		".......",
		".......",
		".......",
	)

	if Ladder(b, board.Point{X: 3, Y: 2}, DefaultDepth) {
		t.Fatal("freistehender Stein ist keine Leiter")
	}
}

func TestSchnappWirdErkannt(t *testing.T) {
	// Weißer Köderstein auf B4 mit genau einer Freiheit (C4). Schlägt
	// Schwarz ihn dort, ist der schlagende Stein von Weiß umstellt und steht
	// selbst im Atari — Weiß nimmt ihn zurück.
	b := build(t,
		".XO..",
		"XO.O.",
		".XO..",
		".....",
		".....",
	)

	if !Snapback(b, board.Point{X: 1, Y: 1}) {
		t.Fatal("Schnapp nicht erkannt")
	}

	// Gegenprobe: Ohne die weiße Umstellung ist es ein gewöhnliches Atari.
	plain := build(t,
		".X...",
		"XO...",
		".X...",
		".....",
		".....",
	)

	if Snapback(plain, board.Point{X: 1, Y: 1}) {
		t.Fatal("gewöhnliches Atari ist kein Schnapp")
	}
}

func TestFindTacticsIstDeterministisch(t *testing.T) {
	b := build(t,
		".......",
		".......",
		"XO.....",
		".......",
		".......",
		".......",
		".......",
	)

	first := names(FindTactics(b))
	second := names(FindTactics(b))

	if strings.Join(first, "|") != strings.Join(second, "|") {
		t.Fatalf("nicht deterministisch:\n%v\n%v", first, second)
	}
}
