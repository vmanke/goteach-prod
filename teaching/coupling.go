package teaching

import (
	"math"
	"sort"
)

// MaxLag begrenzt den Versatz, mit dem zwei Formspuren verglichen werden.
// Ein Zusammenhang, der sich erst nach mehr als zwanzig Zügen zeigt, ist
// keiner mehr, den ein Lehrtext behaupten sollte.
const MaxLag = 20

// surrogateSamples ist die Zahl der Stichproben für die Nullverteilung.
const surrogateSamples = 240

// falseDiscoveryRate ist der Anteil an Fehltreffern, den der Kopplungsgraph
// höchstens enthalten soll.
//
// Zehn Prozent und nicht fünf: Der Graph dient dazu, eine Partie in
// Erzählstränge zu *gruppieren*, nicht dazu, eine Behauptung über Go zu
// belegen. Eine gelegentlich zu viel gezogene Kante führt zu einem etwas
// größeren Strang; eine zu wenig gezogene lässt einen echten Zusammenhang
// unter den Tisch fallen — der teurere Fehler von beiden.
const falseDiscoveryRate = 0.10

// minCorrelation ist die kleinste Korrelation, die überhaupt als Kopplung
// gemeldet wird — unabhängig von der Signifikanz.
//
// Der Grund ist eine reale Fehlerquelle: Existiert eine Form nur wenige Züge
// lang, ist ihre Spur fast überall null, und dann sind *alle* Korrelationen
// winzig. Die Nullverteilung schrumpft mit, und schon ein r von 0.01 liegt
// viele Standardabweichungen über ihr — statistisch signifikant, inhaltlich
// nichts. Signifikanz ohne Effektstärke ist keine Erkenntnis.
const minCorrelation = 0.30

// MaxTraces begrenzt, wie viele Formen in den Kopplungsgraphen eingehen.
//
// Der Grund ist nicht nur Rechenzeit: Die Paarzahl wächst quadratisch, und
// mit ihr die Multiplizitätsstrafe. Zu viele Formen aufzunehmen senkt die
// Trennschärfe so weit, dass am Ende gar keine Kante mehr besteht — mehr
// Kandidaten liefern hier also *weniger* Erkenntnis.
const MaxTraces = 12

// shiftStride erzeugt die Verschiebungen der Surrogatstichproben. Eine
// Primzahl, damit die Verschiebungen die Zeitachse gleichmäßig abdecken,
// ohne Zufallsgenerator — die Analyse muss reproduzierbar bleiben.
const shiftStride = 7919

// Coupling ist eine Kante des Kopplungsgraphen: zwei Formen, deren Spuren
// über die Zeit miteinander laufen.
type Coupling struct {
	From        string  `json:"from"`
	To          string  `json:"to"`
	Correlation float64 `json:"correlation"`
	Lag         int     `json:"lag"`
}

// trace ist die Zeitspur einer Forminstanz über die ganze Partie.
type trace struct {
	label    string
	salience []float64
	strength []float64
}

// correlate liefert die stärkste normierte Kreuzkorrelation zweier Spuren
// und den zugehörigen Versatz.
//
// Der Versatz trägt Information: Läuft A der Spur B voraus, bewegt sich A
// zuerst. Das ist ein zeitlicher Hinweis und ausdrücklich keine Aussage über
// Ursache und Wirkung — der Lehrtext muss das offenlassen.
func correlate(a, b []float64, maxLag int) (float64, int) {
	best, bestLag := 0.0, 0

	for lag := -maxLag; lag <= maxLag; lag++ {
		r := pearsonShifted(a, b, lag)

		if math.Abs(r) > math.Abs(best) {
			best, bestLag = r, lag
		}
	}

	return best, bestLag
}

// pearsonShifted korreliert a[t] mit b[t+lag] über den gemeinsamen Bereich.
func pearsonShifted(a, b []float64, lag int) float64 {
	n := len(a)

	if len(b) != n {
		return 0
	}

	from, to := 0, n

	if lag > 0 {
		to = n - lag
	} else {
		from = -lag
	}

	if to-from < 8 {
		// Zu kurzer Überlapp: jede Korrelation wäre Zufall.
		return 0
	}

	var sumA, sumB float64

	for t := from; t < to; t++ {
		sumA += a[t]
		sumB += b[t+lag]
	}

	count := float64(to - from)
	meanA, meanB := sumA/count, sumB/count

	var cov, varA, varB float64

	for t := from; t < to; t++ {
		da := a[t] - meanA
		db := b[t+lag] - meanB

		cov += da * db
		varA += da * da
		varB += db * db
	}

	if varA <= 1e-12 || varB <= 1e-12 {
		return 0
	}

	return cov / math.Sqrt(varA*varB)
}

