// Lehreinheiten ausliefern, sobald sie fertig sind.
//
// Der Sammelweg (analyzeCore) wartet die ganze Partie ab, bevor er
// irgendetwas herausgibt. Für eine Datei ist das richtig; für eine Seite,
// die jemand ansieht, ist es die falsche Reihenfolge: Der erste Zug ist
// nach Sekunden fertig, der letzte nach Minuten, und dazwischen steht der
// Leser vor einer leeren Seite.
//
// Dass das überhaupt geht, liegt an der Bauart der Texte: Die
// Wiederholungs-Unterdrückung (compose.go) schaut ausschließlich zurück —
// ein Befund schweigt, WEIL er kürzlich schon dastand. Der Text zu Zug i
// hängt also nur an den Zügen davor, nie an denen danach. Genau deshalb
// darf er hinaus, sobald Zug i gerechnet ist, und liest sich am Ende
// Wort für Wort wie der Sammelweg.
package teaching

import (
	"context"
	"fmt"
	"os"

	"github.com/vmanke/goteach-prod/board"
	"github.com/vmanke/goteach-prod/katago"
)

// StreamHandler nimmt entgegen, was ein laufender Durchgang fertigstellt.
// Beide Felder dürfen nil sein.
type StreamHandler struct {
	// Move wird je Zug genau einmal aufgerufen, in aufsteigender
	// Zugreihenfolge — auch wenn die Engine die Stellungen in anderer
	// Reihenfolge fertig rechnet. Ein Fehler bricht die Analyse ab.
	Move func(*MoveReport) error

	// Strands wird einmal am Ende aufgerufen, sofern Stränge gefunden
	// wurden; sie brauchen die ganze Partie und können nicht früher
	// entstehen.
	Strands func([]Strand) error
}

// AnalyzeStream rechnet die Partie und liefert jede Lehreinheit an h.Move,
// sobald sie vorliegt. Zurück kommt derselbe GameReport wie beim
// Sammelweg, damit der Aufrufer am Ende das vollständige Ergebnis in der
// Hand hält.
//
// Nicht unterstützt und bewusst ignoriert: die Rückkopplung RefineVisits
// (sie würde bereits ausgelieferte Züge nachträglich ändern) und der
// LLM-Feinschliff (er läuft je Zug und gehört nicht in den heißen Pfad).
func AnalyzeStream(g *board.Game, an katago.Analyzer, opt Options,
	h StreamHandler) (*GameReport, error) {

	if opt.Tau <= 0 {
		opt.Tau = 3.0
	}

	positions, err := g.Positions()

	if err != nil {
		return nil, err
	}

	n := len(g.Moves)

	if n == 0 {
		return nil, fmt.Errorf("teaching: Partie enthält keine Züge")
	}

	from, to := moveRange(opt, n)
	req := analysisRequest(g, opt)
	turns := analysisTurns(from, to)
	nsq := g.Size * g.Size

	if opt.Progress {
		fmt.Fprintf(os.Stderr,
			"teaching: analysiere %d Stellungen fortlaufend (Visits=%d) ...\n",
			len(turns), opt.Visits)
	}

	byTurn := map[int]*katago.Result{}
	reports := make([]MoveReport, 0, to-from+1)
	state := newDedupState()

	// next ist der Index des nächsten Zuges, der ausgeliefert werden kann.
	// Ein Zug braucht die Stellung davor UND danach; sobald beide da sind,
	// rückt der Zeiger so weit vor, wie die Lücken es zulassen.
	next := from - 1

	err = katago.Stream(an, req, turns, func(res *katago.Result) error {
		if len(res.Ownership) != nsq {
			return fmt.Errorf(
				"teaching: Ownership-Länge %d ≠ %d (Turn %d) — Config prüfen",
				len(res.Ownership), nsq, res.TurnNumber)
		}

		byTurn[res.TurnNumber] = res

		for next < to {
			before, okBefore := byTurn[next]
			after, okAfter := byTurn[next+1]

			if !okBefore || !okAfter {
				break
			}

			var prev *board.Move

			if next > 0 {
				prev = &g.Moves[next-1]
			}

			rep := buildReport(next, g.Moves[next], prev, g.Size, n,
				positions[next], positions[next+1], before, after, opt.Tau)

			// Derselbe Zustand über die ganze Partie wie im Sammelweg —
			// nur eben Zug für Zug statt in einem Rutsch.
			if rep.rose != nil {
				rep.Text = renderRoseText(&rep, state)
			}

			reports = append(reports, rep)
			next++

			if opt.Progress && next%10 == 0 {
				fmt.Fprintf(os.Stderr, "teaching: Zug %d/%d fertig\n", next, to)
			}

			if h.Move != nil {
				if err := h.Move(&reports[len(reports)-1]); err != nil {
					return err
				}
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	if len(reports) != to-from+1 {
		return nil, fmt.Errorf("teaching: %d Zug-Reports erwartet, %d erhalten",
			to-from+1, len(reports))
	}

	report := &GameReport{
		Size:  g.Size,
		Komi:  req.Komi,
		Rules: req.Rules,
		Moves: reports,
	}

	report.Strands = streamStrands(g, opt, positions, byTurn, reports, from, to)

	if h.Strands != nil && len(report.Strands) > 0 {
		if err := h.Strands(report.Strands); err != nil {
			return nil, err
		}
	}

	return report, nil
}

// streamStrands sucht die Erzählstränge, nachdem alle Züge draußen sind.
// Sie brauchen den Blick über die ganze Partie; ohne Ownership zu jedem
// Turn gibt es keine Stränge, und das ist kein Fehler, sondern der
// Normalfall bei kurzen Partien.
func streamStrands(g *board.Game, opt Options, positions []*board.Board,
	byTurn map[int]*katago.Result, reports []MoveReport, from, to int) []Strand {

	lo := from - 1
	ownership := make([][]float64, 0, to-lo+1)

	for t := lo; t <= to; t++ {
		a := byTurn[t]

		if a == nil {
			break
		}

		ownership = append(ownership, a.Ownership)
	}

	var regions [][]board.Point

	if SalienceConfigured() {
		windows, serr := requestSalience(context.Background(), g.Size,
			positions, ownership, lo, opt.SalienceCommand)

		if serr != nil {
			fmt.Fprintf(os.Stderr,
				"teaching: Salienzmodul übersprungen: %v\n", serr)
		} else {
			regions = salienceRegions(windows, g.Size)
		}
	}

	return buildStrands(g, positions, ownership, lo, reports, opt.Tau, regions)
}
