package teaching

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/vmanke/goteach-prod/board"
	"github.com/vmanke/goteach-prod/shapes"
	"github.com/vmanke/goteach-prod/strength"
)

// assignRadius ist die größte Gitterdistanz, in der ein Zug noch einem
// Strang zugeschlagen wird. Weiter entfernte Züge gehören zu keinem — ein
// Strang soll erzählen, was zusammengehört, nicht alles einsammeln.
const assignRadius = 4

// minStrandMoves ist die Mindestzahl zugeordneter Züge. Ein einzelner Zug
// ist keine Geschichte.
const minStrandMoves = 3

// minTraceSupport ist die Mindestzahl von Zügen, über die eine Form
// überhaupt Salienz zeigen muss, um in den Kopplungsgraphen zu kommen. Eine
// Form, die fünf Züge lang ausschlägt, hat keine Zeitreihe, sondern einen
// Punkt.
//
// Bewusst niedrig: Ownership ändert sich je Zug nur in der Umgebung des
// Zuges, eine Form am anderen Brettende schlägt also nur bei einem Bruchteil
// der Züge überhaupt aus. Die eigentliche Absicherung gegen bedeutungslose
// Korrelationen ist die Effektstärke-Schranke in coupling.go; dieser Filter
// fängt nur die ganz entarteten Spuren ab.
const minTraceSupport = 8

// MoveRef verweist auf einen einzelnen Zug innerhalb eines Strangs.
type MoveRef struct {
	Number     int     `json:"number"`
	Player     string  `json:"player"`
	Coord      string  `json:"coord"`
	PointsLost float64 `json:"pointsLost"`
	Category   string  `json:"category"`
}

// Strand ist ein Erzählstrang: gekoppelte Formen, die ihnen zugeordneten
// Züge und die dabei verifizierten Zahlen.
type Strand struct {
	ID         int                `json:"id"`
	Area       string             `json:"area"`
	FromMove   int                `json:"fromMove"`
	ToMove     int                `json:"toMove"`
	Moves      []int              `json:"moves"`
	Shapes     []shapes.Instance  `json:"shapes"`
	Couplings  []Coupling         `json:"couplings,omitempty"`
	PointsLost map[string]float64 `json:"pointsLost"`
	Captures   int                `json:"captures"`
	Worst      *MoveRef           `json:"worst,omitempty"`
	Text       string             `json:"text"`
	TextLLM    string             `json:"textLLM,omitempty"`
}

// instanceTrace hält eine Forminstanz mit ihrer Zeitspur.
type instanceTrace struct {
	label      string
	instance   shapes.Instance
	salience   []float64
	strengthOf []float64
	total      float64
}

// buildStrands zerlegt die analysierten Züge in Erzählstränge.
//
// lo ist der erste Turn, für den Ownership vorliegt (Stellung *vor* dem
// ersten analysierten Zug); ownership[k] gehört zu Turn lo+k.
func buildStrands(g *board.Game, positions []*board.Board, ownership [][]float64,
	lo int, reports []MoveReport, tau float64, external [][]board.Point) []Strand {

	if len(ownership) < 4 || len(reports) == 0 {
		return nil
	}

	salience := salienceSeries(ownership)
	traces := trackShapes(g.Size, positions, ownership, salience, lo, tau)

	// Ohne vorgegebene Fenster brauchen wir mindestens zwei Formspuren — die
	// Gegenden entstehen dann ja aus deren Kopplung. Gibt das gelernte Modul
	// die Gegenden vor, sind Formen willkommen, aber nicht Bedingung.
	if len(traces) < 2 && len(external) == 0 {
		return nil
	}

	plain := make([]trace, len(traces))
	labels := make([]string, len(traces))

	for i, tr := range traces {
		plain[i] = trace{label: tr.label, salience: tr.salience, strength: tr.strengthOf}
		labels[i] = tr.label
	}

	edges := couple(plain, MaxLag)

	byLabel := map[string]instanceTrace{}

	for _, tr := range traces {
		byLabel[tr.label] = tr
	}

	// Liefert das gelernte Modul Fenster, geben sie die Gegenden vor; sonst
	// entstehen sie aus den Zusammenhangskomponenten des Kopplungsgraphen.
	// Formen und Zahlen kommen in beiden Fällen aus demselben
	// deterministischen Code — das gelernte Modul wählt aus, behauptet aber
	// nichts.
	var prepared []candidate

	if len(external) > 0 {
		prepared = candidatesFromRegions(g, external, traces)
	} else {
		prepared = candidatesFromComponents(g, components(labels, edges), byLabel)
	}

	return assemble(g, prepared, edges, reports)
}

