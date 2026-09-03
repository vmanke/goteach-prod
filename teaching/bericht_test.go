package teaching

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vmanke/goteach-prod/board"
)

func TestBerichtEnthaeltAlleAbschnitte(t *testing.T) {
	g := syntheticGame(t, 19, 140)
	g.Result = "B+7.5"
	report, err := AnalyzeGame(g, denseAnalyzer{}, Options{Visits: 10, Tau: 3.0})

	if err != nil {
		t.Fatalf("AnalyzeGame: %v", err)
	}

	if report.Bilanz == nil {
		t.Fatal("keine Bilanz")
	}

	b, err := BuildBericht("synthetisch.sgf", g, report)

	if err != nil {
		t.Fatalf("BuildBericht: %v", err)
	}

	for _, want := range []string{
		"# Partieanalyse synthetisch.sgf",
		"Brett 19×19, Komi 7.5, Regeln chinese. Analysiert wurden 140 Züge.",
		"## Bilanz",
		report.Bilanz.Text,
		"![endstellung.svg](diagramme/endstellung.svg)",
		"## Baustellen",
		"## Erzählstränge",
		"## Die teuersten Züge",
		"| Zug | Spieler | Gespielt | Verlust | Erstwahl | Einstufung |",
	} {
		if !strings.Contains(b.Markdown, want) {
			t.Errorf("Bericht ohne %q", want)
		}
	}

	if len(report.Strands) == 0 {
		t.Fatal("keine Stränge — der Test braucht mindestens einen")
	}

	for _, s := range report.Strands {
		heading := "### " + capitalize(s.Area)

		if !strings.Contains(b.Markdown, heading) {
			t.Errorf("Bericht ohne Strang-Überschrift %q", heading)
		}
	}

	// Ein Diagramm je Strang plus die Endstellung, alle wohlgeformt.
	if len(b.Files) != len(report.Strands)+len(report.Baustellen)+1 {
		t.Fatalf("%d Dateien bei %d Strängen und %d Baustellen",
			len(b.Files), len(report.Strands), len(report.Baustellen))
	}

	for name, content := range b.Files {
		if err := xml.Unmarshal([]byte(content), new(struct{})); err != nil {
			t.Errorf("%s nicht wohlgeformt: %v", name, err)
		}

		if !strings.Contains(b.Markdown, "("+filepath.ToSlash(name)+")") {
			t.Errorf("%s wird im Bericht nicht eingebunden", name)
		}
	}

	dir := t.TempDir()

	if err := b.Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}

	written, err := os.ReadFile(filepath.Join(dir, "bericht.md"))

	if err != nil || string(written) != b.Markdown {
		t.Fatalf("bericht.md fehlt oder weicht ab: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "diagramme", "endstellung.svg")); err != nil {
		t.Fatalf("endstellung.svg fehlt: %v", err)
	}
}

func TestBerichtMitBaustellen(t *testing.T) {
	// Eine kurze Partie, deren Zug-Reports von Hand gesetzt sind: Schwarz
	// ignoriert K10 sechsmal, dann spielt Weiß es.
	g := &board.Game{Size: 19, Komi: 6.5, Rules: "japanese"}
	var reports []MoveReport
	coords := []string{"D4", "Q16", "D16", "Q4", "C10", "R10", "K4", "K16", "J3", "L17", "H3", "M17", "G3", "K10"}

	for i, coord := range coords {
		colour := board.Black
		player := "Schwarz"
		best := "K10"

		if i%2 == 1 {
			colour = board.White
			player = "Weiß"
			best = coord
		}

		p, _, err := board.FromGTP(coord, 19)

		if err != nil {
			t.Fatal(err)
		}

		g.Moves = append(g.Moves, board.Move{Color: colour, Point: p})
		reports = append(reports, MoveReport{
			Number: i + 1, Player: player, Coord: coord, BestMove: best,
			PointsLost: 2, Category: "Ungenauigkeit",
		})
	}

	report := &GameReport{Size: 19, Komi: 6.5, Rules: "japanese", Moves: reports}
	report.Baustellen = findBaustellen(19, reports)

	if len(report.Baustellen) != 1 || report.Baustellen[0].ResolvedBy != "Gegner" {
		t.Fatalf("Baustellen: %+v", report.Baustellen)
	}

	b, err := BuildBericht("kurz.sgf", g, report)

	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(b.Markdown, "### K10 für Schwarz, Züge 1 bis 13") {
		t.Fatalf("Überschrift fehlt:\n%s", b.Markdown)
	}

	svg, ok := b.Files[filepath.Join("diagramme", "baustelle-01.svg")]

	if !ok {
		t.Fatal("baustelle-01.svg fehlt")
	}

	// Der Punkt ist nach Zug 14 besetzt: roter Ring ohne Koordinatentext,
	// und der Stein trägt die Nummer 14.
	if !strings.Contains(svg, `fill="#111">14<`) || strings.Contains(svg, `fill="#d00">K10<`) {
		t.Fatalf("Diagramm markiert die Auflösung nicht: %s", svg)
	}

	if !strings.Contains(b.Markdown, "Keine Bilanz") {
		t.Fatal("Bericht ohne Hinweis auf die fehlende Bilanz")
	}
}
