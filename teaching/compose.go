// composeTexts rendert die Lehrtexte einer ganzen Partie in einem Pass.
//
// Genau deshalb ist es ein Pass über alle Reports und kein Aufruf je Zug:
// Wiederholungs-Unterdrückung braucht Spielgedächtnis. Derselbe Befund
// ("die weiße Kette um Q10 ist schwach") ist beim ersten Mal Lehre und beim
// fünften Mal Rauschen; ein Merksatz, der jede zweite Karte beschließt,
// lehrt gar nichts mehr. refine() ruft nach dem Nachrechnen einzelner Züge
// wieder den GANZEN Pass auf — so bleibt die Unterdrückung deterministisch
// und driftet nicht.
package teaching

import (
	"fmt"
	"math"
	"strings"
)

// Fenster und Obergrenzen der Unterdrückung.
const (
	dedupWindow   = 5 // Züge, innerhalb derer derselbe Befund schweigt
	goldenGap     = 8 // Mindestabstand der Merkregel-Sätze
	closerMaxUse  = 2 // Verwendungen je Abschluss-Formulierung
	tenukiMaxUse  = 2 // "Tenuki ist erlaubt" je Partie
	maxChainProse = 2 // Ketten-Sätze je Zug
)

type dedupState struct {
	lastFinding map[string]int // Fingerprint → Zugnummer der letzten Nennung
	closerUse   map[string]int
	shapePraise map[string]bool
	// eFamilyUse zählt E-Befunde je Familie (Phase + offen/zu): E ist der
	// Normalfall einer Stellung und nach zwei Nennungen keine Nachricht mehr.
	eFamilyUse map[string]int
	lastGolden int
	tenukiUsed int
	passUsed   bool
}

func newDedupState() *dedupState {
	return &dedupState{
		lastFinding: map[string]int{},
		closerUse:   map[string]int{},
		shapePraise: map[string]bool{},
		eFamilyUse:  map[string]int{},
		lastGolden:  -goldenGap,
	}
}

// allow prüft ein Fingerprint gegen das Fenster und merkt sich die Nennung.
func (st *dedupState) allow(fingerprint string, number int) bool {
	if last, ok := st.lastFinding[fingerprint]; ok && number-last < dedupWindow {
		return false
	}

	st.lastFinding[fingerprint] = number

	return true
}

// composeTexts setzt rep.Text für alle Reports neu. Reports ohne
// ROSE-Detail (defensiv, sollte nicht vorkommen) behalten ihren Text.
func composeTexts(reports []MoveReport) {
	st := newDedupState()

	for i := range reports {
		if reports[i].rose == nil {
			continue
		}

		reports[i].Text = renderRoseText(&reports[i], st)
	}
}

// severity ordnet die Kategorie für Schwellenvergleiche.
func severity(category string) int {
	switch category {
	case "grober Fehler":
		return 4
	case "Fehler":
		return 3
	case "Ungenauigkeit":
		return 2
	case "gut":
		return 1
	default:
		return 0
	}
}

