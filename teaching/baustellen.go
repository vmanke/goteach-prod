package teaching

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vmanke/goteach-prod/board"
)

// baustelleReach ist der Umkreis (Chebyshev-Distanz), in dem eine Erstwahl
// noch zur selben Baustelle zählt. Die Engine wechselt in einem Eckkampf
// gern zwischen zwei Nachbarpunkten — das ist dieselbe Baustelle, nicht
// zwei.
const baustelleReach = 1

// baustelleGap ist die Zahl eigener Züge ohne Treffer, die eine Baustelle
// überbrückt. Wer dreimal hintereinander etwas anderes spielen musste, hat
// die Stelle nicht vergessen; beim vierten Mal war sie nicht mehr dran.
const baustelleGap = 3

// minBaustelleHits ist die Mindestzahl eigener Züge, in denen die Engine
// den Punkt nannte. Darunter ist es eine Gelegenheit, keine Baustelle.
const minBaustelleHits = 6

// counterpartReach ist der Umkreis, in dem die Baustelle der Gegenseite als
// dieselbe Stelle gilt.
const counterpartReach = 2

// Baustelle ist ein Punkt, den die Engine über viele eigene Züge hinweg als
// Erstwahl nannte, ohne dass der Spieler dort spielte.
//
// Die Lehreinheit je Zug sagt bei jedem dieser Züge dasselbe („G2 wäre
// besser gewesen") und verliert damit ihre Wirkung. Die Baustelle sagt es
// einmal, mit Dauer und Preis.
type Baustelle struct {
	ID       int    `json:"id"`
	Area     string `json:"area"`
	Player   string `json:"player"`
	Point    string `json:"point"`
	FromMove int    `json:"fromMove"`
	ToMove   int    `json:"toMove"`

	// Hits zählt die eigenen Züge im Fenster, in denen die Erstwahl auf
	// dem Punkt oder einem Nachbarpunkt lag; NearHits davon die
	// Nachbarpunkte.
	Hits     int `json:"hits"`
	NearHits int `json:"nearHits,omitempty"`
	OwnMoves int `json:"ownMoves"`

	// PointsLost ist die Summe der Zugverluste über die Treffer — aus den
	// Zug-Reports nachrechenbar.
	PointsLost float64 `json:"pointsLost"`

	// Resolution ist der erste Zug nach dem Fenster auf dem Punkt oder
	// daneben; ResolvedBy sagt, wer ihn spielte ("selbst" oder "Gegner").
	Resolution *MoveRef `json:"resolution,omitempty"`
	ResolvedBy string   `json:"resolvedBy,omitempty"`

	// Counterpart ist die Baustelle der Gegenseite an derselben Stelle zur
	// selben Zeit, 0 ohne Gegenstück.
	Counterpart int `json:"counterpart,omitempty"`

	Text string `json:"text"`
}

// baustelleMove ist ein Zug-Report mit aufgelösten Koordinaten.
type baustelleMove struct {
	index   int
	report  *MoveReport
	played  board.Point
	best    board.Point
	hasBest bool
	pass    bool
}

// findBaustellen sucht je Spieler die Punkte, die die Engine über viele
// Züge als Erstwahl nannte.
func findBaustellen(size int, reports []MoveReport) []Baustelle {
	all := resolveMoves(size, reports)

	var out []Baustelle

	for _, player := range []string{"Schwarz", "Weiß"} {
		var own []*baustelleMove

		for i := range all {
			if all[i].report.Player == player {
				own = append(own, &all[i])
			}
		}

		out = append(out, baustellenFor(size, player, own, all)...)
	}

	for i := range out {
		out[i].ID = i + 1
	}

	linkCounterparts(size, out)

	for i := range out {
		out[i].Text = baustelleText(&out[i])
	}

	return out
}

func resolveMoves(size int, reports []MoveReport) []baustelleMove {
	out := make([]baustelleMove, len(reports))

	for i := range reports {
		r := &reports[i]
		m := baustelleMove{index: i, report: r, pass: r.Pass}

		if !r.Pass {
			if p, pass, err := board.FromGTP(r.Coord, size); err == nil && !pass {
				m.played = p
			} else {
				m.pass = true
			}
		}

		if r.BestMove != "" {
			if p, pass, err := board.FromGTP(r.BestMove, size); err == nil && !pass {
				m.best = p
				m.hasBest = true
			}
		}

		out[i] = m
	}

	return out
}

func near(a, b board.Point, reach int) bool {
	dx, dy := a.X-b.X, a.Y-b.Y

	if dx < 0 {
		dx = -dx
	}

	if dy < 0 {
		dy = -dy
	}

	return dx <= reach && dy <= reach
}

// baustellenFor läuft über die eigenen Züge eines Spielers und öffnet an
// jeder unbeachteten Erstwahl ein Fenster.
func baustellenFor(size int, player string, own []*baustelleMove,
	all []baustelleMove) []Baustelle {

	var out []Baustelle

	for i := 0; i < len(own); {
		start := own[i]

		// Ein Fenster beginnt nur dort, wo die Engine etwas anderes
		// wollte als das Gespielte.
		if !start.hasBest || start.pass || start.played == start.best {
			i++

			continue
		}

		anchor := start.best

		// Der erste Zug irgendeines Spielers auf dem Anker oder daneben
		// beendet das Fenster: Danach ist die Stelle keine Baustelle mehr.
		limit := -1

		for k := start.index + 1; k < len(all); k++ {
			if !all[k].pass && near(all[k].played, anchor, baustelleReach) {
				limit = k

				break
			}
		}

		hits := []int{i}
		last := i
		gap := 0

		for j := i + 1; j < len(own); j++ {
			m := own[j]

			if limit >= 0 && m.index >= limit {
				break
			}

			if m.hasBest && near(m.best, anchor, baustelleReach) {
				hits = append(hits, j)
				last = j
				gap = 0

				continue
			}

			gap++

			if gap > baustelleGap {
				break
			}
		}

		if len(hits) < minBaustelleHits {
			i++

			continue
		}

		point := modePoint(own, hits, anchor)
		b := buildBaustelle(size, player, point, own, i, last, all)
		out = append(out, b)
		i = last + 1
	}

	return out
}

