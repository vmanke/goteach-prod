package board

import "testing"

func TestErgebnisAusDemSGF(t *testing.T) {
	cases := []struct {
		re      string
		winner  Color
		margin  float64
		numeric bool
	}{
		{"B+24.5", Black, 24.5, true},
		{"W+3", White, 3, true},
		{"w+0.5", White, 0.5, true},
		{"B+R", Black, 0, false},
		{"W+Resign", White, 0, false},
		{"B+T", Black, 0, false},
		{"0", Empty, 0, true},
		{"Draw", Empty, 0, true},
		{"?", Empty, 0, false},
		{"", Empty, 0, false},
		{"Unsinn", Empty, 0, false},
	}

	for _, tc := range cases {
		g := &Game{Result: tc.re}
		winner, margin, numeric := g.ResultMargin()

		if winner != tc.winner || margin != tc.margin || numeric != tc.numeric {
			t.Errorf("%q: %v %v %v, erwartet %v %v %v", tc.re,
				winner, margin, numeric, tc.winner, tc.margin, tc.numeric)
		}
	}

	g, err := ParseSGF("(;GM[1]SZ[9]KM[6.5]RE[B+2.5];B[ee];W[cc])")

	if err != nil {
		t.Fatal(err)
	}

	if g.Result != "B+2.5" {
		t.Fatalf("Result %q", g.Result)
	}
}
