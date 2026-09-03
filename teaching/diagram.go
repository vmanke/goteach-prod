package teaching

import (
	"fmt"
	"html"
	"strings"
	"unicode"

	"github.com/vmanke/goteach-prod/board"
)

// Maße der SVG-Diagramme in Pixeln. Ein Diagramm zeigt einen Ausschnitt
// des Bretts; Linien, die am Rand des Ausschnitts weiterlaufen, ragen einen
// halben Schritt hinaus, Brettkanten enden auf der letzten Linie.
const (
	svgCell       = 26
	svgStone      = 12
	svgMargin     = 30
	svgTitleLine  = 15
	svgTitleBase  = 36
	svgBottom     = 30
	svgLineBleed  = 13
	svgHoshi      = 2.5
	svgCross      = 8
	svgTitleSize  = 12
	svgLabelSize  = 9
	svgNumberSize = 11
)

// minCropSpan ist die kleinste Kantenlänge eines Ausschnitts. Ein
// Ausschnitt, der an die Brettkante stößt, wird auf der anderen Seite so
// weit verlängert, dass mindestens so viele Linien zu sehen sind.
const minCropSpan = 7

// crop ist ein Brettausschnitt in Brettkoordinaten, beide Enden inklusive.
type crop struct {
	x0, x1, y0, y1 int
}

func (c crop) cols() int { return c.x1 - c.x0 + 1 }
func (c crop) rows() int { return c.y1 - c.y0 + 1 }

func (c crop) contains(p board.Point) bool {
	return p.X >= c.x0 && p.X <= c.x1 && p.Y >= c.y0 && p.Y <= c.y1
}

// cropAround legt den Ausschnitt um die Saatpunkte: Hülle plus Rand, an
// den Brettkanten beschnitten und auf minCropSpan verlängert.
func cropAround(size int, seeds []board.Point, margin int) crop {
	if len(seeds) == 0 {
		return crop{0, size - 1, 0, size - 1}
	}

	c := crop{x0: size, x1: -1, y0: size, y1: -1}

	for _, p := range seeds {
		c.x0 = minInt(c.x0, p.X-margin)
		c.x1 = maxInt(c.x1, p.X+margin)
		c.y0 = minInt(c.y0, p.Y-margin)
		c.y1 = maxInt(c.y1, p.Y+margin)
	}

	c.x0, c.x1 = clipSpan(c.x0, c.x1, size)
	c.y0, c.y1 = clipSpan(c.y0, c.y1, size)

	return c
}

// clipSpan beschneidet ein Intervall auf das Brett und verlängert es auf
// der freien Seite, bis es minCropSpan Linien umfasst.
func clipSpan(lo, hi, size int) (int, int) {
	if lo < 0 {
		lo = 0
	}

	if hi > size-1 {
		hi = size - 1
	}

	for hi-lo+1 < minCropSpan {
		grown := false

		if hi < size-1 {
			hi++
			grown = true
		}

		if hi-lo+1 < minCropSpan && lo > 0 {
			lo--
			grown = true
		}

		if !grown {
			break
		}
	}

	return lo, hi
}

func minInt(a, b int) int {
	if a < b {
		return a
	}

	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}

	return b
}

// diagram ist ein Brettausschnitt mit Steinen, Zugnummern und Markierungen.
type diagram struct {
	size  int
	crop  crop
	title []string
	board *board.Board

	// labels trägt die Zugnummer zu einem Stein; ring markiert einen Punkt
	// rot, ringText steht in einem leeren Ring; crosses kreuzt Steine.
	labels   map[board.Point]int
	ring     *board.Point
	ringText string
	crosses  []board.Point
}

// titleSegments fügt die Teile eines Titels zu Zeilen zusammen, die in die
// Breite passen. Die Schriftbreite wird geschätzt, nicht gemessen — ein
// SVG kennt seine Schrift erst beim Betrachter.
func titleSegments(segments []string, width int) []string {
	limit := float64(width - 40)
	var lines []string
	current := ""

	for _, seg := range segments {
		joined := seg

		if current != "" {
			joined = current + ", " + seg
		}

		if current == "" || textWidth(joined, svgTitleSize) <= limit {
			current = joined

			continue
		}

		lines = append(lines, current)
		current = seg
	}

	if current != "" {
		lines = append(lines, current)
	}

	return lines
}

// textWidth schätzt die Breite eines Textes in einer Grotesk: Großbuchstaben
// breit, Kleinbuchstaben mittel, Ziffern etwas schmaler, Satzzeichen und
// Leerraum schmal.
func textWidth(s string, fontSize float64) float64 {
	var em float64

	for _, r := range s {
		switch {
		case unicode.IsUpper(r):
			em += 0.72
		case unicode.IsLetter(r):
			em += 0.60
		case unicode.IsDigit(r):
			em += 0.56
		default:
			em += 0.28
		}
	}

	return em * fontSize
}

