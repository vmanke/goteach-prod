package teaching

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vmanke/goteach-prod/board"
)

// berichtDiagramDir ist das Unterverzeichnis der Diagramme im Bericht.
const berichtDiagramDir = "diagramme"

// berichtTopMoves ist die Zahl der Züge in der Tabelle der teuersten Züge.
const berichtTopMoves = 12

// Bericht ist der lesbare Bericht zu einer Partie: Markdown mit
// SVG-Diagrammen, Dateinamen relativ zum Berichtsverzeichnis.
type Bericht struct {
	Markdown string
	Files    map[string]string
}

// BuildBericht setzt den Bericht aus dem GameReport zusammen. Die
// Diagramme brauchen die Brettstellungen, deshalb wird die Partie hier
// noch einmal nachgespielt.
func BuildBericht(name string, g *board.Game, report *GameReport) (*Bericht, error) {
	positions, err := g.Positions()

	if err != nil {
		return nil, err
	}

	b := &Bericht{Files: map[string]string{}}
	var md strings.Builder

	fmt.Fprintf(&md, "# Partieanalyse %s\n\n", name)
	fmt.Fprintf(&md,
		"Brett %d×%d, Komi %.1f, Regeln %s. Analysiert wurden %d Züge. "+
			"Punktverluste gesamt laut Engine: Schwarz %.1f, Weiß %.1f.\n\n",
		report.Size, report.Size, report.Komi, report.Rules, len(report.Moves),
		report.TotalPointsLost("Schwarz"), report.TotalPointsLost("Weiß"))

	md.WriteString("## Bilanz\n\n")

	if report.Bilanz == nil {
		md.WriteString("Keine Bilanz: Die Endstellung wurde nicht analysiert.\n\n")
	} else {
		bl := report.Bilanz
		md.WriteString(bl.Text + "\n\n")
		fmt.Fprintf(&md, "Tote schwarze Steine: %s.\n\n", joinOrNone(bl.DeadBlack))
		fmt.Fprintf(&md, "Tote weiße Steine: %s.\n\n", joinOrNone(bl.DeadWhite))

		file := "endstellung.svg"
		b.Files[filepath.Join(berichtDiagramDir, file)] = endDiagram(g, positions, bl)
		md.WriteString(imageLine(file))
	}

	md.WriteString("## Baustellen\n\n")
	md.WriteString("Eine Baustelle ist ein Punkt, den die Engine über viele eigene " +
		"Züge als Erstwahl nannte, ohne dass dort gespielt wurde.\n\n")

	if len(report.Baustellen) == 0 {
		md.WriteString("Keine Baustellen gefunden.\n\n")
	}

	for i := range report.Baustellen {
		bs := &report.Baustellen[i]
		fmt.Fprintf(&md, "### %s für %s, Züge %d bis %d\n\n%s\n\n",
			bs.Point, bs.Player, bs.FromMove, bs.ToMove, bs.Text)

		file := fmt.Sprintf("baustelle-%02d.svg", bs.ID)
		b.Files[filepath.Join(berichtDiagramDir, file)] = baustelleDiagram(g, positions, bs)
		md.WriteString(imageLine(file))
	}

	md.WriteString("## Erzählstränge\n\n")

	if len(report.Strands) == 0 {
		md.WriteString("Keine Erzählstränge gefunden — die Partie verlief ohne " +
			"erkennbar zusammenhängende Kämpfe.\n\n")
	}

	for i := range report.Strands {
		s := &report.Strands[i]
		fmt.Fprintf(&md, "### %s, Züge %d bis %d\n\n%s\n\n",
			capitalize(s.Area), s.FromMove, s.ToMove, s.Text)

		if s.TextLLM != "" {
			fmt.Fprintf(&md, "Lehrtext (LLM): %s\n\n", s.TextLLM)
		}

		fmt.Fprintf(&md, "Punktverluste im Strang laut Engine: Schwarz %.1f, Weiß %.1f.\n\n",
			s.PointsLost["Schwarz"], s.PointsLost["Weiß"])

		file := fmt.Sprintf("strang-%02d.svg", s.ID)
		b.Files[filepath.Join(berichtDiagramDir, file)] = strandDiagram(g, positions, s)
		md.WriteString(imageLine(file))
	}

	md.WriteString("## Die teuersten Züge\n\n")
	md.WriteString("Verlust je Zug laut Engine (Stellung vor und nach dem Zug); " +
		"Erstwahl ist der Zug, den die Engine stattdessen erwartet hätte.\n\n")
	md.WriteString("| Zug | Spieler | Gespielt | Verlust | Erstwahl | Einstufung |\n")
	md.WriteString("|---|---|---|---|---|---|\n")

	for _, m := range costliestMoves(report.Moves, berichtTopMoves) {
		fmt.Fprintf(&md, "| %d | %s | %s | %.1f | %s | %s |\n",
			m.Number, m.Player, m.Coord, m.PointsLost, m.BestMove, m.Category)
	}

	b.Markdown = md.String()

	return b, nil
}

// Write legt bericht.md und die Diagramme unter dir ab.
func (b *Bericht) Write(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, berichtDiagramDir), 0o755); err != nil {
		return err
	}

	for name, content := range b.Files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return err
		}
	}

	return os.WriteFile(filepath.Join(dir, "bericht.md"), []byte(b.Markdown), 0o644)
}

func imageLine(file string) string {
	return fmt.Sprintf("![%s](%s/%s)\n\n", file, berichtDiagramDir, file)
}

func joinOrNone(list []string) string {
	if len(list) == 0 {
		return "keine"
	}

	return strings.Join(list, ", ")
}

// costliestMoves liefert die n teuersten Züge, größter Verlust zuerst.
func costliestMoves(moves []MoveReport, n int) []MoveReport {
	out := make([]MoveReport, len(moves))
	copy(out, moves)

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].PointsLost != out[j].PointsLost {
			return out[i].PointsLost > out[j].PointsLost
		}

		return out[i].Number < out[j].Number
	})

	if len(out) > n {
		out = out[:n]
	}

	return out
}