// permutationP liefert den p-Wert einer beobachteten Korrelation gegen eine
// Nullverteilung aus zyklisch verschobenen Spuren.
//
// Zyklisch verschoben und nicht neu gewürfelt: Das zerstört den Zusammenhang
// der beiden Spuren, erhält aber Länge, Verteilung und Autokorrelation jeder
// einzelnen — also genau die Eigenschaften, aus denen zufällige Korrelation
// entsteht. Die Nullverteilung wird *je Paar* gebildet; eine gemeinsame
// Verteilung über alle Paare wäre billiger, würde aber die Eigenart der
// beteiligten Spuren einebnen und damit am falschen Maßstab messen.
func permutationP(a, b []float64, maxLag int) (float64, int, float64) {
	observed, lag := correlate(a, b, maxLag)
	magnitude := math.Abs(observed)
	n := len(b)

	if n < 16 {
		return observed, lag, 1.0
	}

	// Die Verschiebung muss den Suchbereich deutlich überschreiten, sonst
	// zerstört sie den Zusammenhang gar nicht: Wird eine Spur um weniger als
	// maxLag verschoben, findet die Versatzsuche dieselbe Übereinstimmung
	// einfach bei einem anderen Versatz wieder. Solche Stichproben gehören
	// nicht zur Nullverteilung — sie enthalten noch das Signal, das sie
	// widerlegen sollen. Der Zuschlag deckt zusätzlich die Eigenkorrelation
	// der Spuren ab.
	minShift := 2*maxLag + 8
	span := n - 2*minShift

	if span <= 1 {
		return observed, lag, 1.0
	}

	exceed := 0
	surrogates := make([]float64, 0, surrogateSamples)

	for k := 0; k < surrogateSamples; k++ {
		shift := minShift + (k*shiftStride)%span
		r, _ := correlate(a, rotateSeries(b, shift), maxLag)

		surrogates = append(surrogates, math.Abs(r))

		if math.Abs(r) >= magnitude {
			exceed++
		}
	}

	if exceed > 0 {
		// Im Rumpf der Verteilung ist der empirische Anteil die ehrlichste
		// Schätzung. Der Zähler beginnt bei eins, weil der beobachtete Wert
		// selbst zur Nullverteilung gehört.
		return observed, lag, float64(exceed+1) / float64(surrogateSamples+1)
	}

	return observed, lag, gumbelTail(surrogates, magnitude)
}

// eulerMascheroni wird für die Momentenschätzung der Gumbel-Verteilung
// gebraucht.
const eulerMascheroni = 0.5772156649015329

// gumbelTail schätzt die Überschreitungswahrscheinlichkeit im äußersten Rand
// der Nullverteilung.
//
// Nötig wird das durch die Multiplizität: Bei zwanzig Formen werden 190 Paare
// geprüft, und die Benjamini-Hochberg-Schranke liegt für den besten Rang bei
// 0.05/190 ≈ 0.00026. Aus 240 Permutationen lässt sich aber kein p-Wert unter
// 1/241 ≈ 0.0042 ablesen — rein empirisch könnte also *keine* Kante je
// bestehen, egal wie deutlich der Zusammenhang ist.
//
// Die Verteilung wird deshalb im Rand fortgeschrieben. Gumbel und nicht
// Normal, weil die Teststatistik selbst schon ein Maximum ist (über alle
// Versätze) — für Maxima ist die Extremwertverteilung die richtige Familie.
// Das ist eine Näherung, und sie ist der Preis dafür, überhaupt streng gegen
// Mehrfachvergleiche absichern zu können.
func gumbelTail(surrogates []float64, observed float64) float64 {
	if len(surrogates) < 8 {
		return 1.0
	}

	var sum float64

	for _, v := range surrogates {
		sum += v
	}

	mean := sum / float64(len(surrogates))

	var variance float64

	for _, v := range surrogates {
		variance += (v - mean) * (v - mean)
	}

	deviation := math.Sqrt(variance / float64(len(surrogates)-1))

	if deviation < 1e-12 {
		return 1.0
	}

	// Momentenschätzung der Gumbel-Parameter.
	beta := deviation * math.Sqrt(6) / math.Pi
	location := mean - eulerMascheroni*beta

	z := (observed - location) / beta

	// 1 - exp(-exp(-z)), numerisch stabil über Expm1.
	p := -math.Expm1(-math.Exp(-z))

	// Untergrenze gegen Überzuversicht: Die Fortschreibung darf den
	// Stichprobenumfang um eine Größenordnung übertreffen, nicht um beliebig
	// viele. Ohne diese Bremse erzeugt eine zufällig günstige Stichprobe
	// p-Werte, die durch nichts gedeckt sind — und die dann jede
	// Mehrfachvergleichskorrektur mühelos unterlaufen.
	return math.Max(p, 1.0/(10*float64(surrogateSamples+1)))
}

