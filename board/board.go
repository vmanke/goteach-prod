// Package board implementiert den symbolischen Brettzustand (Stufe 3 der
// Architektur): Steine, Züge, Schlagen, Selbstmord- und einfaches Ko-Verbot,
// Zobrist-Hashing sowie SGF-Ein-/Ausgabe und Koordinatenkonvertierung.
package board

import (
	"fmt"
	"math/rand"
)

// Color ist der Zustand eines Schnittpunkts.
type Color int8

const (
	Empty Color = iota
	Black
	White
)

// Opponent liefert die Gegenfarbe.
func (c Color) Opponent() Color {
	switch c {
	case Black:
		return White
	case White:
		return Black
	}

	return Empty
}

// String liefert die GTP-übliche Kurzform.
func (c Color) String() string {
	switch c {
	case Black:
		return "B"
	case White:
		return "W"
	}

	return "."
}

// Point ist ein Schnittpunkt; X = Spalte (0 = links), Y = Zeile (0 = oben).
// Diese Orientierung entspricht exakt der KataGo-Ownership-Reihenfolge
// (row-major, beginnend oben links bei A19; verifiziert in
// docs/Analysis_Engine.md des KataGo-Repos).
type Point struct {
	X int
	Y int
}

// Move ist ein Spielzug; Pass == true ignoriert Point.
type Move struct {
	Color Color
	Point Point
	Pass  bool
}

// Board ist ein quadratisches Go-Brett mit inkrementellem Zobrist-Hash.
type Board struct {
	Size int

	grid    []Color
	zobrist [][2]uint64
	hash    uint64
	koHash  uint64

	// Captured[Black] = Anzahl bislang geschlagener schwarzer Steine usw.
	Captured [3]int
}

// New erzeugt ein leeres Brett der Kantenlänge size.
func New(size int) *Board {
	if size < 2 || size > 25 {
		panic(fmt.Sprintf("board: unzulässige Größe %d", size))
	}

	n := size * size
	rng := rand.New(rand.NewSource(0x5EED_C0FFEE))
	zb := make([][2]uint64, n)

	for i := range zb {
		zb[i][0] = rng.Uint64()
		zb[i][1] = rng.Uint64()
	}

	return &Board{
		Size:    size,
		grid:    make([]Color, n),
		zobrist: zb,
	}
}

// Clone liefert eine tiefe Kopie (Zobrist-Tabelle wird geteilt, da read-only).
func (b *Board) Clone() *Board {
	nb := *b
	nb.grid = make([]Color, len(b.grid))
	copy(nb.grid, b.grid)

	return &nb
}

// Idx liefert den Index von p in row-major-Ordnung (kompatibel zur
// KataGo-Ownership-Reihenfolge).
func (b *Board) Idx(p Point) int {
	return p.Y*b.Size + p.X
}

// InBounds prüft die Brettgrenzen.
func (b *Board) InBounds(p Point) bool {
	return p.X >= 0 && p.X < b.Size && p.Y >= 0 && p.Y < b.Size
}

// Get liefert die Farbe an p.
func (b *Board) Get(p Point) Color {
	return b.grid[b.Idx(p)]
}

// Hash liefert den aktuellen Zobrist-Hash der Stellung.
func (b *Board) Hash() uint64 {
	return b.hash
}

func (b *Board) set(p Point, c Color) {
	i := b.Idx(p)
	old := b.grid[i]

	if old == Black {
		b.hash ^= b.zobrist[i][0]
	} else if old == White {
		b.hash ^= b.zobrist[i][1]
	}

	if c == Black {
		b.hash ^= b.zobrist[i][0]
	} else if c == White {
		b.hash ^= b.zobrist[i][1]
	}

	b.grid[i] = c
}

// SetStone setzt einen Stein ohne Regelprüfung (für AB/AW-Setup, Handicap).
func (b *Board) SetStone(p Point, c Color) error {
	if !b.InBounds(p) {
		return fmt.Errorf("board: Punkt %v außerhalb", p)
	}

	b.set(p, c)

	return nil
}

// Neighbors liefert die orthogonalen Nachbarn von p (2 bis 4 Punkte).
func (b *Board) Neighbors(p Point) []Point {
	out := make([]Point, 0, 4)

	for _, d := range [4]Point{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		q := Point{p.X + d.X, p.Y + d.Y}

		if b.InBounds(q) {
			out = append(out, q)
		}
	}

	return out
}

