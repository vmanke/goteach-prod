package groups

import (
	"testing"

	"github.com/vmanke/goteach-prod/board"
)

func put(t *testing.T, b *board.Board, c board.Color, pts ...[2]int) {
	t.Helper()

	for _, p := range pts {
		if err := b.SetStone(board.Point{X: p[0], Y: p[1]}, c); err != nil {
			t.Fatal(err)
		}
	}
}

// Zwei echte Ein-Punkt-Augen → unbedingt lebendig.
func TestBensonTwoEyesAlive(t *testing.T) {
	b := board.New(5)

	// X X X X X
	// X . X . X
	// X X X X X
	put(t, b, board.Black,
		[2]int{0, 0}, [2]int{1, 0}, [2]int{2, 0}, [2]int{3, 0}, [2]int{4, 0},
		[2]int{0, 1}, [2]int{2, 1}, [2]int{4, 1},
		[2]int{0, 2}, [2]int{1, 2}, [2]int{2, 2}, [2]int{3, 2}, [2]int{4, 2},
	)

	alive := UnconditionallyAlive(b, board.Black)

	if !alive[board.Point{X: 0, Y: 0}] || !alive[board.Point{X: 2, Y: 1}] {
		t.Fatalf("Zwei-Augen-Kette nicht als unbedingt lebendig erkannt: %v",
			alive)
	}
}

// Nur ein Auge, Rest offen → NICHT unbedingt lebendig.
func TestBensonOneEyeNotAlive(t *testing.T) {
	b := board.New(5)

	// X X X . .
	// X . X . .
	// X X X . .
	put(t, b, board.Black,
		[2]int{0, 0}, [2]int{1, 0}, [2]int{2, 0},
		[2]int{0, 1}, [2]int{2, 1},
		[2]int{0, 2}, [2]int{1, 2}, [2]int{2, 2},
	)

	alive := UnconditionallyAlive(b, board.Black)

	if len(alive) != 0 {
		t.Fatalf("Ein-Auge-Kette fälschlich als lebendig markiert: %v", alive)
	}
}