func renderRoseText(r *MoveReport, st *dedupState) string {
	d := r.rose
	var sentences []string

	top := topFinding(d.findings)
	posBucket := roseE

	if top != nil {
		posBucket = top.bucket
	}

	// --- Pass: eigener, kurzer Pfad -------------------------------------
	if r.Pass {
		return passText(r, st, top)
	}

	// --- 1) Befund der Stellung -----------------------------------------
	if top != nil {
		fingerprint := roseLetter(top.bucket) + ":" + top.rep

		if (top.atari && top.bucket == roseR) || st.allow(fingerprint, r.Number) {
			sentences = append(sentences, befundSentence(top, d.prevCoord))
		}
	} else if d.tenuki && st.tenukiUsed < tenukiMaxUse && severity(r.Category) <= 1 {
		st.tenukiUsed++
		sentences = append(sentences, tenukiSentence)
	} else if family := fmt.Sprintf("E|%s|%t", d.phase, d.openArea); st.eFamilyUse[family] < closerMaxUse &&
		st.allow("E:"+d.phase, r.Number) {
		st.eFamilyUse[family]++
		sentences = append(sentences, eSentence(d, r.Coord))
	}

	// --- 2) Urteil über den gespielten Zug (immer genau ein Satz) -------
	sentences = append(sentences, verdictSentence(r, st, posBucket))

	// --- 3) Form des Zuges ----------------------------------------------
	if d.shapeBad != nil {
		fingerprint := "shape:" + d.shapeBad.Name

		if st.allow(fingerprint, r.Number) {
			sentences = append(sentences, fmt.Sprintf(
				"S wie Shape: Der Zug bildet ein %s (%s) — %s",
				d.shapeBad.Name, strings.Join(d.shapeBad.Points, " "),
				lowerFirst(d.shapeBad.Teaching)))
		}
	} else if d.shapeGood != nil && severity(r.Category) <= 1 &&
		!st.shapePraise[d.shapeGood.Name] {
		st.shapePraise[d.shapeGood.Name] = true
		sentences = append(sentences, fmt.Sprintf(
			"Form: %s (%s) — %s", d.shapeGood.Name,
			strings.Join(d.shapeGood.Points, " "),
			lowerFirst(d.shapeGood.Teaching)))
	}

	// --- 4) Ketten-Sätze nur bei Ereignis -------------------------------
	sentences = append(sentences, chainProse(r)...)

	// --- 5) Abschluss: nur bei lehrreichen Zügen, Pools statt Dauerschleife
	if severity(r.Category) >= 2 {
		if closer := st.pickCloser(posBucket); closer != "" {
			sentences = append(sentences, closer)
		}
	}

	return strings.Join(sentences, "\n")
}

// verdictSentence ist der eine unbedingte Satz je Zug: Wo steht der Zug in
// der Checkliste, verglichen mit der Erstwahl?
func verdictSentence(r *MoveReport, st *dedupState, posBucket int) string {
	d := r.rose
	played := bucketIndex(r.Rose.Played)
	best := d.bucketBest

	bestName := r.BestMove
	pv := variantSuffix(r.BestPV)

	switch {
	case d.matchesBest:
		return fmt.Sprintf(
			"%s ist die Engine-Erstwahl und bedient %s.", r.Coord,
			bucketClaim(played, findingOf(d.findings, played), d.prevCoord))

	case best >= 0 && played > best:
		// Der Zug ist weniger dringlich als nötig — der Kern der Regel.
		claim1 := bucketClaim(best, findingOf(d.findings, best), d.prevCoord)
		claim2 := bucketClaim(played, nil, "")

		if severity(r.Category) >= 2 &&
			(r.Number-st.lastGolden >= goldenGap || severity(r.Category) >= 3) {
			st.lastGolden = r.Number

			return fmt.Sprintf(
				"Merkregel: Dringlichkeit geht vor Größe — hier stand %s vor "+
					"%s; die Erstwahl %s%s zeigt es.",
				claim1, claim2, bestName, pv)
		}

		return fmt.Sprintf("Die Erstwahl %s bedient %s, der Zug spielt %s.",
			bestName, claim1, claim2)

	case best >= 0 && played < best:
		return fmt.Sprintf(
			"Der Zug antwortet dringlicher als nötig (%s) — die Erstwahl "+
				"%s%s spielt %s.",
			roseLetter(played), bestName, pv, bucketClaim(best, nil, ""))

	case best >= 0 && severity(r.Category) >= 2:
		return fmt.Sprintf(
			"Richtige Stufe (%s), aber die Erstwahl %s%s setzt sie wirksamer um.",
			roseLetter(played), bestName, pv)

	case posBucket == roseR && d.answered:
		return fmt.Sprintf("%s beantwortet die Not — R ist bedient.", r.Coord)

	case posBucket == roseS && d.helpedS:
		return fmt.Sprintf(
			"%s festigt die eigene Gruppe — S ist bedient.", r.Coord)

	case best >= 0:
		// Gleiche Stufe wie eine abweichende Erstwahl, Zug in Ordnung.
		return fmt.Sprintf("%s liegt wie die Erstwahl %s auf Stufe %s.",
			r.Coord, bestName, roseLetter(played))

	default:
		return fmt.Sprintf(
			"%s liegt auf Stufe %s der Checkliste.", r.Coord, roseLetter(played))
	}
}