// rotateSeries verschiebt eine Spur zyklisch.
func rotateSeries(series []float64, shift int) []float64 {
	n := len(series)
	out := make([]float64, n)

	for i := range series {
		out[i] = series[(i+shift)%n]
	}

	return out
}

// keepByFDR wählt aus den p-Werten die Kanten aus, die bei der gegebenen
// False-Discovery-Rate bestehen (Benjamini-Hochberg).
//
// Eine feste Schwelle je Einzeltest wäre hier falsch: Bei zwei Dutzend Formen
// werden mehrere hundert Paare geprüft, und bei p ≤ 0.05 wären ein Dutzend
// Fehltreffer der Normalfall — der Graph bestünde zu großen Teilen aus
// Einbildung.
func keepByFDR(pValues []float64, rate float64) []bool {
	m := len(pValues)
	keep := make([]bool, m)

	if m == 0 {
		return keep
	}

	order := make([]int, m)

	for i := range order {
		order[i] = i
	}

	sort.Slice(order, func(i, j int) bool {
		return pValues[order[i]] < pValues[order[j]]
	})

	cutoff := -1

	for rank, idx := range order {
		limit := rate * float64(rank+1) / float64(m)

		if pValues[idx] <= limit {
			cutoff = rank
		}
	}

	for rank, idx := range order {
		if rank <= cutoff {
			keep[idx] = true
		}
	}

	return keep
}

// couple baut den Kopplungsgraphen: alle Formpaare, deren Korrelation die
// Nullverteilung überlebt.
func couple(traces []trace, maxLag int) []Coupling {
	var (
		candidates []Coupling
		pValues    []float64
	)

	for i := 0; i < len(traces); i++ {
		for j := i + 1; j < len(traces); j++ {
			r, lag, p := permutationP(traces[i].salience, traces[j].salience, maxLag)

			from, to := i, j

			// Die vorauslaufende Spur zuerst nennen.
			if lag < 0 {
				from, to = j, i
				lag = -lag
			}

			candidates = append(candidates, Coupling{
				From:        traces[from].label,
				To:          traces[to].label,
				Correlation: r,
				Lag:         lag,
			})
			pValues = append(pValues, p)
		}
	}

	// Reihenfolge beachten: Die Fehltrefferkorrektur läuft über *alle*
	// geprüften Paare. Würde vorher nach Effektstärke gefiltert, schrumpfte
	// der Nenner der Korrektur und sie würde schwächer statt strenger — die
	// Vorauswahl ist nicht unabhängig vom p-Wert, denn große Korrelationen
	// haben kleine p-Werte. Erst korrigieren, dann die Effektstärke fordern.
	keep := keepByFDR(pValues, falseDiscoveryRate)
	var out []Coupling

	for i, ok := range keep {
		if ok && math.Abs(candidates[i].Correlation) >= minCorrelation {
			out = append(out, candidates[i])
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if math.Abs(out[i].Correlation) != math.Abs(out[j].Correlation) {
			return math.Abs(out[i].Correlation) > math.Abs(out[j].Correlation)
		}

		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}

		return out[i].To < out[j].To
	})

	return out
}

// components zerlegt den Kopplungsgraphen in Zusammenhangskomponenten. Genau
// hier verliert die Fensterung ihre räumliche Beschränkung: Eine Leiter am
// einen Brettrand und ein Kampf am anderen landen in derselben Komponente,
// wenn ihre Spuren zusammen laufen.
func components(labels []string, edges []Coupling) [][]string {
	parent := map[string]string{}

	var find func(string) string
	find = func(x string) string {
		if parent[x] == "" || parent[x] == x {
			parent[x] = x

			return x
		}

		root := find(parent[x])
		parent[x] = root

		return root
	}

	union := func(a, b string) {
		ra, rb := find(a), find(b)

		if ra != rb {
			parent[ra] = rb
		}
	}

	for _, label := range labels {
		find(label)
	}

	for _, e := range edges {
		union(e.From, e.To)
	}

	grouped := map[string][]string{}

	for _, label := range labels {
		root := find(label)
		grouped[root] = append(grouped[root], label)
	}

	out := make([][]string, 0, len(grouped))

	for _, members := range grouped {
		sort.Strings(members)
		out = append(out, members)
	}

	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}

		return out[i][0] < out[j][0]
	})

	return out
}
