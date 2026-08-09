package teaching

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vmanke/goteach-prod/board"
)

// fakeSalience legt ein ausführbares Skript ab, das sich wie das
// Python-Modul verhält: Vertrag von stdin, Fenster nach stdout. Damit prüfen
// die Tests den echten Subprozesspfad, ohne einen Python-Stack vorauszusetzen.
func fakeSalience(t *testing.T, script string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "salienz")
	body := "#!/bin/sh\ncat >/dev/null\n" + script + "\n"

	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("Attrappe schreiben: %v", err)
	}

	return path
}

func smallGame(t *testing.T) (*board.Game, []*board.Board, [][]float64) {
	t.Helper()

	g := syntheticGame(t, 9, 20)
	positions, err := g.Positions()

	if err != nil {
		t.Fatalf("Positions: %v", err)
	}

	ownership := make([][]float64, len(positions))

	for i := range ownership {
		ownership[i] = make([]float64, 81)

		for j := range ownership[i] {
			ownership[i][j] = float64((i+j)%7) / 7.0
		}
	}

	return g, positions, ownership
}

func TestSalienzmodulLiefertFenster(t *testing.T) {
	command := fakeSalience(t, `echo '{"windows":[{"fromTurn":2,"toTurn":9,`+
		`"points":["D4","D5","E4"],"score":1.0}]}'`)

	g, positions, ownership := smallGame(t)

	windows, err := requestSalience(context.Background(), g.Size, positions,
		ownership, 0, command)

	if err != nil {
		t.Fatalf("requestSalience: %v", err)
	}

	if len(windows) != 1 {
		t.Fatalf("%d Fenster, erwartet 1", len(windows))
	}

	if windows[0].FromTurn != 2 || windows[0].ToTurn != 9 {
		t.Fatalf("Bereich %d..%d", windows[0].FromTurn, windows[0].ToTurn)
	}

	regions := salienceRegions(windows, g.Size)

	if len(regions) != 1 || len(regions[0]) != 3 {
		t.Fatalf("Regionen %v", regions)
	}
}

func TestSalienzmodulReichtFehlerWeiter(t *testing.T) {
	command := fakeSalience(t, `echo "goteach-salience: Vertrag unlesbar" >&2; exit 1`)

	g, positions, ownership := smallGame(t)

	_, err := requestSalience(context.Background(), g.Size, positions,
		ownership, 0, command)

	if err == nil {
		t.Fatal("Fehler erwartet")
	}

	// Ohne die stderr-Zeile stünde hier nur "exit status 1".
	if !strings.Contains(err.Error(), "Vertrag unlesbar") {
		t.Fatalf("stderr fehlt in der Meldung: %v", err)
	}
}

func TestSalienzmodulOhneKonfigurationMeldetKlar(t *testing.T) {
	t.Setenv(EnvSalienceCommand, "")

	g, positions, ownership := smallGame(t)

	_, err := requestSalience(context.Background(), g.Size, positions,
		ownership, 0, "")

	if !errors.Is(err, ErrSalienceNotConfigured) {
		t.Fatalf("ErrSalienceNotConfigured erwartet, erhalten: %v", err)
	}
}

func TestSalienzmodulUnbrauchbareAusgabe(t *testing.T) {
	command := fakeSalience(t, `echo "kein JSON"`)

	g, positions, ownership := smallGame(t)

	if _, err := requestSalience(context.Background(), g.Size, positions,
		ownership, 0, command); err == nil {
		t.Fatal("Fehler bei unlesbarer Ausgabe erwartet")
	}
}

func TestBrettzeilenFolgenDemVisionVertrag(t *testing.T) {
	b := board.New(3)

	if err := b.SetStone(board.Point{X: 0, Y: 0}, board.Black); err != nil {
		t.Fatalf("SetStone: %v", err)
	}

	if err := b.SetStone(board.Point{X: 2, Y: 2}, board.White); err != nil {
		t.Fatalf("SetStone: %v", err)
	}

	rows := boardRows(b)

	// Zeile 0 ist oben, '.' leer, 'X' schwarz, 'O' weiß — identisch zum
	// Austauschformat der Bilderkennung.
	if rows[0] != "X.." || rows[2] != "..O" {
		t.Fatalf("Zeilen %v", rows)
	}
}

func TestAnalyseLaeuftWeiterWennDasModulScheitert(t *testing.T) {
	// Das gelernte Modul ist eine Verbesserung, keine Voraussetzung: Fällt es
	// aus, muss die deterministische Fensterung übernehmen.
	t.Setenv(EnvSalienceCommand, fakeSalience(t, `exit 1`))

	g := syntheticGame(t, 19, 140)
	report, err := AnalyzeGame(g, denseAnalyzer{}, Options{Visits: 10, Tau: 3.0})

	if err != nil {
		t.Fatalf("AnalyzeGame: %v", err)
	}

	if len(report.Strands) == 0 {
		t.Fatal("ohne Salienzmodul müssen die Stränge trotzdem entstehen")
	}
}

func TestFensterDesModulsBestimmenDieGegenden(t *testing.T) {
	// Ein Fenster in der oberen linken Ecke: Der daraus gebaute Strang muss
	// dort liegen, nicht irgendwo.
	command := fakeSalience(t, `echo '{"windows":[{"fromTurn":0,"toTurn":60,`+
		`"points":["A19","B19","C19","A18","B18","C18","A17","B17","C17"],`+
		`"score":1.0}]}'`)

	t.Setenv(EnvSalienceCommand, command)

	g := syntheticGame(t, 19, 140)
	report, err := AnalyzeGame(g, denseAnalyzer{}, Options{Visits: 10, Tau: 3.0})

	if err != nil {
		t.Fatalf("AnalyzeGame: %v", err)
	}

	if len(report.Strands) == 0 {
		t.Fatal("keine Stränge aus dem Fenster des Moduls")
	}

	for _, s := range report.Strands {
		if s.Area != "oben links" {
			t.Errorf("Strang %d liegt %q, erwartet oben links", s.ID, s.Area)
		}
	}
}