// candidate ist eine Gegend samt der Formen, die dort liegen.
type candidate struct {
	labels []string
	region []board.Point
	shapes []shapes.Instance
}

func candidatesFromComponents(g *board.Game, groups [][]string,
	byLabel map[string]instanceTrace) []candidate {

	var out []candidate

	for _, members := range groups {
		c := candidate{labels: members}
		seen := map[int]bool{}

		for _, label := range members {
			tr, ok := byLabel[label]

			if !ok {
				continue
			}

			c.shapes = append(c.shapes, tr.instance)

			for _, s := range tr.instance.Stones {
				if idx := s.Y*g.Size + s.X; !seen[idx] {
					seen[idx] = true
					c.region = append(c.region, s)
				}
			}
		}

		if len(c.region) > 0 {
			out = append(out, c)
		}
	}

	return out
}

// candidatesFromRegions ordnet jede Form dem Fenster zu, das die meisten
// ihrer Steine enthält.
func candidatesFromRegions(g *board.Game, regions [][]board.Point,
	traces []instanceTrace) []candidate {

	out := make([]candidate, len(regions))
	inside := make([]map[int]bool, len(regions))

	for i, region := range regions {
		out[i].region = region
		inside[i] = map[int]bool{}

		for _, p := range region {
			inside[i][p.Y*g.Size+p.X] = true
		}
	}

	for _, tr := range traces {
		best, bestCount := -1, 0

		for i := range regions {
			count := 0

			for _, s := range tr.instance.Stones {
				if inside[i][s.Y*g.Size+s.X] {
					count++
				}
			}

			if count > bestCount {
				best, bestCount = i, count
			}
		}

		if best >= 0 {
			out[best].shapes = append(out[best].shapes, tr.instance)
			out[best].labels = append(out[best].labels, tr.label)
		}
	}

	return out
}

// salienceSeries bildet den Salienztensor: wie stark sich die Zugehörigkeit
// jedes Punktes mit jedem Zug verändert.
func salienceSeries(ownership [][]float64) [][]float64 {
	out := make([][]float64, len(ownership))
	out[0] = make([]float64, len(ownership[0]))

	for t := 1; t < len(ownership); t++ {
		row := make([]float64, len(ownership[t]))

		for i := range row {
			d := ownership[t][i] - ownership[t-1][i]

			if d < 0 {
				d = -d
			}

			row[i] = d
		}

		out[t] = row
	}

	return out
}

// trackShapes verfolgt jede Forminstanz über die Zeit und gibt die
// salientesten zurück.
func trackShapes(size int, positions []*board.Board, ownership, salience [][]float64,
	lo int, tau float64) []instanceTrace {

	length := len(ownership)
	byKey := map[string]*instanceTrace{}

	fields := make([][]float64, length)

	for k := 0; k < length; k++ {
		fields[k] = strength.Field(size, ownership[k], tau)
	}

	for k := 0; k < length; k++ {
		turn := lo + k

		if turn < 0 || turn >= len(positions) {
			continue
		}

		found := append(shapes.Find(positions[turn]),
			shapes.FindTactics(positions[turn])...)

		for _, inst := range found {
			key := inst.Name + "|" + inst.Color + "|" + strings.Join(inst.Points, ",")
			tr := byKey[key]

			if tr == nil {
				tr = &instanceTrace{
					label:      inst.Name + " " + inst.Points[0],
					instance:   inst,
					salience:   make([]float64, length),
					strengthOf: make([]float64, length),
				}
				byKey[key] = tr
			}

			s := mean(salience[k], inst.Stones, size)
			tr.salience[k] = s
			tr.strengthOf[k] = mean(fields[k], inst.Stones, size)
			tr.total += s
		}
	}

	out := make([]instanceTrace, 0, len(byKey))

	for _, tr := range byKey {
		if support(tr.salience) < minTraceSupport {
			continue
		}

		out = append(out, *tr)
	}

	// Die salientesten Formen zuerst; nur sie gehen in den Kopplungsgraphen.
	sort.Slice(out, func(i, j int) bool {
		if out[i].total != out[j].total {
			return out[i].total > out[j].total
		}

		return out[i].label < out[j].label
	})

	if len(out) > MaxTraces {
		out = out[:MaxTraces]
	}

	// Eindeutige Beschriftungen: Zwei gleichnamige Formen am selben ersten
	// Punkt sind selten, aber möglich.
	seen := map[string]int{}

	for i := range out {
		seen[out[i].label]++

		if n := seen[out[i].label]; n > 1 {
			out[i].label = fmt.Sprintf("%s (%d)", out[i].label, n)
		}
	}

	return out
}