// passText behandelt Pass-Züge: unter Not ein Alarm, sonst eine knappe
// Einordnung — und ab dem zweiten ruhigen Pass nur noch die Stufe.
func passText(r *MoveReport, st *dedupState, top *roseFinding) string {
	if top != nil && top.bucket == roseR {
		return fmt.Sprintf(
			"Passen, während die %s Kette um %s in Not steht, gibt sie auf — "+
				"R geht vor allem anderen.", adjColor(top.color), top.rep)
	}

	if !st.passUsed {
		st.passUsed = true

		return "Pass — vertretbar, sobald kein Zug mehr Punkte bringt; " +
			"offene Randpunkte und Ko-Drohungen vorher prüfen."
	}

	return "ROSE-Stufe E."
}

// pickCloser liefert die nächste noch nicht verbrauchte Formulierung der
// Stufe; sind beide erschöpft, bleibt es still.
func (st *dedupState) pickCloser(bucket int) string {
	for _, closer := range closers[bucket] {
		if st.closerUse[closer] < closerMaxUse {
			st.closerUse[closer]++

			return closer
		}
	}

	return ""
}

// chainProse übersetzt die Effects in höchstens zwei Sätze — und nur, wenn
// wirklich etwas passiert ist: Schlagen, Atari, Benson-Übergang, großer
// Stärkesprung oder akute Freiheitsnot. Die JSON-Effects bleiben immer
// vollständig; gefiltert wird nur die Prosa.
func chainProse(r *MoveReport) []string {
	var out []string

	for i := range r.Effects {
		if len(out) >= maxChainProse {
			break
		}

		e := &r.Effects[i]

		switch {
		case e.Captured:
			out = append(out, fmt.Sprintf(
				"Geschlagen: %s Kette um %s (%d Steine).",
				adjColor(e.Color), e.Rep, e.Stones))

		case e.InAtari:
			out = append(out, fmt.Sprintf(
				"Die %s Kette um %s steht im Atari.", adjColor(e.Color), e.Rep))

		case e.UncondAlive && !e.UncondAliveBefore:
			out = append(out, fmt.Sprintf(
				"Die %s Kette um %s ist jetzt unbedingt lebendig (Benson).",
				adjColor(e.Color), e.Rep))

		case math.Abs(e.StrengthAfter-e.StrengthBefore) >= 0.25:
			out = append(out, fmt.Sprintf(
				"%s Kette um %s: Stärke %.2f → %.2f.",
				upperFirst(adjColor(e.Color)), e.Rep,
				noNegZero(e.StrengthBefore), noNegZero(e.StrengthAfter)))

		case e.Liberties <= 2 && !e.UncondAlive && e.Stones >= 2:
			out = append(out, fmt.Sprintf(
				"Die %s Kette um %s hängt an %d Freiheiten.",
				adjColor(e.Color), e.Rep, e.Liberties))
		}
	}

	return out
}

// noNegZero druckt Werte, die auf zwei Stellen null ergeben, ohne
// Vorzeichen: "Stärke -0.00" ist keine Aussage über die Stellung, sondern
// eine Eigenheit der Fließkommadarstellung.
func noNegZero(v float64) float64 {
	if math.Abs(v) < 0.005 {
		return 0
	}

	return v
}

func upperFirst(s string) string {
	r := []rune(s)

	if len(r) == 0 {
		return s
	}

	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// bucketIndex ist die Umkehrung von roseLetter.
func bucketIndex(letter string) int {
	switch letter {
	case "R":
		return roseR
	case "O":
		return roseO
	case "S":
		return roseS
	default:
		return roseE
	}
}
