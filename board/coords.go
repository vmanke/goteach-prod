package board

import (
	"fmt"
	"strings"
)

// gtpCols sind die GTP-Spaltenbuchstaben; "I" wird per Konvention übersprungen.
const gtpCols = "ABCDEFGHJKLMNOPQRSTUVWXYZ"

// ToGTP wandelt einen Punkt in eine GTP-Koordinate um (z. B. {3,3} → "D16"
// auf 19×19). Zeilen zählen in GTP von unten, intern von oben.
func ToGTP(p Point, size int) string {
	return fmt.Sprintf("%c%d", gtpCols[p.X], size-p.Y)
}

// FromGTP parst eine GTP-Koordinate ("D16", "pass") in einen Move-Punkt.
func FromGTP(s string, size int) (Point, bool, error) {
	s = strings.ToUpper(strings.TrimSpace(s))

	if s == "PASS" {
		return Point{}, true, nil
	}

	if len(s) < 2 {
		return Point{}, false, fmt.Errorf("coords: ungültige GTP-Koordinate %q", s)
	}

	x := strings.IndexByte(gtpCols, s[0])

	if x < 0 || x >= size {
		return Point{}, false, fmt.Errorf("coords: ungültige Spalte in %q", s)
	}

	var row int

	if _, err := fmt.Sscanf(s[1:], "%d", &row); err != nil || row < 1 || row > size {
		return Point{}, false, fmt.Errorf("coords: ungültige Zeile in %q", s)
	}

	return Point{X: x, Y: size - row}, false, nil
}

// FromSGF parst eine SGF-Punktangabe ("pd") in einen Punkt; "" und "tt"
// (bei size ≤ 19) bedeuten Pass.
func FromSGF(v string, size int) (Point, bool, error) {
	if v == "" || (v == "tt" && size <= 19) {
		return Point{}, true, nil
	}

	if len(v) != 2 {
		return Point{}, false, fmt.Errorf("coords: ungültige SGF-Koordinate %q", v)
	}

	x := int(v[0] - 'a')
	y := int(v[1] - 'a')

	if x < 0 || x >= size || y < 0 || y >= size {
		return Point{}, false, fmt.Errorf("coords: SGF-Koordinate %q außerhalb", v)
	}

	return Point{X: x, Y: y}, false, nil
}
