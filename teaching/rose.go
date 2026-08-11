// ROSE-Klassifikation: Jeder Zug wird gegen die vier Fragen der
// ROSE-Checkliste gehalten — R Respond (muss lokal geantwortet werden?),
// O Offense (gibt es eine schwache gegnerische Gruppe?), S Status/Shape
// (sind die eigenen Gruppen sicher?), E Expansion/Endgame (wo ist das
// größte offene Gebiet?). Die goldene Regel dazu: Dringlichkeit geht vor
// Größe, also R vor O vor S vor E.
//
// Alle Befunde entstehen auf der Stellung VOR dem Zug — die Checkliste ist
// eine Eigenschaft der Stellung, nicht des Zuges. Der gespielte Zug und die
// Engine-Erstwahl werden anschließend gegen dieselben Befunde eingestuft;
// erst dieser Vergleich trägt die Didaktik ("E gespielt, wo R dran war").
//
// Halluzinationsschutz wie im ganzen Paket: ausschließlich verifizierte
// Zahlen (Freiheiten, Benson, situative Stärke, exakt gelesene Taktiken),
// keine Spekulation.
package teaching

import (
	"math"
	"strings"

	"github.com/vmanke/goteach-prod/board"
	"github.com/vmanke/goteach-prod/groups"
	"github.com/vmanke/goteach-prod/shapes"
	"github.com/vmanke/goteach-prod/strength"
)

// Die vier Stufen, numerisch geordnet: kleiner = dringlicher.
const (
	roseR = iota
	roseO
	roseS
	roseE
)

// roseLetter ist der Anzeigebuchstabe einer Stufe.
func roseLetter(bucket int) string {
	return [...]string{"R", "O", "S", "E"}[bucket]
}

// RoseFacts ist der nach außen sichtbare Kern der Einstufung (JSON und —
// über den Text — die Lines-Clients).
type RoseFacts struct {
	// Played ist die Stufe des gespielten Zuges: "R", "O", "S" oder "E".
	Played string `json:"played"`
	// Best ist die Stufe der Engine-Erstwahl; leer, wenn die Erstwahl
	// fehlt oder nicht parsebar ist (dann gibt es keinen Vergleich).
	Best string `json:"best,omitempty"`
	// Urgent meldet, ob die Stellung überhaupt einen R-Befund trug.
	Urgent bool `json:"urgent,omitempty"`
}

// roseFinding ist ein einzelner Befund der Checkliste: eine Kette, um die
// es geht, mit den Zahlen, die den Befund tragen.
type roseFinding struct {
	bucket   int
	rep      string // GTP-Koordinate der Ketten-Referenz
	color    string // "Schwarz"/"Weiß" der betroffenen Kette
	stones   int
	libs     int
	strength float64
	atari    bool
	// tactic trägt das exakt gelesene Motiv (Leiter/Netz), wenn der Befund
	// daraus stammt — dessen Lehrsatz ist der beste verfügbare Text.
	tactic *shapes.Instance
	// dist ist die BFS-Distanzkarte zur Kette, für die Frage "bedient ein
	// Punkt diesen Befund?".
	dist []int
}

// roseDetail ist der paketinterne Träger aller Fakten, aus denen compose.go
// den Text baut. Bewusst unexportiert: die Öffentlichkeit sieht RoseFacts
// und den fertigen Text, nicht das Zwischenmaterial.
type roseDetail struct {
	findings    []roseFinding // dringlichste zuerst
	bucketBest  int           // -1 = unbekannt
	prevCoord   string        // letzter gegnerischer Zug, "" ohne Vorzug
	tenuki      bool          // gespielter Zug weit weg vom Vorzug
	answered    bool          // R-Befund vorhanden und vom Zug bedient
	helpedS     bool          // S-Befund vorhanden und vom Zug gestärkt
	openArea    bool          // Umfeld des Zuges noch offen (E-Check)
	phase       string        // "Eröffnung" | "Mittelspiel" | "Endspiel"
	shapeBad    *shapes.Instance
	shapeGood   *shapes.Instance
	matchesBest bool
}

// Schwellen der Klassifikation. Werte sind bewusst konservativ: lieber ein
// Befund weniger als ein behaupteter, den die Stellung nicht hergibt.
const (
	roseWeakStrength = 0.25 // Kettenstärke, unter der eine Gruppe "schwach" ist
	roseWeakLibs     = 4    // Freiheiten, bis zu denen Schwäche zählt
	roseLostStrength = -0.5 // darunter gilt eine Kette als ohnehin verloren
	roseServeR       = 2    // Distanz, bis zu der ein Zug einen R-Befund bedient
	roseServeO       = 3    // dito für O
	roseServeS       = 2    // dito für S
	roseTenukiDist   = 4    // Chebyshev-Distanz, ab der ein Zug Tenuki ist
	roseOpenOwn      = 0.35 // mittlere |Ownership| im 5×5, bis zu der "offen" gilt
)