// modePoint wählt den Punkt, den die Engine im Fenster am häufigsten
// nannte; bei Gleichstand den Anker, sonst die kleinere Koordinate.
func modePoint(own []*baustelleMove, hits []int, anchor board.Point) board.Point {
	count := map[board.Point]int{}

	for _, j := range hits {
		count[own[j].best]++
	}

	best := anchor

	for p, c := range count {
		switch {
		case c > count[best]:
			best = p
		case c == count[best] && p != anchor && best != anchor && less(p, best):
			best = p
		}
	}

	return best
}

func less(a, b board.Point) bool {
	if a.Y != b.Y {
		return a.Y < b.Y
	}

	return a.X < b.X
}

func buildBaustelle(size int, player string, point board.Point,
	own []*baustelleMove, first, last int, all []baustelleMove) Baustelle {

	b := Baustelle{
		Area:     areaName(size, []board.Point{point}),
		Player:   player,
		Point:    board.ToGTP(point, size),
		FromMove: own[first].report.Number,
		ToMove:   own[last].report.Number,
	}

	for j := first; j <= last; j++ {
		m := own[j]
		b.OwnMoves++

		if !m.hasBest || !near(m.best, point, baustelleReach) {
			continue
		}

		b.Hits++
		b.PointsLost += m.report.PointsLost

		if m.best != point {
			b.NearHits++
		}
	}

	for k := own[last].index + 1; k < len(all); k++ {
		m := &all[k]

		if m.pass || !near(m.played, point, baustelleReach) {
			continue
		}

		b.Resolution = &MoveRef{
			Number:     m.report.Number,
			Player:     m.report.Player,
			Coord:      m.report.Coord,
			PointsLost: m.report.PointsLost,
			Category:   m.report.Category,
		}

		if m.report.Player == player {
			b.ResolvedBy = "selbst"
		} else {
			b.ResolvedBy = "Gegner"
		}

		break
	}

	return b
}

// linkCounterparts verbindet Baustellen beider Seiten, die zur selben Zeit
// an derselben Stelle liegen.
func linkCounterparts(size int, list []Baustelle) {
	points := make([]board.Point, len(list))

	for i := range list {
		p, _, err := board.FromGTP(list[i].Point, size)

		if err == nil {
			points[i] = p
		}
	}

	for i := range list {
		if list[i].Counterpart != 0 {
			continue
		}

		for j := i + 1; j < len(list); j++ {
			if list[j].Counterpart != 0 || list[j].Player == list[i].Player {
				continue
			}

			overlap := list[i].FromMove <= list[j].ToMove &&
				list[j].FromMove <= list[i].ToMove

			if overlap && near(points[i], points[j], counterpartReach) {
				list[i].Counterpart = list[j].ID
				list[j].Counterpart = list[i].ID

				break
			}
		}
	}
}

// baustelleText baut den verifizierten Text einer Baustelle — nur aus den
// gezählten Zügen, ohne Deutung.
func baustelleText(b *Baustelle) string {
	var sb strings.Builder

	fmt.Fprintf(&sb,
		"Von Zug %d bis %d nannte die Engine für %s den Punkt %s (%s) in "+
			"%d von %d eigenen Zügen als Erstwahl.",
		b.FromMove, b.ToMove, b.Player, b.Point, b.Area, b.Hits, b.OwnMoves)

	if b.NearHits > 0 {
		fmt.Fprintf(&sb, " Davon lag die Erstwahl %d-mal auf einem Nachbarpunkt.",
			b.NearHits)
	}

	fmt.Fprintf(&sb, " %s spielte in dieser Zeit nie dort.", b.Player)

	if b.PointsLost < 0 {
		fmt.Fprintf(&sb,
			" Die Züge, in denen der Punkt liegen blieb, brachten unter dem "+
				"Strich %.1f Punkte ein (Summe der Zugverluste laut Engine).",
			-b.PointsLost)
	} else {
		fmt.Fprintf(&sb,
			" Die Züge, in denen der Punkt liegen blieb, kosteten zusammen "+
				"%.1f Punkte (Summe der Zugverluste laut Engine).",
			b.PointsLost)
	}

	switch {
	case b.Resolution == nil:
		sb.WriteString(" Bis zum Ende der analysierten Züge blieb der Punkt frei.")

	case b.ResolvedBy == "selbst":
		fmt.Fprintf(&sb, " Beendet hat es %s selbst mit Zug %d auf %s.",
			b.Player, b.Resolution.Number, b.Resolution.Coord)

	default:
		fmt.Fprintf(&sb, " Zugegriffen hat am Ende %s mit Zug %d auf %s.",
			b.Resolution.Player, b.Resolution.Number, b.Resolution.Coord)
	}

	if b.Counterpart != 0 {
		fmt.Fprintf(&sb,
			" Gegenstück: Baustelle %d der anderen Seite am selben Ort.",
			b.Counterpart)
	}

	return sb.String()
}

// sortedGTP sortiert Koordinaten als Zeichenketten — so, wie eine Liste
// gelesen wird, nicht nach Brettgeometrie.
func sortedGTP(points []board.Point, size int) []string {
	out := make([]string, 0, len(points))

	for _, p := range points {
		out = append(out, board.ToGTP(p, size))
	}

	sort.Strings(out)

	return out
}