// support zählt die Zeitpunkte, an denen eine Spur überhaupt ausschlägt.
func support(series []float64) int {
	n := 0

	for _, v := range series {
		if v > 1e-9 {
			n++
		}
	}

	return n
}

func mean(field []float64, points []board.Point, size int) float64 {
	if len(points) == 0 {
		return 0
	}

	var sum float64

	for _, p := range points {
		sum += field[p.Y*size+p.X]
	}

	return sum / float64(len(points))
}

// assemble baut aus den Gegenden die Stränge und ordnet ihnen Züge zu.
func assemble(g *board.Game, candidates []candidate, edges []Coupling,
	reports []MoveReport) []Strand {

	if len(candidates) == 0 {
		return nil
	}

	// Jeder Zug geht an höchstens einen Strang — den räumlich nächsten.
	// Ohne diese Eindeutigkeit summierten sich Punktverluste doppelt und die
	// Bilanz eines Strangs wäre nicht mehr nachrechenbar.
	distances := make([][]int, len(candidates))

	for i, c := range candidates {
		distances[i] = strength.Distances(g.Size, c.region)
	}

	assigned := make([][]int, len(candidates))

	for _, rep := range reports {
		if rep.Pass {
			continue
		}

		point, _, err := board.FromGTP(rep.Coord, g.Size)

		if err != nil {
			continue
		}

		best, bestDist := -1, assignRadius+1

		for i := range candidates {
			if d := distances[i][point.Y*g.Size+point.X]; d >= 0 && d < bestDist {
				best, bestDist = i, d
			}
		}

		if best >= 0 {
			assigned[best] = append(assigned[best], rep.Number)
		}
	}

	byNumber := map[int]*MoveReport{}

	for i := range reports {
		byNumber[reports[i].Number] = &reports[i]
	}

	var out []Strand

	for i, c := range candidates {
		if len(assigned[i]) < minStrandMoves {
			continue
		}

		s := Strand{
			Area:       areaName(g.Size, c.region),
			Moves:      assigned[i],
			Shapes:     c.shapes,
			Couplings:  edgesWithin(edges, c.labels),
			PointsLost: map[string]float64{},
		}

		s.FromMove = s.Moves[0]
		s.ToMove = s.Moves[len(s.Moves)-1]

		for _, number := range s.Moves {
			rep := byNumber[number]

			if rep == nil {
				continue
			}

			s.PointsLost[rep.Player] += rep.PointsLost

			for _, effect := range rep.Effects {
				if effect.Captured {
					s.Captures += effect.Stones
				}
			}

			if s.Worst == nil || rep.PointsLost > s.Worst.PointsLost {
				s.Worst = &MoveRef{
					Number:     rep.Number,
					Player:     rep.Player,
					Coord:      rep.Coord,
					PointsLost: rep.PointsLost,
					Category:   rep.Category,
				}
			}
		}

		s.Text = strandText(&s)
		out = append(out, s)
	}

	// Nach Gewicht sortieren: der Strang mit dem größten Punktverlust zuerst.
	sort.Slice(out, func(i, j int) bool {
		li, lj := totalLost(out[i]), totalLost(out[j])

		if li != lj {
			return li > lj
		}

		return out[i].FromMove < out[j].FromMove
	})

	for i := range out {
		out[i].ID = i + 1
	}

	return out
}

func totalLost(s Strand) float64 {
	var sum float64

	for _, v := range s.PointsLost {
		sum += v
	}

	return sum
}

func edgesWithin(edges []Coupling, labels []string) []Coupling {
	member := map[string]bool{}

	for _, l := range labels {
		member[l] = true
	}

	var out []Coupling

	for _, e := range edges {
		if member[e.From] && member[e.To] {
			out = append(out, e)
		}
	}

	return out
}