// assessRose baut den kompletten Befund für einen Zug. bb/ab sind die
// Stellungen vor/nach dem Zug, prev der vorangehende Zug (nil bei Zug 1),
// total die Gesamtzahl der Züge der Partie (für die Phase).
func assessRose(mv board.Move, prev *board.Move, size int, bb, ab *board.Board,
	ownBefore, ownAfter []float64, tau float64, bestMove string,
	matchesBest bool, total, number int) (*RoseFacts, *roseDetail) {

	d := &roseDetail{bucketBest: -1, matchesBest: matchesBest}

	switch {
	case 3*number < total:
		d.phase = "Eröffnung"
	case 4*number >= 3*total:
		d.phase = "Endspiel"
	default:
		d.phase = "Mittelspiel"
	}

	tactics := shapes.FindTactics(bb)

	// --- R: Not an eigenen Ketten neben dem letzten gegnerischen Zug ----
	if prev != nil && !prev.Pass && prev.Color == mv.Color.Opponent() {
		d.prevCoord = board.ToGTP(prev.Point, size)
		d.findings = append(d.findings,
			rFindings(mv.Color, prev.Point, size, bb, ownBefore, tau, tactics)...)
	}

	// --- O: schwache gegnerische Ketten --------------------------------
	d.findings = append(d.findings, weakChainFindings(roseO,
		mv.Color.Opponent(), size, bb, ownBefore, tau, tactics,
		claimedReps(d.findings))...)

	// --- S: chronisch schwache eigene Ketten (ohne die R-Ketten) --------
	d.findings = append(d.findings, weakChainFindings(roseS,
		mv.Color, size, bb, ownBefore, tau, nil,
		claimedReps(d.findings))...)

	// --- Einstufung des gespielten Zuges und der Erstwahl ---------------
	facts := &RoseFacts{Urgent: hasBucket(d.findings, roseR)}

	var played int

	if mv.Pass {
		played = roseE
	} else {
		played = classifyPoint(d.findings, size, mv.Point)
	}

	facts.Played = roseLetter(played)

	if bestMove != "" {
		if bp, pass, err := board.FromGTP(bestMove, size); err == nil && !pass {
			d.bucketBest = classifyPoint(d.findings, size, bp)
			facts.Best = roseLetter(d.bucketBest)
		}
	}

	if !mv.Pass {
		d.tenuki = prev != nil && !prev.Pass &&
			chebyshev(mv.Point, prev.Point) >= roseTenukiDist
		d.answered = servesFinding(d.findings, roseR, size, mv.Point) &&
			answeredUrgency(d.findings, mv, bb, ab)
		d.helpedS = servesFinding(d.findings, roseS, size, mv.Point) &&
			helpedOwnChain(mv, size, bb, ab, ownBefore, ownAfter, tau)
		d.openArea = openNeighborhood(size, ownBefore, mv.Point)
		d.shapeBad, d.shapeGood = newShapes(mv, bb, ab)
	}

	return facts, d
}

// rFindings sammelt die akuten Notlagen eigener Ketten rund um den letzten
// gegnerischen Zug.
func rFindings(mover board.Color, prev board.Point, size int, bb *board.Board,
	own []float64, tau float64, tactics []shapes.Instance) []roseFinding {

	var out []roseFinding

	alive := groups.UnconditionallyAlive(bb, mover)
	seen := map[string]bool{}

	for _, q := range append(bb.Neighbors(prev), prev) {
		if bb.Get(q) != mover {
			continue
		}

		ch := groups.ChainAt(bb, q)

		if ch == nil || len(ch.Stones) == 0 {
			continue
		}

		rep := board.ToGTP(ch.Rep(size), size)

		if seen[rep] || alive[ch.Stones[0]] {
			continue
		}

		seen[rep] = true

		st := strength.Group(size, own, ch.Stones, mover, tau)
		f := roseFinding{
			bucket:   roseR,
			rep:      rep,
			color:    playerName(mover),
			stones:   len(ch.Stones),
			libs:     len(ch.Liberties),
			strength: st,
			dist:     strength.Distances(size, ch.Stones),
		}

		switch {
		case len(ch.Liberties) == 1 && st > roseLostStrength:
			f.atari = true
			out = append(out, f)

		case len(ch.Liberties) == 2 && len(ch.Stones) >= 2 && st < 0.30:
			if t := tacticOn(tactics, playerName(mover), rep); t != nil {
				f.tactic = t
			}

			out = append(out, f)

		default:
			if t := tacticOn(tactics, playerName(mover), rep); t != nil {
				f.tactic = t
				out = append(out, f)
			}
		}
	}

	return out
}