// hoshi liefert die Sternpunkte eines Bretts.
func hoshi(size int) []board.Point {
	var coords []int

	switch {
	case size >= 13:
		coords = []int{3, size / 2, size - 4}
	case size >= 9:
		coords = []int{2, size / 2, size - 3}
	default:
		return nil
	}

	var out []board.Point

	for _, y := range coords {
		for _, x := range coords {
			out = append(out, board.Point{X: x, Y: y})
		}
	}

	return out
}

// svg zeichnet das Diagramm.
func (d *diagram) svg() string {
	width := 2*svgMargin + (d.crop.cols()-1)*svgCell
	lines := titleSegments(d.title, width)
	top := svgTitleBase + svgTitleLine*len(lines)
	height := top + (d.crop.rows()-1)*svgCell + svgBottom

	colX := func(x int) int { return svgMargin + (x-d.crop.x0)*svgCell }
	rowY := func(y int) int { return top + (y-d.crop.y0)*svgCell }

	var sb strings.Builder

	fmt.Fprintf(&sb, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" `+
		`viewBox="0 0 %d %d" font-family="Helvetica, Arial, sans-serif">`,
		width, height, width, height)
	fmt.Fprintf(&sb, `<rect x="0" y="0" width="%d" height="%d" fill="#dcb35c"/>`,
		width, height)

	for i, line := range lines {
		fmt.Fprintf(&sb, `<text x="8" y="%d" font-size="%d" fill="#222">%s</text>`,
			16+svgTitleLine*i, svgTitleSize, html.EscapeString(line))
	}

	// Gitter: Linien laufen über den Ausschnitt hinaus, wo das Brett
	// weitergeht.
	yTop, yBot := rowY(d.crop.y0), rowY(d.crop.y1)
	xLeft, xRight := colX(d.crop.x0), colX(d.crop.x1)

	if d.crop.y0 > 0 {
		yTop -= svgLineBleed
	}

	if d.crop.y1 < d.size-1 {
		yBot += svgLineBleed
	}

	if d.crop.x0 > 0 {
		xLeft -= svgLineBleed
	}

	if d.crop.x1 < d.size-1 {
		xRight += svgLineBleed
	}

	for x := d.crop.x0; x <= d.crop.x1; x++ {
		fmt.Fprintf(&sb, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#000" stroke-width="1"/>`,
			colX(x), yTop, colX(x), yBot)
	}

	for y := d.crop.y0; y <= d.crop.y1; y++ {
		fmt.Fprintf(&sb, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#000" stroke-width="1"/>`,
			xLeft, rowY(y), xRight, rowY(y))
	}

	for _, h := range hoshi(d.size) {
		if d.crop.contains(h) {
			fmt.Fprintf(&sb, `<circle cx="%d" cy="%d" r="%.1f" fill="#000"/>`,
				colX(h.X), rowY(h.Y), svgHoshi)
		}
	}

	for x := d.crop.x0; x <= d.crop.x1; x++ {
		fmt.Fprintf(&sb, `<text x="%d" y="%d" font-size="%d" text-anchor="middle" fill="#333">%c</text>`,
			colX(x), height-3, svgLabelSize, board.ToGTP(board.Point{X: x}, d.size)[0])
	}

	for y := d.crop.y0; y <= d.crop.y1; y++ {
		fmt.Fprintf(&sb, `<text x="13" y="%d" font-size="%d" text-anchor="end" fill="#333">%d</text>`,
			rowY(y)+3, svgLabelSize, d.size-y)
	}

	for y := d.crop.y0; y <= d.crop.y1; y++ {
		for x := d.crop.x0; x <= d.crop.x1; x++ {
			p := board.Point{X: x, Y: y}
			colour := d.board.Get(p)

			if colour == board.Empty {
				continue
			}

			fill, ink := "#111", "#fff"

			if colour == board.White {
				fill, ink = "#fafafa", "#111"
			}

			fmt.Fprintf(&sb, `<circle cx="%d" cy="%d" r="%d" fill="%s" stroke="#000" stroke-width="0.8"/>`,
				colX(x), rowY(y), svgStone, fill)

			if number, ok := d.labels[p]; ok && number > 0 {
				fontSize := svgNumberSize

				if number >= 100 {
					fontSize = svgLabelSize
				}

				fmt.Fprintf(&sb, `<text x="%d" y="%d" font-size="%d" font-weight="bold" text-anchor="middle" fill="%s">%d</text>`,
					colX(x), rowY(y)+4, fontSize, ink, number)
			}
		}
	}

	for _, p := range d.crosses {
		if !d.crop.contains(p) {
			continue
		}

		cx, cy := colX(p.X), rowY(p.Y)
		fmt.Fprintf(&sb, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#d00" stroke-width="2"/>`,
			cx-svgCross, cy-svgCross, cx+svgCross, cy+svgCross)
		fmt.Fprintf(&sb, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#d00" stroke-width="2"/>`,
			cx-svgCross, cy+svgCross, cx+svgCross, cy-svgCross)
	}

	if d.ring != nil && d.crop.contains(*d.ring) {
		cx, cy := colX(d.ring.X), rowY(d.ring.Y)

		if d.ringText != "" && d.board.Get(*d.ring) == board.Empty {
			fmt.Fprintf(&sb, `<text x="%d" y="%d" font-size="%d" font-weight="bold" text-anchor="middle" fill="#d00">%s</text>`,
				cx, cy+3, svgLabelSize, html.EscapeString(d.ringText))
		}

		fmt.Fprintf(&sb, `<circle cx="%d" cy="%d" r="%d" fill="none" stroke="#d00" stroke-width="2"/>`,
			cx, cy, svgStone)
	}

	sb.WriteString("</svg>")

	return sb.String()
}

