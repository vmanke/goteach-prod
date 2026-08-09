package teaching

import (
	"fmt"
	"os"
	"sort"

	"github.com/vmanke/goteach-prod/board"
	"github.com/vmanke/goteach-prod/katago"
)

// defaultRefineTop ist die Zahl der Stränge, die nachgerechnet werden, wenn
// RefineTop nicht gesetzt ist.
const defaultRefineTop = 3

// refine rechnet die stärksten Erzählstränge mit mehr Visits nach.
//
// Das ist die Rückkopplung: Erst sagt die Auswertung, wo die Partie
// entschieden wurde, dann geht die Rechenzeit genau dorthin. Eine
// gleichmäßige Verteilung der Visits über alle Stellungen gibt einer ruhigen
// Eröffnungsfolge dasselbe Gewicht wie dem Kampf, der die Partie kippt.
//
// Die Segmentierung selbst bleibt unangetastet — sie war die Grundlage der
// Auswahl, sie im Nachhinein mit den verfeinerten Zahlen zu verschieben
// hieße, sich im Kreis zu drehen. Aktualisiert werden nur die Zahlen.
func refine(g *board.Game, an katago.Analyzer, opt Options, req katago.Request,
	positions []*board.Board, report *GameReport) error {

	if opt.RefineVisits <= opt.Visits || len(report.Strands) == 0 {
		return nil
	}

	top := opt.RefineTop

	if top <= 0 {
		top = defaultRefineTop
	}

	if top > len(report.Strands) {
		top = len(report.Strands)
	}

	// Ein Zug-Report vergleicht die Stellung *vor* und *nach* dem Zug; beide
	// Turns müssen also in der zweiten Runde liegen.
	wanted := map[int]bool{}

	for _, s := range report.Strands[:top] {
		for _, number := range s.Moves {
			wanted[number-1] = true
			wanted[number] = true
		}
	}

	turns := make([]int, 0, len(wanted))

	for turn := range wanted {
		if turn >= 0 && turn < len(positions) {
			turns = append(turns, turn)
		}
	}

	if len(turns) == 0 {
		return nil
	}

	sort.Ints(turns)

	req.MaxVisits = opt.RefineVisits

	if opt.Progress {
		fmt.Fprintf(os.Stderr,
			"teaching: rechne %d Stellungen der %d stärksten Stränge mit "+
				"%d Visits nach ...\n", len(turns), top, opt.RefineVisits)
	}

	analyses, err := an.AnalyzeGame(req, turns)

	if err != nil {
		return err
	}

	byTurn := map[int]*katago.Result{}
	nsq := g.Size * g.Size

	for _, a := range analyses {
		if len(a.Ownership) != nsq {
			return fmt.Errorf(
				"teaching: Ownership-Länge %d ≠ %d (Turn %d) — Config prüfen",
				len(a.Ownership), nsq, a.TurnNumber)
		}

		byTurn[a.TurnNumber] = a
	}

	refined := 0

	for i := range report.Moves {
		number := report.Moves[i].Number
		before, okBefore := byTurn[number-1]
		after, okAfter := byTurn[number]

		if !okBefore || !okAfter {
			continue
		}

		index := number - 1

		if index < 0 || index+1 >= len(positions) || index >= len(g.Moves) {
			continue
		}

		report.Moves[i] = buildReport(index, g.Moves[index], g.Size,
			positions[index], positions[index+1], before, after, opt.Tau)
		refined++
	}

	recomputeStrandFacts(report)

	if opt.Progress {
		fmt.Fprintf(os.Stderr, "teaching: %d Zug-Reports verfeinert\n", refined)
	}

	return nil
}

// recomputeStrandFacts zieht die Strang-Bilanzen aus den — jetzt
// verfeinerten — Zug-Reports neu.
func recomputeStrandFacts(report *GameReport) {
	byNumber := map[int]*MoveReport{}

	for i := range report.Moves {
		byNumber[report.Moves[i].Number] = &report.Moves[i]
	}

	for i := range report.Strands {
		s := &report.Strands[i]

		s.PointsLost = map[string]float64{}
		s.Captures = 0
		s.Worst = nil

		for _, number := range s.Moves {
			rep := byNumber[number]

			if rep == nil {
				continue
			}

			s.PointsLost[rep.Player] += rep.PointsLost

			for _, effect := range rep.Effects {
				if effect.Captured {
					s.Captures += effect.Stones
				}
			}

			if s.Worst == nil || rep.PointsLost > s.Worst.PointsLost {
				s.Worst = &MoveRef{
					Number:     rep.Number,
					Player:     rep.Player,
					Coord:      rep.Coord,
					PointsLost: rep.PointsLost,
					Category:   rep.Category,
				}
			}
		}

		s.Text = strandText(s)
	}
}
