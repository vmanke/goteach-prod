package shapes

import (
	"sort"
	"strings"

	"github.com/vmanke/goteach-prod/board"
)

// Instance ist eine gefundene Form auf einem konkreten Brett.
type Instance struct {
	Name     string        `json:"name"`
	Japanese string        `json:"japanese,omitempty"`
	Color    string        `json:"color"`
	Points   []string      `json:"points"`
	Teaching string        `json:"teaching,omitempty"`
	Stones   []board.Point `json:"-"`
}

// variant ist eine Schablone in einer festen Orientierung.
type variant struct {
	template *Template
	rows     []string
	width    int
	height   int

	// bicolour markiert Muster, die beide Farben enthalten (Kreuzschnitt).
	// Bei ihnen liefert der Farbtausch dieselbe Steinmenge noch einmal, nur
	// mit vertauschten Rollen — die Form gehört dort keiner Farbe.
	bicolour bool
}

// colourName liefert die deutsche Bezeichnung; Color.String() ist die
// GTP-Kurzform und für Lehrtexte unbrauchbar.
func colourName(c board.Color) string {
	if c == board.Black {
		return "Schwarz"
	}

	return "Weiß"
}

// bothColours ist die Farbangabe für Muster ohne eigene Farbzugehörigkeit.
const bothColours = "beide"

// variants hält alle Orientierungen aller Katalogeinträge; einmal beim Start
// erzeugt, danach nur noch gelesen.
var variants = buildVariants()

func buildVariants() []variant {
	var out []variant

	for i := range Catalog {
		seen := map[string]bool{}

		for _, rows := range orientations(Catalog[i].Rows) {
			key := strings.Join(rows, "/")

			// Symmetrische Muster fallen bei mehreren Drehungen auf dieselbe
			// Form zurück; die doppelt zu prüfen wäre reine Arbeit.
			if seen[key] {
				continue
			}

			seen[key] = true
			out = append(out, variant{
				template: &Catalog[i],
				rows:     rows,
				width:    len(rows[0]),
				height:   len(rows),
				bicolour: strings.ContainsRune(
					strings.Join(Catalog[i].Rows, ""), CellOpponent),
			})
		}
	}

	return out
}

// orientations liefert die acht Symmetrien des Quadrats.
func orientations(rows []string) [][]string {
	out := make([][]string, 0, 8)
	current := rows

	for turn := 0; turn < 4; turn++ {
		out = append(out, current, mirror(current))
		current = rotate(current)
	}

	return out
}

// rotate dreht das Muster um 90 Grad im Uhrzeigersinn.
func rotate(rows []string) []string {
	height, width := len(rows), len(rows[0])
	out := make([]string, width)

	for x := 0; x < width; x++ {
		var sb strings.Builder

		for y := height - 1; y >= 0; y-- {
			sb.WriteByte(rows[y][x])
		}

		out[x] = sb.String()
	}

	return out
}

// mirror spiegelt das Muster an der senkrechten Achse.
func mirror(rows []string) []string {
	out := make([]string, len(rows))

	for i, row := range rows {
		b := []byte(row)

		for l, r := 0, len(b)-1; l < r; l, r = l+1, r-1 {
			b[l], b[r] = b[r], b[l]
		}

		out[i] = string(b)
	}

	return out
}

// Find liefert alle Formen des Katalogs auf dem Brett, deterministisch
// sortiert nach Name und Lage.
//
// Jede Form wird in beiden Farbrollen gesucht: Ein leeres Dreieck ist ein
// leeres Dreieck, egal wer es gebaut hat. Mehrfachtreffer derselben
// Steinmenge — bei symmetrischen Mustern der Normalfall — werden zusammen-
// gefasst.
func Find(b *board.Board) []Instance {
	found := map[string]Instance{}

	for _, v := range variants {
		roles := []board.Color{board.Black, board.White}

		if v.bicolour {
			// Beide Rollen ergäben dieselben Steine; einmal genügt.
			roles = roles[:1]
		}

		for _, own := range roles {
			collect(b, v, own, found)
		}
	}

	out := make([]Instance, 0, len(found))

	for _, inst := range found {
		out = append(out, inst)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}

		if out[i].Color != out[j].Color {
			return out[i].Color < out[j].Color
		}

		return strings.Join(out[i].Points, ",") < strings.Join(out[j].Points, ",")
	})

	return out
}

func collect(b *board.Board, v variant, own board.Color, found map[string]Instance) {
	opponent := own.Opponent()

	for oy := 0; oy <= b.Size-v.height; oy++ {
		for ox := 0; ox <= b.Size-v.width; ox++ {
			stones := matchAt(b, v, ox, oy, own, opponent)

			if stones == nil {
				continue
			}

			coords := make([]string, len(stones))

			for i, s := range stones {
				coords[i] = board.ToGTP(s, b.Size)
			}

			colour := colourName(own)

			if v.bicolour {
				colour = bothColours
			}

			inst := Instance{
				Name:     v.template.Name,
				Japanese: v.template.Japanese,
				Color:    colour,
				Points:   coords,
				Teaching: v.template.Teaching,
				Stones:   stones,
			}

			found[inst.Name+"|"+inst.Color+"|"+strings.Join(coords, ",")] = inst
		}
	}
}

// matchAt prüft eine Orientierung an einer Position und liefert die
// beteiligten Steine, oder nil.
func matchAt(b *board.Board, v variant, ox, oy int, own, opponent board.Color) []board.Point {
	var stones []board.Point

	for y := 0; y < v.height; y++ {
		for x := 0; x < v.width; x++ {
			cell := v.rows[y][x]

			if cell == CellAny {
				continue
			}

			p := board.Point{X: ox + x, Y: oy + y}
			got := b.Get(p)

			switch cell {
			case CellEmpty:
				if got != board.Empty {
					return nil
				}

			case CellOwn:
				if got != own {
					return nil
				}

				stones = append(stones, p)

			case CellOpponent:
				if got != opponent {
					return nil
				}

				stones = append(stones, p)

			default:
				return nil
			}
		}
	}

	sort.Slice(stones, func(i, j int) bool {
		if stones[i].Y != stones[j].Y {
			return stones[i].Y < stones[j].Y
		}

		return stones[i].X < stones[j].X
	})

	return stones
}
