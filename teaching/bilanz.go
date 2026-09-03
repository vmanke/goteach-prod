package teaching

import (
	"fmt"
	"math"
	"strings"

	"github.com/vmanke/goteach-prod/board"
)

// deadOwnership ist der Betrag der Zugehörigkeit, ab dem ein Stein auf
// gegnerischem Gebiet als tot gilt.
const deadOwnership = 0.5

// Bilanz ist die Abrechnung der Endstellung: Gefangene aus dem
// Nachspielen, tote Steine aus der Engine-Ownership, die Engine-Schätzung
// und — falls das SGF eines nennt — das eingetragene Ergebnis daneben.
type Bilanz struct {
	// PrisonersByBlack sind die weißen Steine, die Schwarz bis zum letzten
	// analysierten Zug geschlagen hat; PrisonersByWhite entsprechend.
	PrisonersByBlack int `json:"prisonersByBlack"`
	PrisonersByWhite int `json:"prisonersByWhite"`

	// DeadBlack und DeadWhite sind die Steine, die in der Endstellung auf
	// gegnerischem Gebiet stehen (Ownership-Betrag über deadOwnership).
	DeadBlack []string `json:"deadBlack"`
	DeadWhite []string `json:"deadWhite"`

	FinalTurn int `json:"finalTurn"`

	// ScoreLead ist die Engine-Schätzung der Endstellung aus Schwarz-Sicht.
	ScoreLead float64 `json:"scoreLead"`

	// ResultSGF ist das Ergebnis laut SGF, Margin die Punktdifferenz daraus
	// (0, wenn das SGF keine nennt).
	ResultSGF string  `json:"resultSGF,omitempty"`
	Margin    float64 `json:"margin,omitempty"`

	Text string `json:"text"`
}

// buildBilanz rechnet die Endstellung ab. final ist die Stellung nach dem
// letzten analysierten Zug, ownership das Feld der Engine dazu (Schwarz-
// Sicht, wie alles andere), scoreLead ihre Schätzung.
func buildBilanz(g *board.Game, final *board.Board, finalTurn int,
	ownership []float64, scoreLead float64) *Bilanz {

	b := &Bilanz{
		PrisonersByBlack: final.Captured[board.White],
		PrisonersByWhite: final.Captured[board.Black],
		FinalTurn:        finalTurn,
		ScoreLead:        scoreLead,
		ResultSGF:        g.Result,
	}

	var deadBlack, deadWhite []board.Point

	if len(ownership) == g.Size*g.Size {
		for y := 0; y < g.Size; y++ {
			for x := 0; x < g.Size; x++ {
				p := board.Point{X: x, Y: y}
				own := ownership[y*g.Size+x]

				switch final.Get(p) {
				case board.Black:
					if own < -deadOwnership {
						deadBlack = append(deadBlack, p)
					}
				case board.White:
					if own > deadOwnership {
						deadWhite = append(deadWhite, p)
					}
				}
			}
		}
	}

	b.DeadBlack = sortedGTP(deadBlack, g.Size)
	b.DeadWhite = sortedGTP(deadWhite, g.Size)

	if _, margin, numeric := g.ResultMargin(); numeric {
		b.Margin = margin
	}

	b.Text = bilanzText(g, b)

	return b
}

// deadPoints liefert die toten Steine als Punkte, für das Diagramm.
func (b *Bilanz) deadPoints(size int) []board.Point {
	var out []board.Point

	for _, list := range [][]string{b.DeadBlack, b.DeadWhite} {
		for _, coord := range list {
			if p, pass, err := board.FromGTP(coord, size); err == nil && !pass {
				out = append(out, p)
			}
		}
	}

	return out
}

// scoreString schreibt eine Punktdifferenz aus Schwarz-Sicht so, wie ein
// Ergebnis geschrieben wird: B+25.2, W+3.0, oder 0.0 bei Gleichstand.
func scoreString(lead float64) string {
	rounded := math.Round(lead*10) / 10

	switch {
	case rounded > 0:
		return fmt.Sprintf("B+%.1f", rounded)

	case rounded < 0:
		return fmt.Sprintf("W+%.1f", -rounded)
	}

	return "0.0"
}

func bilanzText(g *board.Game, b *Bilanz) string {
	var sb strings.Builder

	fmt.Fprintf(&sb,
		"Bis Zug %d schlug Schwarz %d weiße und Weiß %d schwarze Steine "+
			"(aus den Brettstellungen). In der Endstellung sind laut "+
			"Engine-Ownership %d weiße und %d schwarze Steine tot (Betrag "+
			"über %.1f). Gefangene im Sinne der Serverzählung damit %d für "+
			"Schwarz und %d für Weiß. Engine-Schätzung der Endstellung %s "+
			"(scoreLead, Schwarz-Sicht).",
		b.FinalTurn, b.PrisonersByBlack, b.PrisonersByWhite,
		len(b.DeadWhite), len(b.DeadBlack), deadOwnership,
		b.PrisonersByBlack+len(b.DeadWhite), b.PrisonersByWhite+len(b.DeadBlack),
		scoreString(b.ScoreLead))

	if b.ResultSGF == "" {
		return sb.String()
	}

	winner, margin, numeric := g.ResultMargin()

	if !numeric {
		fmt.Fprintf(&sb, " Ergebnis laut SGF %s.", b.ResultSGF)

		return sb.String()
	}

	signed := margin

	if winner == board.White {
		signed = -margin
	}

	fmt.Fprintf(&sb, " Ergebnis laut SGF %s, Abweichung %.1f Punkte.",
		b.ResultSGF, math.Abs(b.ScoreLead-signed))

	return sb.String()
}