// areaName benennt die Brettgegend eines Strangs so, wie ein Lehrer sie
// nennen würde.
func areaName(size int, region []board.Point) string {
	if len(region) == 0 {
		return "unbestimmt"
	}

	var sx, sy int
	minX, maxX := size, 0
	minY, maxY := size, 0

	for _, p := range region {
		sx += p.X
		sy += p.Y

		if p.X < minX {
			minX = p.X
		}

		if p.X > maxX {
			maxX = p.X
		}

		if p.Y < minY {
			minY = p.Y
		}

		if p.Y > maxY {
			maxY = p.Y
		}
	}

	// Erstreckt sich der Strang über mehr als das halbe Brett, ist eine
	// Gegend nicht mehr die richtige Beschreibung.
	if maxX-minX > size/2 || maxY-minY > size/2 {
		return "über das ganze Brett verteilt"
	}

	cx := sx / len(region)
	cy := sy / len(region)

	third := size / 3
	row := zone(cy, third, size)
	column := zone(cx, third, size)

	vertical := []string{"oben", "in der Mitte", "unten"}[row]
	horizontal := []string{"links", "", "rechts"}[column]

	// Getrennte Beugungsform, sonst käme "am rechtsen Rand" heraus.
	edge := []string{"linken", "", "rechten"}[column]

	if horizontal == "" {
		if row == 1 {
			return "im Zentrum"
		}

		return vertical + " in der Mitte"
	}

	if row == 1 {
		return "am " + edge + " Rand"
	}

	return vertical + " " + horizontal
}

func zone(v, third, size int) int {
	switch {
	case v < third:
		return 0
	case v >= size-third:
		return 2
	}

	return 1
}

// strandText baut den verifizierten Basistext eines Strangs.
//
// Ausschließlich aus nachgerechneten Zahlen und benannten Formen — dieselbe
// Halluzinationssperre wie beim Teaching pro Zug. Insbesondere behauptet der
// Text bei gekoppelten Formen *keine* Ursache: Die Kreuzkorrelation zeigt
// einen zeitlichen Zusammenhang, mehr gibt sie nicht her.
func strandText(s *Strand) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "%s, Züge %d bis %d (%d davon gehören hierher). ",
		capitalize(s.Area), s.FromMove, s.ToMove, len(s.Moves))

	if names := shapeNames(s.Shapes); names != "" {
		fmt.Fprintf(&sb, "Beteiligte Formen: %s. ", names)
	}

	players := make([]string, 0, len(s.PointsLost))

	for player := range s.PointsLost {
		players = append(players, player)
	}

	sort.Strings(players)

	for _, player := range players {
		// Ein negativer Punktverlust ist ein Gewinn; "verliert -4.7 Punkte"
		// wäre zwar rechnerisch richtig, aber als Satz unbrauchbar.
		if lost := s.PointsLost[player]; lost < 0 {
			fmt.Fprintf(&sb, "%s gewinnt hier %.1f Punkte. ", player, -lost)
		} else {
			fmt.Fprintf(&sb, "%s verliert hier %.1f Punkte. ", player, lost)
		}
	}

	if s.Captures > 0 {
		fmt.Fprintf(&sb, "%d Steine werden geschlagen. ", s.Captures)
	}

	if s.Worst != nil && s.Worst.PointsLost > 1.5 {
		fmt.Fprintf(&sb, "Der teuerste Zug ist %d (%s %s, %s, %.1f Punkte). ",
			s.Worst.Number, s.Worst.Player, s.Worst.Coord,
			s.Worst.Category, s.Worst.PointsLost)
	}

	if len(s.Couplings) > 0 {
		c := s.Couplings[0]
		fmt.Fprintf(&sb,
			"Zeitlich eng damit verbunden: %s und %s (Versatz %d Züge).",
			c.From, c.To, c.Lag)
	}

	return strings.TrimSpace(sb.String())
}

func shapeNames(instances []shapes.Instance) string {
	seen := map[string]bool{}
	var out []string

	for _, inst := range instances {
		label := inst.Name + " " + inst.Points[0]

		if !seen[label] {
			seen[label] = true
			out = append(out, label)
		}
	}

	sort.Strings(out)

	if len(out) > 6 {
		out = append(out[:6], "…")
	}

	return strings.Join(out, ", ")
}

// capitalize schreibt den ersten Buchstaben groß — runenweise.
//
// s[:1] wäre falsch: "über" beginnt mit einem Zwei-Byte-Zeichen, und das
// erste Byte allein ergibt kein gültiges UTF-8. Aus "über das ganze Brett"
// wurde so ein Ersatzzeichen.
func capitalize(s string) string {
	if s == "" {
		return s
	}

	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])

	return string(runes)
}
