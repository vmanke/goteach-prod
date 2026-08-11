// composeTexts rendert die Lehrtexte einer ganzen Partie in einem Pass.
//
// Zwei Eigenschaften bestimmen den Aufbau:
//
// Länge nach Gewicht. Ein guter Zug braucht keine Erklärung — die Zahlen
// auf der Karte sagen alles. Ein grober Fehler braucht die ganze Rechnung:
// was die Stellung verlangte, was der Zug kostet, was die Engine
// stattdessen gespielt hätte und wie es danach weitergegangen wäre. Früher
// bekamen beide gleich viel Text, und dadurch war er beim guten Zug zu viel
// und beim Fehler zu wenig.
//
// Rückblickende Unterdrückung. Derselbe Befund ist beim ersten Mal Lehre
// und beim fünften Rauschen. Der Zustand schaut ausschließlich zurück — der
// Text zu Zug i hängt nur an den Zügen davor. Genau deshalb darf ein Zug
// ausgeliefert werden, sobald er gerechnet ist (stream.go), und refine()
// ruft nach dem Nachrechnen wieder den ganzen Pass auf.
package teaching

import (
	"fmt"
	"math"
	"strings"
)

// Fenster und Obergrenzen der Unterdrückung.
const (
	dedupWindow  = 5 // Züge, innerhalb derer derselbe Befund schweigt
	goldenGap    = 8 // Mindestabstand der Merkregel-Sätze
	tenukiMaxUse = 2 // "Tenuki ist erlaubt" je Partie
)

// sentenceBudget ist die Höchstzahl der Sätze je Kategorie. Sie ist der
// ganze Unterschied zwischen einer Randnotiz und einer Erklärung.
var sentenceBudget = map[string]int{
	"ausgezeichnet": 2,
	"gut":           3,
	"Ungenauigkeit": 4,
	"Fehler":        6,
	"grober Fehler": 8,
}

type dedupState struct {
	lastFinding map[string]int // Fingerprint → Zugnummer der letzten Nennung
	lastGolden  int
	tenukiUsed  int
	passUsed    bool
}