// weakChainFindings sammelt schwache Ketten einer Farbe als O- bzw.
// S-Befunde. except schließt Ketten aus, die schon ein dringenderer Befund
// beansprucht (S soll nicht wiederholen, was R schon sagt).
func weakChainFindings(bucket int, color board.Color, size int, bb *board.Board,
	own []float64, tau float64, tactics []shapes.Instance,
	except map[string]bool) []roseFinding {

	var out []roseFinding

	alive := groups.UnconditionallyAlive(bb, color)
	colorName := playerName(color)

	for _, ch := range groups.FindChains(bb) {
		if ch.Color != color || len(ch.Stones) == 0 {
			continue
		}

		rep := board.ToGTP(ch.Rep(size), size)

		if except[rep] || alive[ch.Stones[0]] {
			continue
		}

		if len(ch.Stones) < 2 && len(ch.Liberties) > 2 {
			// Einzelsteine mit Luft sind Leichtgewichte, keine Gruppen in
			// Gefahr — sonst wäre jede frühe Eröffnung voller Befunde.
			continue
		}

		st := strength.Group(size, own, ch.Stones, color, tau)

		if st > roseWeakStrength || st <= roseLostStrength ||
			len(ch.Liberties) > roseWeakLibs {
			continue
		}

		f := roseFinding{
			bucket:   bucket,
			rep:      rep,
			color:    colorName,
			stones:   len(ch.Stones),
			libs:     len(ch.Liberties),
			strength: st,
			tactic:   tacticOn(tactics, colorName, rep),
			dist:     strength.Distances(size, ch.Stones),
		}

		out = append(out, f)
	}

	// Schwächste zuerst; höchstens zwei, mehr sagt kein Text sinnvoll auf.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].strength < out[j-1].strength; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}

	if len(out) > 2 {
		out = out[:2]
	}

	return out
}

// tacticOn liefert das gelesene Motiv (Leiter/Netz) zu einer Kette, falls
// eines existiert. Schnapp ist bewusst außen vor: Er warnt vor dem Schlagen
// und begründet keinen Angriff.
func tacticOn(tactics []shapes.Instance, color, rep string) *shapes.Instance {
	for i := range tactics {
		t := &tactics[i]

		if t.Name == "Schnapp" || t.Color != color {
			continue
		}

		for _, p := range t.Points {
			if p == rep {
				return t
			}
		}
	}

	return nil
}

// classifyPoint stuft einen Punkt gegen die Befunde ein: die dringlichste
// Frage, die er bedient, ist seine Stufe; bedient er keine, ist er E.
func classifyPoint(findings []roseFinding, size int, p board.Point) int {
	for _, bucket := range [...]int{roseR, roseO, roseS} {
		if servesBucketAt(findings, bucket, size, p) {
			return bucket
		}
	}

	return roseE
}

func servesBucketAt(findings []roseFinding, bucket, size int, p board.Point) bool {
	limit := [...]int{roseServeR, roseServeO, roseServeS, 0}[bucket]
	idx := p.Y*size + p.X

	for _, f := range findings {
		if f.bucket != bucket || idx < 0 || idx >= len(f.dist) {
			continue
		}

		if f.dist[idx] <= limit {
			return true
		}
	}

	return false
}

// servesFinding meldet, ob ein Punkt irgendeinen Befund der Stufe bedient.
func servesFinding(findings []roseFinding, bucket, size int, p board.Point) bool {
	return servesBucketAt(findings, bucket, size, p)
}

// answeredUrgency prüft, ob der Zug die R-Not tatsächlich lindert: die
// bedrohte Kette hat danach mehr Freiheiten, oder eine angrenzende
// gegnerische Kette ist verschwunden (geschlagen).
func answeredUrgency(findings []roseFinding, mv board.Move, bb, ab *board.Board) bool {
	for _, f := range findings {
		if f.bucket != roseR {
			continue
		}

		before := chainLibsAt(bb, f.rep, bb.Size)
		after := chainLibsAt(ab, f.rep, ab.Size)

		if after > before {
			return true
		}
	}

	// Freiheiten unverändert: auch ein Schlagzug direkt am Brennpunkt zählt
	// als Antwort — das Schlagen selbst steht in den Effects.
	for _, q := range bb.Neighbors(mv.Point) {
		if bb.Get(q) == mv.Color.Opponent() && ab.Get(q) == board.Empty {
			return true
		}
	}

	return false
}

