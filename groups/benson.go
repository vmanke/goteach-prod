package groups

import (
	"github.com/vmanke/goteach-prod/board"
)

// UnconditionallyAlive implementiert Bensons Algorithmus (Benson 1976,
// "Life in the Game of Go", Information Sciences 10:17–29): Eine Kette der
// Farbe c lebt unbedingt, wenn sie mindestens zwei "vitale" c-umschlossene
// Regionen besitzt. Eine Region R (maximale Zusammenhangskomponente aus
// Nicht-c-Punkten) ist vital für Kette K, wenn JEDER leere Punkt von R eine
// Freiheit von K ist. Iterativ werden Ketten mit < 2 vitalen Regionen sowie
// Regionen entfernt, die an entfernte Ketten grenzen, bis Fixpunkt erreicht
// ist. Das Ergebnis ist beweisbar korrekt, aber konservativ (Seki u. Ä.
// zählen nicht als unbedingt lebendig).
//
// Rückgabe: Menge aller Steinpunkte unbedingt lebender c-Ketten.
func UnconditionallyAlive(b *board.Board, c board.Color) map[board.Point]bool {
	n := b.Size * b.Size

	// Ketten der Farbe c indizieren.
	var chains []*Chain

	for _, ch := range FindChains(b) {
		if ch.Color == c {
			chains = append(chains, ch)
		}
	}

	stoneToChain := make([]int, n)

	for i := range stoneToChain {
		stoneToChain[i] = -1
	}

	for id, ch := range chains {
		for _, s := range ch.Stones {
			stoneToChain[b.Idx(s)] = id
		}
	}

	// adjChains(e): an Punkt e angrenzende c-Ketten (für Vitalitätsprüfung).
	adjChains := func(p board.Point) map[int]bool {
		out := map[int]bool{}

		for _, q := range b.Neighbors(p) {
			if id := stoneToChain[b.Idx(q)]; id >= 0 {
				out[id] = true
			}
		}

		return out
	}

	// Regionen: Zusammenhangskomponenten aller Nicht-c-Punkte.
	type region struct {
		empties   []board.Point
		neighbors map[int]bool
	}

	visited := make([]bool, n)
	var regions []*region

	for y := 0; y < b.Size; y++ {
		for x := 0; x < b.Size; x++ {
			p := board.Point{X: x, Y: y}
			pi := b.Idx(p)

			if visited[pi] || b.Get(p) == c {
				continue
			}

			r := &region{neighbors: map[int]bool{}}
			queue := []board.Point{p}
			visited[pi] = true

			for len(queue) > 0 {
				cur := queue[len(queue)-1]
				queue = queue[:len(queue)-1]

				if b.Get(cur) == board.Empty {
					r.empties = append(r.empties, cur)
				}

				for _, q := range b.Neighbors(cur) {
					qi := b.Idx(q)

					if b.Get(q) == c {
						r.neighbors[stoneToChain[qi]] = true

						continue
					}

					if !visited[qi] {
						visited[qi] = true
						queue = append(queue, q)
					}
				}
			}

			regions = append(regions, r)
		}
	}

	// Vitalität vorbereiten: vital[r][k] ⇔ jeder leere Punkt von r grenzt an k.
	vital := func(r *region, chainID int) bool {
		if !r.neighbors[chainID] {
			return false
		}

		for _, e := range r.empties {
			if !adjChains(e)[chainID] {
				return false
			}
		}

		return true
	}

	alive := map[int]bool{}

	for id := range chains {
		alive[id] = true
	}

	activeRegion := make([]bool, len(regions))

	for i := range activeRegion {
		activeRegion[i] = true
	}

	// Fixpunktiteration.
	for {
		removedAny := false

		for id := range chains {
			if !alive[id] {
				continue
			}

			count := 0

			for ri, r := range regions {
				if activeRegion[ri] && vital(r, id) {
					count++

					if count >= 2 {
						break
					}
				}
			}

			if count < 2 {
				delete(alive, id)
				removedAny = true
			}
		}

		if !removedAny {
			break
		}

		// Regionen streichen, die an eine entfernte Kette grenzen.
		for ri, r := range regions {
			if !activeRegion[ri] {
				continue
			}

			for id := range r.neighbors {
				if !alive[id] {
					activeRegion[ri] = false

					break
				}
			}
		}
	}

	out := map[board.Point]bool{}

	for id := range alive {
		for _, s := range chains[id].Stones {
			out[s] = true
		}
	}

	return out
}
