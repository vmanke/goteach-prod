package katago

import (
	"fmt"
	"math"

	"github.com/vmanke/goteach-prod/board"
	"github.com/vmanke/goteach-prod/strength"
)

// Mock ist ein deterministischer Ersatz-Analyzer OHNE KataGo-Binary.
// Er erzeugt SYNTHETISCHE Ownership-Werte über ein einfaches
// Zobrist/Bouzy-artiges Distanz-Abkling-Einflussmodell. Die Zahlen sind
// NICHT spielstark und dienen ausschließlich Tests, CI und Pipeline-Demos.
// Jede Nutzung wird im CLI deutlich als MOCK gekennzeichnet.
type Mock struct{}

func (Mock) Close() error { return nil }

func (m Mock) AnalyzeGame(req Request, turns []int) ([]*Result, error) {
	out := make([]*Result, 0, len(turns))

	err := m.AnalyzeGameStream(req, turns, func(res *Result) error {
		out = append(out, res)

		return nil
	})

	if err != nil {
		return nil, err
	}

	return out, nil
}

// AnalyzeGameStream rechnet Turn für Turn und gibt jedes Ergebnis sofort
// weiter — der Mock kann das, weil er ohnehin je Stellung rechnet.
func (Mock) AnalyzeGameStream(req Request, turns []int,
	emit func(*Result) error) error {

	for _, t := range turns {
		res, err := mockAnalyzeTurn(req, t)

		if err != nil {
			return err
		}

		if err := emit(res); err != nil {
			return err
		}
	}

	return nil
}

func mockAnalyzeTurn(req Request, turn int) (*Result, error) {
	if turn < 0 || turn > len(req.Moves) {
		return nil, fmt.Errorf("mock: ungültiger Turn %d", turn)
	}

	b := board.New(req.Size)

	place := func(entry [2]string, viaPlay bool) error {
		c := board.Black

		if entry[0] == "W" || entry[0] == "w" {
			c = board.White
		}

		pt, pass, err := board.FromGTP(entry[1], req.Size)

		if err != nil {
			return err
		}

		if pass {
			return b.Play(board.Move{Color: c, Pass: true})
		}

		if viaPlay {
			return b.Play(board.Move{Color: c, Point: pt})
		}

		return b.SetStone(pt, c)
	}

	for _, s := range req.InitialStones {
		if err := place(s, false); err != nil {
			return nil, fmt.Errorf("mock: initialStones: %w", err)
		}
	}

	for i := 0; i < turn; i++ {
		if err := place(req.Moves[i], true); err != nil {
			return nil, fmt.Errorf("mock: Zug %d: %w", i+1, err)
		}
	}

	n := req.Size * req.Size
	own := make([]float64, n)

	influence := func(c board.Color) []float64 {
		out := make([]float64, n)
		src := b.Stones(c)

		if len(src) == 0 {
			return out
		}

		dist := strength.Distances(req.Size, src)

		for i, d := range dist {
			out[i] = math.Exp(-float64(d) / 2.5)
		}

		return out
	}

	ib := influence(board.Black)
	iw := influence(board.White)
	sum := 0.0

	for i := range own {
		own[i] = math.Tanh(1.6 * (ib[i] - iw[i]))
		sum += own[i]
	}

	winrateBlack := 0.5 + 0.45*math.Tanh(sum/40.0)
	scoreLeadBlack := 0.5*sum - req.Komi

	// "Bester" Zug: umkämpfter leerer Punkt mit hoher beidseitiger Nähe.
	bestIdx, bestVal := -1, -1.0

	for y := 0; y < req.Size; y++ {
		for x := 0; x < req.Size; x++ {
			p := board.Point{X: x, Y: y}
			i := b.Idx(p)

			if b.Get(p) != board.Empty {
				continue
			}

			v := (1.0 - math.Abs(own[i])) * (ib[i] + iw[i])

			if v > bestVal {
				bestVal = v
				bestIdx = i
			}
		}
	}

	var infos []MoveInfo

	if bestIdx >= 0 {
		bp := board.Point{X: bestIdx % req.Size, Y: bestIdx / req.Size}
		mv := board.ToGTP(bp, req.Size)

		infos = append(infos, MoveInfo{
			Move:      mv,
			Visits:    1,
			Winrate:   winrateBlack,
			ScoreLead: scoreLeadBlack,
			Order:     0,
			PV:        []string{mv},
		})
	}

	return &Result{
		ID:         "mock",
		TurnNumber: turn,
		MoveInfos:  infos,
		RootInfo: RootInfo{
			Winrate:   winrateBlack,
			ScoreLead: scoreLeadBlack,
			Visits:    1,
		},
		Ownership: own,
	}, nil
}