// moveLabels ordnet den Steinen einer Stellung die Nummern der Züge zu, die
// sie gesetzt haben — nur für Züge, die want bejaht. Ein späterer Zug auf
// demselben Punkt (nach einem Schlag) ersetzt die Nummer oder löscht sie,
// wenn er nicht dazugehört; geschlagene Steine tragen keine Nummer, weil
// sie nicht mehr da sind.
func moveLabels(g *board.Game, upto int, final *board.Board,
	want func(number int) bool) map[board.Point]int {

	labels := map[board.Point]int{}

	for i := 0; i < upto && i < len(g.Moves); i++ {
		m := g.Moves[i]

		if m.Pass {
			continue
		}

		if want(i + 1) {
			labels[m.Point] = i + 1
		} else {
			delete(labels, m.Point)
		}
	}

	for p := range labels {
		if final.Get(p) == board.Empty {
			delete(labels, p)
		}
	}

	return labels
}

// strandDiagram zeichnet die Stellung nach dem letzten Zug eines Strangs
// mit den Nummern seiner Züge.
func strandDiagram(g *board.Game, positions []*board.Board, s *Strand) string {
	upto := minInt(s.ToMove, len(positions)-1)
	final := positions[upto]
	member := map[int]bool{}

	var seeds []board.Point

	for _, number := range s.Moves {
		member[number] = true

		if number >= 1 && number <= len(g.Moves) && !g.Moves[number-1].Pass {
			seeds = append(seeds, g.Moves[number-1].Point)
		}
	}

	for _, sh := range s.Shapes {
		seeds = append(seeds, sh.Stones...)
	}

	d := diagram{
		size:   g.Size,
		crop:   cropAround(g.Size, seeds, 2),
		title:  []string{fmt.Sprintf("Erzählstrang %d", s.ID), s.Area, fmt.Sprintf("Züge %d bis %d", s.FromMove, s.ToMove)},
		board:  final,
		labels: moveLabels(g, upto, final, func(n int) bool { return member[n] }),
	}

	return d.svg()
}

// baustelleDiagram zeichnet die Stellung, in der die Baustelle beendet
// wurde, mit den Zügen des Fensters und dem Punkt rot markiert.
func baustelleDiagram(g *board.Game, positions []*board.Board, b *Baustelle) string {
	last := b.ToMove

	if b.Resolution != nil {
		last = b.Resolution.Number
	}

	upto := minInt(last, len(positions)-1)
	final := positions[upto]
	point, _, err := board.FromGTP(b.Point, g.Size)

	if err != nil {
		point = board.Point{}
	}

	d := diagram{
		size:  g.Size,
		crop:  cropAround(g.Size, []board.Point{point}, 4),
		title: []string{fmt.Sprintf("Baustelle %d", b.ID), fmt.Sprintf("%s für %s", b.Point, b.Player), fmt.Sprintf("Züge %d bis %d", b.FromMove, last)},
		board: final,
		labels: moveLabels(g, upto, final, func(n int) bool {
			return n >= b.FromMove && n <= last
		}),
		ring:     &point,
		ringText: b.Point,
	}

	return d.svg()
}

// endDiagram zeichnet die Endstellung mit gekreuzten toten Steinen.
func endDiagram(g *board.Game, positions []*board.Board, b *Bilanz) string {
	upto := minInt(b.FinalTurn, len(positions)-1)

	d := diagram{
		size:    g.Size,
		crop:    crop{0, g.Size - 1, 0, g.Size - 1},
		title:   []string{fmt.Sprintf("Endstellung nach Zug %d, tote Steine gekreuzt, Engine %s", b.FinalTurn, scoreString(b.ScoreLead))},
		board:   positions[upto],
		crosses: b.deadPoints(g.Size),
	}

	return d.svg()
}