// chainAndLiberties liefert alle Steine der Kette an p sowie deren
// Freiheitenzahl (BFS, O(Kettengröße)).
func (b *Board) chainAndLiberties(p Point) (stones []Point, liberties int) {
	c := b.Get(p)

	if c == Empty {
		return nil, 0
	}

	visited := make([]bool, len(b.grid))
	libSeen := make([]bool, len(b.grid))
	queue := []Point{p}
	visited[b.Idx(p)] = true

	for len(queue) > 0 {
		cur := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		stones = append(stones, cur)

		for _, q := range b.Neighbors(cur) {
			qi := b.Idx(q)

			switch b.Get(q) {
			case Empty:
				if !libSeen[qi] {
					libSeen[qi] = true
					liberties++
				}
			case c:
				if !visited[qi] {
					visited[qi] = true
					queue = append(queue, q)
				}
			}
		}
	}

	return stones, liberties
}

// Liberties liefert die Freiheitenzahl der Kette an p (0, falls leer).
func (b *Board) Liberties(p Point) int {
	_, libs := b.chainAndLiberties(p)

	return libs
}

func (b *Board) removeChain(stones []Point, c Color) {
	for _, s := range stones {
		b.set(s, Empty)
	}

	b.Captured[c] += len(stones)
}

// Play führt einen Zug regelkonform aus: Schlagen gegnerischer Ketten ohne
// Freiheiten, Selbstmordverbot, einfaches Ko (Verbot der unmittelbaren
// Stellungswiederholung der Position vor dem letzten gegnerischen Zug).
// Hinweis: Positional Superko ist bewusst nicht implementiert; für die
// Analyse legaler SGF-Partien genügt einfaches Ko.
func (b *Board) Play(m Move) error {
	if m.Pass {
		// Nach einem Pass ist die Ko-Sperre aufgehoben.
		b.koHash = 0

		return nil
	}

	if m.Color != Black && m.Color != White {
		return fmt.Errorf("board: ungültige Farbe %v", m.Color)
	}

	if !b.InBounds(m.Point) {
		return fmt.Errorf("board: Punkt %v außerhalb", m.Point)
	}

	if b.Get(m.Point) != Empty {
		return fmt.Errorf("board: Punkt %v ist besetzt", m.Point)
	}

	preHash := b.hash
	b.set(m.Point, m.Color)

	// Gegnerische Nachbarketten ohne Freiheiten schlagen.
	opp := m.Color.Opponent()
	captured := 0
	var removed []Point

	for _, q := range b.Neighbors(m.Point) {
		if b.Get(q) != opp {
			continue
		}

		stones, libs := b.chainAndLiberties(q)

		if libs == 0 {
			removed = append(removed, stones...)
			b.removeChain(stones, opp)
			captured += len(stones)
		}
	}

	undo := func() {
		for _, s := range removed {
			b.set(s, opp)
		}

		b.Captured[opp] -= captured
		b.set(m.Point, Empty)
	}

	// Selbstmordverbot.
	if _, libs := b.chainAndLiberties(m.Point); libs == 0 {
		undo()

		return fmt.Errorf("board: Selbstmordzug auf %v verboten", m.Point)
	}

	// Einfaches Ko: Wiederholung der Stellung vor dem letzten Zug verboten.
	if captured == 1 && b.hash == b.koHash {
		undo()

		return fmt.Errorf("board: Ko-Verbot auf %v", m.Point)
	}

	b.koHash = preHash

	return nil
}

// Stones liefert alle Punkte der Farbe c.
func (b *Board) Stones(c Color) []Point {
	var out []Point

	for y := 0; y < b.Size; y++ {
		for x := 0; x < b.Size; x++ {
			p := Point{x, y}

			if b.Get(p) == c {
				out = append(out, p)
			}
		}
	}

	return out
}

// String rendert das Brett als ASCII (Zeile 0 oben), für Logs und Tests.
func (b *Board) String() string {
	out := make([]byte, 0, (b.Size*2+1)*b.Size)

	for y := 0; y < b.Size; y++ {
		for x := 0; x < b.Size; x++ {
			switch b.Get(Point{x, y}) {
			case Black:
				out = append(out, 'X', ' ')
			case White:
				out = append(out, 'O', ' ')
			default:
				out = append(out, '.', ' ')
			}
		}

		out = append(out, '\n')
	}

	return string(out)
}