func newDedupState() *dedupState {
	return &dedupState{
		lastFinding: map[string]int{},
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

// noNegZero druckt Werte, die auf zwei Stellen null ergeben, ohne
// Vorzeichen: "Stärke -0.00" ist keine Aussage über die Stellung, sondern
// eine Eigenheit der Fließkommadarstellung.
func noNegZero(v float64) float64 {
	if math.Abs(v) < 0.005 {
		return 0
	}

	return v
}

// engineCost ist der Abstand des gespielten Zuges zur Erstwahl, wie die
// Engine ihn in EINER Suche gerechnet hat. Das ist der ehrlichere Preis:
// PointsLost setzt zwei getrennte Suchen voneinander ab und trägt deren
// Rauschen mit. Ohne Kandidaten zum gespielten Zug gibt es keinen Wert.
func engineCost(r *MoveReport) (float64, bool) {
	if r.Played == nil || len(r.Alternatives) == 0 {
		return 0, false
	}

	best := r.Alternatives[0].ScoreLead

	for _, alt := range r.Alternatives[1:] {
		if alt.ScoreLead > best {
			best = alt.ScoreLead
		}
	}

	cost := best - r.Played.ScoreLead

	if cost <= 0.05 {
		return 0, false
	}

	return cost, true
}

func renderRoseText(r *MoveReport, st *dedupState) string {
	d := r.rose
	budget := sentenceBudget[r.Category]

	if budget == 0 {
		budget = 3
	}

	var sentences []string
	add := func(s string) {
		if s != "" && len(sentences) < budget {
			sentences = append(sentences, s)
		}
	}

	if r.Pass {
		return passText(r, st, topFinding(d.findings))
	}

	// 1) Was die Stellung verlangte — nur der dringlichste Befund, und nur,
	//    wenn er nicht gerade eben schon dastand.
	top := topFinding(d.findings)
	// spoken hält die Kette fest, über die der Befund schon gesprochen hat:
	// Ihr Zustand nach dem Zug muss nicht noch einmal gemeldet werden.
	spoken := ""

	if top != nil {
		fingerprint := roseLetter(top.bucket) + ":" + top.rep

		if (top.atari && top.bucket == roseR) || st.allow(fingerprint, r.Number) {
			add(demandSentence(top, d.prevCoord))
			spoken = top.rep
		}
	} else if d.tenuki && st.tenukiUsed < tenukiMaxUse && severity(r.Category) <= 1 {
		st.tenukiUsed++
		add("Keine eigene Gruppe stand unter Druck; der Zug durfte woanders " +
			"spielen.")
	}

	// 2) Was auf dem Brett geschehen ist — außer über die Kette, die der
	//    Befund gerade behandelt hat.
	for _, line := range chainProse(r, spoken) {
		add(line)
	}

	// 3) Das Urteil über den Zug: bei Fehlern die volle Rechnung, sonst ein
	//    Satz oder gar keiner.
	for _, line := range verdictLines(r, st) {
		add(line)
	}

	// 4) Ein neu entstandener Formfehler — er benennt einen Mechanismus
	//    ("deckt weniger Freiheiten ab") und ist damit belegt.
	//
	//    Lob für eine gute Form steht hier bewusst NICHT: Der Katalogsatz
	//    dazu ist eine Faustregel ("gilt als nie schlecht"), und auf einer
	//    Karte, die ohnehin einen guten Zug zeigt, ist er reine Füllung.
	if d.shapeBad != nil && st.allow("shape:"+d.shapeBad.Name, r.Number) {
		add(fmt.Sprintf("%s bildet ein %s (%s): %s", r.Coord, d.shapeBad.Name,
			strings.Join(d.shapeBad.Points, " "), lowerFirst(d.shapeBad.Teaching)))
	}

	if len(sentences) == 0 {
		// Auch ein unauffälliger Zug bekommt eine Zeile — aber eine, die
		// eine Tatsache nennt, keine Floskel.
		return fallbackLine(r)
	}

	return strings.Join(sentences, "\n")
}

// verdictLines sind die Sätze über den Zug selbst. Ihre Zahl wächst mit dem
// Gewicht des Fehlers: Deckung mit der Erstwahl ist ein Satz, ein grober
// Fehler bekommt Preis, Alternative und gerechnete Fortsetzung.
func verdictLines(r *MoveReport, st *dedupState) []string {
	d := r.rose

	if d.matchesBest {
		return []string{fmt.Sprintf("%s ist die Erstwahl der Engine.", r.Coord)}
	}

	var out []string

	// Der Preis, so wie die Engine ihn in einer Suche gerechnet hat.
	if cost, ok := engineCost(r); ok && r.BestMove != "" {
		out = append(out, costSentence(r.Coord, r.BestMove, cost))
	} else if severity(r.Category) >= 2 && r.BestMove != "" {
		out = append(out, fmt.Sprintf("Die Engine spielt %s.", r.BestMove))
	}

	// Die Erstwahl gehört zu Ende gerechnet, nicht nur genannt.
	if severity(r.Category) >= 2 {
		if line := variationSentence("Nach "+r.BestMove, r.BestPV); line != "" {
			out = append(out, line)
		}
	}

	// Bei groben Fehlern auch die Fortsetzung des gespielten Zuges: Sie
	// zeigt, worauf er hinausläuft.
	if severity(r.Category) >= 4 && r.Played != nil {
		if line := variationSentence("Nach "+r.Coord, r.Played.PV); line != "" {
			out = append(out, line)
		}
	}

	// Hat die Engine den Zug gar nicht geprüft, ist das die deutlichste
	// Auskunft, die es über ihn gibt.
	if r.Played == nil && severity(r.Category) >= 3 && len(r.Alternatives) > 0 {
		visits := 0

		for _, alt := range r.Alternatives {
			visits += alt.Visits
		}

		if visits > 0 {
			out = append(out, unconsideredSentence(r.Coord, visits))
		}
	}

	// Die Merkregel, die der Verein sich gewünscht hat — selten, und immer
	// mit beiden Seiten benannt.
	if golden := goldenLine(r, st); golden != "" {
		out = append(out, golden)
	}

	return out
}

// goldenLine ist die einzige Regel, die dieser Text kennt: Dringlichkeit
// geht vor Größe. Sie fällt nur, wenn der Zug wirklich eine dringlichere
// Stufe übergangen hat, und dann mit beiden Stellen auf dem Brett.
func goldenLine(r *MoveReport, st *dedupState) string {
	d := r.rose
	played := bucketIndex(r.Rose.Played)

	if d.bucketBest < 0 || played <= d.bucketBest || severity(r.Category) < 2 {
		return ""
	}

	if r.Number-st.lastGolden < goldenGap && severity(r.Category) < 3 {
		return ""
	}

	f := findingOf(d.findings, d.bucketBest)

	if f == nil {
		return ""
	}

	st.lastGolden = r.Number

	// Was der Zug NICHT tut, ist hier die belegte Aussage: Seine Einstufung
	// sagt genau, dass er keinem dringlicheren Befund dient. Über seine
	// eigene Größe ist dagegen nichts bekannt — er hat gerade Punkte
	// gekostet.
	return fmt.Sprintf(
		"Dringlichkeit geht vor Größe: %s tut nichts für die Kette um %s.",
		r.Coord, f.rep)
}

// findingOf liefert den ersten Befund einer Stufe.
func findingOf(findings []roseFinding, bucket int) *roseFinding {
	for i := range findings {
		if findings[i].bucket == bucket {
			return &findings[i]
		}
	}

	return nil
}

// fallbackLine ist die eine Zeile für einen Zug, über den es sonst nichts
// Belegbares zu sagen gibt: die Stelle und die Engine-Erstwahl.
func fallbackLine(r *MoveReport) string {
	if r.BestMove != "" && !strings.EqualFold(r.BestMove, r.Coord) {
		return fmt.Sprintf("Erstwahl der Engine: %s.", r.BestMove)
	}

	return fmt.Sprintf("%s deckt sich mit der Rechnung der Engine.", r.Coord)
}

// passText behandelt Pass-Züge: unter Not ein Alarm, sonst eine knappe
// Einordnung — und ab dem zweiten ruhigen Pass nur noch die Tatsache.
func passText(r *MoveReport, st *dedupState, top *roseFinding) string {
	if top != nil && top.bucket == roseR {
		return fmt.Sprintf(
			"Pass, während die %s Kette um %s mit %d Freiheiten dasteht — "+
				"sie ist damit aufgegeben.",
			adjColor(top.color), top.rep, top.libs)
	}

	if !st.passUsed {
		st.passUsed = true

		return "Pass; die Engine sieht keinen Zug mehr, der Punkte bringt."
	}

	return "Pass."
}

// chainProse übersetzt die Effects in Sätze — und nur, wenn wirklich etwas
// passiert ist: Schlagen, Atari, Benson-Übergang, großer Stärkesprung oder
// akute Freiheitsnot. Die JSON-Effects bleiben immer vollständig;
// gefiltert wird nur die Prosa.
func chainProse(r *MoveReport, spoken string) []string {
	var out []string

	for i := range r.Effects {
		if len(out) >= 2 {
			break
		}

		e := &r.Effects[i]

		// Über diese Kette hat der Befund schon gesprochen, und zwar mit
		// der Ursache dazu; ein zweiter Satz darüber wäre bloß Widerhall.
		if e.Rep == spoken {
			continue
		}

		switch {
		case e.Captured:
			out = append(out, fmt.Sprintf(
				"%s schlägt %d Steine um %s.",
				r.Player, e.Stones, e.Rep))

		case e.InAtari:
			out = append(out, fmt.Sprintf(
				"Die %s Kette um %s steht danach im Atari.",
				adjColor(e.Color), e.Rep))

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
