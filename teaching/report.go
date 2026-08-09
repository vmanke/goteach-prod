package teaching

import (
	"github.com/vmanke/goteach-prod/board"
	"github.com/vmanke/goteach-prod/katago"
)

// GameReport ist die vollständige Auswertung einer Partie: die Erzählstränge
// als Hauptsicht und die Lehreinheiten pro Zug als Detailebene darunter.
//
// Die Reihenfolge ist Absicht. Eine Partie mit 250 Zügen ergibt 250
// Zug-Lehreinheiten, die nichts voneinander wissen — eine Textwand. Die
// Stränge fassen zusammen, was zusammengehört; wer es genauer wissen will,
// findet die Züge weiterhin einzeln.
type GameReport struct {
	Size  int     `json:"size"`
	Komi  float64 `json:"komi"`
	Rules string  `json:"rules"`

	Strands []Strand     `json:"strands,omitempty"`
	Moves   []MoveReport `json:"moves"`
}

// AnalyzeGame analysiert eine Partie und zerlegt sie zusätzlich in
// Erzählstränge.
//
// Gegenüber Analyze kostet das keinen weiteren Engine-Aufruf: Die
// Strang-Analyse arbeitet auf den Ownership-Feldern, die KataGo ohnehin
// mitliefert und die Analyze bisher nach dem Ableiten der Skalare verwarf.
func AnalyzeGame(g *board.Game, an katago.Analyzer, opt Options) (*GameReport, error) {
	return analyzeCore(g, an, opt, true)
}

// TotalPointsLost summiert den Punktverlust eines Spielers über alle Züge.
func (r *GameReport) TotalPointsLost(player string) float64 {
	var sum float64

	for i := range r.Moves {
		if r.Moves[i].Player == player {
			sum += r.Moves[i].PointsLost
		}
	}

	return sum
}

// StrandMoves liefert die Zugnummern, die überhaupt einem Strang zugeordnet
// wurden. Der Rest der Partie blieb ohne Strang — das ist der Normalfall für
// ruhige Passagen und keine Lücke.
func (r *GameReport) StrandMoves() map[int]int {
	out := map[int]int{}

	for _, s := range r.Strands {
		for _, number := range s.Moves {
			out[number] = s.ID
		}
	}

	return out
}
