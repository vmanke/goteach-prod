package teaching

import (
	"strings"
	"testing"

	"github.com/vmanke/goteach-prod/board"
	"github.com/vmanke/goteach-prod/katago"
)

// setupGame baut eine Stellung ohne Zughistorie — genau die Form, die
// vision.Position.Game() liefert.
func setupGame(size int, komi float64, stones map[string]board.Color) *board.Game {
	g := &board.Game{Size: size, Komi: komi}

	for coord, colour := range stones {
		pt, _, err := board.FromGTP(coord, size)

		if err != nil {
			panic(err)
		}

		g.Setup = append(g.Setup, board.Move{Color: colour, Point: pt})
	}

	return g
}

func TestAnalyzePositionOhneZuege(t *testing.T) {
	g := setupGame(9, 6.5, map[string]board.Color{
		"D4": board.Black,
		"F6": board.White,
		"D5": board.Black,
	})

	rep, err := AnalyzePosition(g, katago.Mock{}, Options{Visits: 10})

	if err != nil {
		t.Fatalf("AnalyzePosition: %v", err)
	}

	if rep.Size != 9 || rep.Komi != 6.5 {
		t.Fatalf("Size %d Komi %v", rep.Size, rep.Komi)
	}

	if rep.Stones != 3 {
		t.Fatalf("Stones = %d, erwartet 3", rep.Stones)
	}

	// D4+D5 bilden eine Kette, F6 eine zweite.
	if len(rep.Groups) != 2 {
		t.Fatalf("%d Ketten, erwartet 2", len(rep.Groups))
	}

	if rep.Text == "" {
		t.Fatal("Lehrtext fehlt")
	}

	if !strings.Contains(rep.Text, "9×9") {
		t.Fatalf("Brettgröße fehlt im Text: %q", rep.Text)
	}
}

func TestAnalyzePositionLehntPartienMitZuegenAb(t *testing.T) {
	// Analyze ist für Partien zuständig, AnalyzePosition für Stellungen;
	// die Verwechslung soll auffallen statt still das Falsche zu tun.
	g := setupGame(9, 6.5, map[string]board.Color{"D4": board.Black})
	pt, _, _ := board.FromGTP("F6", 9)
	g.Moves = append(g.Moves, board.Move{Color: board.White, Point: pt})

	_, err := AnalyzePosition(g, katago.Mock{}, Options{Visits: 10})

	if err == nil {
		t.Fatal("Fehler bei vorhandenen Zügen erwartet")
	}

	if !strings.Contains(err.Error(), "ohne Züge") {
		t.Fatalf("unerwartete Meldung: %v", err)
	}
}

func TestAnalyzeLehntStellungOhneZuegeAb(t *testing.T) {
	// Die Gegenrichtung: Ohne diese Abgrenzung liefe eine erkannte Stellung
	// in Analyze und käme mit leerem Ergebnis zurück.
	g := setupGame(9, 6.5, map[string]board.Color{"D4": board.Black})

	if _, err := Analyze(g, katago.Mock{}, Options{Visits: 10}); err == nil {
		t.Fatal("Analyze muss eine Partie ohne Züge ablehnen")
	}
}

func TestAnalyzePositionErkenntAtariUndBenson(t *testing.T) {
	// Weiß auf B8, drei der vier Nachbarn schwarz: nur noch B7 als Freiheit.
	// Bewusst nicht am Rand — ein Eckpunkt hat von vornherein nur zwei
	// Nachbarn und wäre mit zwei schwarzen Steinen bereits geschlagen.
	g := setupGame(9, 7.5, map[string]board.Color{
		"B8": board.White,
		"A8": board.Black,
		"C8": board.Black,
		"B9": board.Black,
	})

	rep, err := AnalyzePosition(g, katago.Mock{}, Options{Visits: 10})

	if err != nil {
		t.Fatalf("AnalyzePosition: %v", err)
	}

	var white *GroupState

	for i := range rep.Groups {
		if rep.Groups[i].Color == "Weiß" {
			white = &rep.Groups[i]
		}
	}

	if white == nil {
		t.Fatal("weiße Kette fehlt")
	}

	if !white.InAtari {
		t.Fatalf("weiße Kette hat %d Freiheiten, Atari erwartet", white.Liberties)
	}

	if !strings.Contains(rep.Text, "Atari") {
		t.Fatalf("Atari fehlt im Lehrtext: %q", rep.Text)
	}
}

func TestAnalyzePositionStaerkeLiegtImWertebereich(t *testing.T) {
	g := setupGame(19, 7.5, map[string]board.Color{
		"D4": board.Black, "Q16": board.White, "D16": board.Black,
	})

	rep, err := AnalyzePosition(g, katago.Mock{}, Options{Visits: 10})

	if err != nil {
		t.Fatalf("AnalyzePosition: %v", err)
	}

	for _, grp := range rep.Groups {
		if grp.Strength < -1.0 || grp.Strength > 1.0 {
			t.Errorf("Stärke %v von %s außerhalb [-1, 1]", grp.Strength, grp.Rep)
		}
	}
}