// chainLibsAt zählt die Freiheiten der Kette an einer GTP-Koordinate;
// 0, wenn dort keine (mehr) steht.
func chainLibsAt(b *board.Board, gtp string, size int) int {
	p, pass, err := board.FromGTP(gtp, size)

	if err != nil || pass || b.Get(p) == board.Empty {
		return 0
	}

	if ch := groups.ChainAt(b, p); ch != nil {
		return len(ch.Liberties)
	}

	return 0
}

// helpedOwnChain prüft, ob der Zug die eigene Umgebung gestärkt hat:
// die Kette des gesetzten Steins hat mehr Freiheiten als die schwächste
// bediente S-Kette vorher, oder ihre situative Stärke ist gestiegen.
func helpedOwnChain(mv board.Move, size int, bb, ab *board.Board,
	ownBefore, ownAfter []float64, tau float64) bool {

	ch := groups.ChainAt(ab, mv.Point)

	if ch == nil {
		return false
	}

	after := strength.Group(size, ownAfter, ch.Stones, mv.Color, tau)

	var existed []board.Point

	for _, s := range ch.Stones {
		if bb.Get(s) == mv.Color {
			existed = append(existed, s)
		}
	}

	if len(existed) == 0 {
		return len(ch.Liberties) >= 3
	}

	before := strength.Group(size, ownBefore, existed, mv.Color, tau)

	return after > before || len(ch.Liberties) > chainLibsAtPoint(bb, existed[0])
}

func chainLibsAtPoint(b *board.Board, p board.Point) int {
	if ch := groups.ChainAt(b, p); ch != nil {
		return len(ch.Liberties)
	}

	return 0
}

// openNeighborhood misst, ob das 5×5-Umfeld eines Punktes noch offen ist:
// mittlere |Ownership| unterhalb der Schwelle.
func openNeighborhood(size int, own []float64, p board.Point) bool {
	if len(own) != size*size {
		return false
	}

	var sum float64
	var count int

	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			x, y := p.X+dx, p.Y+dy

			if x < 0 || y < 0 || x >= size || y >= size {
				continue
			}

			sum += math.Abs(own[y*size+x])
			count++
		}
	}

	return count > 0 && sum/float64(count) <= roseOpenOwn
}

// newShapes liefert die vom Zug NEU gebildete schlechte bzw. gute Form des
// Ziehenden (Vergleich der Formmengen vor/nach dem Zug; Identität über
// Name + Punkte).
func newShapes(mv board.Move, bb, ab *board.Board) (bad, good *shapes.Instance) {
	color := playerName(mv.Color)
	before := map[string]bool{}

	for _, s := range shapes.Find(bb) {
		before[s.Name+"|"+strings.Join(s.Points, ",")] = true
	}

	coord := board.ToGTP(mv.Point, ab.Size)

	for _, s := range shapes.Find(ab) {
		if s.Color != color || before[s.Name+"|"+strings.Join(s.Points, ",")] {
			continue
		}

		involved := false

		for _, p := range s.Points {
			if p == coord {
				involved = true

				break
			}
		}

		if !involved {
			continue
		}

		instance := s

		if s.Name == "leeres Dreieck" {
			if bad == nil {
				bad = &instance
			}
		} else if good == nil {
			good = &instance
		}
	}

	return bad, good
}

func hasBucket(findings []roseFinding, bucket int) bool {
	for _, f := range findings {
		if f.bucket == bucket {
			return true
		}
	}

	return false
}

// topFinding liefert den dringlichsten Befund (Stufenreihenfolge, innerhalb
// einer Stufe die Reihenfolge der Sammlung: R zuerst, dann schwächste).
func topFinding(findings []roseFinding) *roseFinding {
	best := -1

	for i := range findings {
		if best < 0 || findings[i].bucket < findings[best].bucket {
			best = i
		}
	}

	if best < 0 {
		return nil
	}

	return &findings[best]
}

func claimedReps(findings []roseFinding) map[string]bool {
	out := map[string]bool{}

	for _, f := range findings {
		out[f.rep] = true
	}

	return out
}

func chebyshev(a, b board.Point) int {
	dx, dy := a.X-b.X, a.Y-b.Y

	if dx < 0 {
		dx = -dx
	}

	if dy < 0 {
		dy = -dy
	}

	if dx > dy {
		return dx
	}

	return dy
}
