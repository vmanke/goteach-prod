// Package strength implementiert das situative Stärkemaß pro Gruppe
// (Stufe 5 der Architektur): eine distanzgewichtete Aggregation des
// KataGo-Ownership-Felds über Gruppe plus Nachbarschaft — die neuronal
// fundierte Variante klassischer Einflussmodelle (Zobrist 1969, Bouzy 2003).
package strength

import (
	"math"

	"github.com/vmanke/goteach/board"
)

// Distances liefert per Multi-Source-BFS die Gitterdistanz jedes Punkts zur
// nächstgelegenen Quelle (Quellpunkte = 0). Laufzeit O(N).
func Distances(size int, sources []board.Point) []int {
	n := size * size
	dist := make([]int, n)

	for i := range dist {
		dist[i] = -1
	}

	queue := make([]int, 0, n)

	for _, s := range sources {
		i := s.Y*size + s.X
		dist[i] = 0
		queue = append(queue, i)
	}

	for head := 0; head < len(queue); head++ {
		i := queue[head]
		x, y := i%size, i/size

		for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			nx, ny := x+d[0], y+d[1]

			if nx < 0 || nx >= size || ny < 0 || ny >= size {
				continue
			}

			j := ny*size + nx

			if dist[j] < 0 {
				dist[j] = dist[i] + 1
				queue = append(queue, j)
			}
		}
	}

	return dist
}

// Group berechnet die situative Stärke einer Kette als distanzgewichtete
// Aggregation des Ownership-Felds (aus Schwarz-Sicht, row-major ab oben
// links — exakt die KataGo-Analysis-Ordnung):
//
//	strength = Σ_p exp(-d(p, Gruppe)/tau) · sign(c) · ownership_black(p)
//	           ─────────────────────────────────────────────────────────
//	           Σ_p exp(-d(p, Gruppe)/tau)
//
// Wertebereich [-1, +1]: +1 = maximal stark aus Sicht der Gruppenfarbe.
// tau ist die Abkling-Längenskala der Nachbarschaft (Default 3.0).
func Group(size int, ownershipBlack []float64, stones []board.Point,
	c board.Color, tau float64) float64 {

	if len(stones) == 0 || len(ownershipBlack) != size*size {
		return math.NaN()
	}

	if tau <= 0 {
		tau = 3.0
	}

	sign := 1.0

	if c == board.White {
		sign = -1.0
	}

	dist := Distances(size, stones)

	var accum, total float64

	for i, d := range dist {
		w := math.Exp(-float64(d) / tau)

		accum += w * sign * ownershipBlack[i]
		total += w
	}

	return accum / total
}
