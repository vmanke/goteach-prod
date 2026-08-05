// Package groups implementiert die Gruppensegmentierung (Stufe 4 der
// Architektur): Ketten (verbundene Steine gleicher Farbe) mit Freiheiten
// sowie Bensons Test auf unbedingtes Leben als exakten, konservativen
// Cross-Check gegen das neuronale Stärkemaß.
package groups

import (
	"github.com/vmanke/goteach/board"
)

// Chain ist eine maximal verbundene Steinkette einer Farbe.
type Chain struct {
	Color     board.Color
	Stones    []board.Point
	Liberties []board.Point
}

// Rep liefert einen deterministischen Repräsentanten (kleinster Index).
func (c *Chain) Rep(size int) board.Point {
	best := c.Stones[0]
	bestIdx := best.Y*size + best.X

	for _, s := range c.Stones[1:] {
		if i := s.Y*size + s.X; i < bestIdx {
			best = s
			bestIdx = i
		}
	}

	return best
}

// FindChains liefert alle Ketten des Bretts (beide Farben), deterministisch
// in Scanreihenfolge, jeweils mit Freiheitenliste. Laufzeit O(N).
func FindChains(b *board.Board) []*Chain {
	n := b.Size * b.Size
	visited := make([]bool, n)
	var out []*Chain

	for y := 0; y < b.Size; y++ {
		for x := 0; x < b.Size; x++ {
			p := board.Point{X: x, Y: y}
			pi := b.Idx(p)

			if visited[pi] || b.Get(p) == board.Empty {
				continue
			}

			c := b.Get(p)
			ch := &Chain{Color: c}
			libSeen := make(map[int]bool)
			queue := []board.Point{p}
			visited[pi] = true

			for len(queue) > 0 {
				cur := queue[len(queue)-1]
				queue = queue[:len(queue)-1]
				ch.Stones = append(ch.Stones, cur)

				for _, q := range b.Neighbors(cur) {
					qi := b.Idx(q)

					switch b.Get(q) {
					case board.Empty:
						if !libSeen[qi] {
							libSeen[qi] = true
							ch.Liberties = append(ch.Liberties, q)
						}

					case c:
						if !visited[qi] {
							visited[qi] = true
							queue = append(queue, q)
						}
					}
				}
			}

			out = append(out, ch)
		}
	}

	return out
}

// ChainAt liefert die Kette am Punkt p oder nil, falls leer.
func ChainAt(b *board.Board, p board.Point) *Chain {
	if b.Get(p) == board.Empty {
		return nil
	}

	for _, ch := range FindChains(b) {
		for _, s := range ch.Stones {
			if s == p {
				return ch
			}
		}
	}

	return nil
}
