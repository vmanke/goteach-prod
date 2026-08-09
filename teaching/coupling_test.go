package teaching

import (
	"math"
	"testing"
)

// pulse baut eine Spur mit einem Ausschlag um t0 herum.
func pulse(length, centre, width int, amplitude float64) []float64 {
	out := make([]float64, length)

	for t := range out {
		d := float64(t - centre)
		out[t] = amplitude * math.Exp(-0.5*(d/float64(width))*(d/float64(width)))
	}

	return out
}

func TestKorrelationFindetDenVersatz(t *testing.T) {
	const length = 200

	a := pulse(length, 60, 6, 1.0)
	b := pulse(length, 75, 6, 1.0)

	r, lag := correlate(a, b, MaxLag)

	if r < 0.9 {
		t.Fatalf("Korrelation %v, deutlich positiv erwartet", r)
	}

	// b läuft a um 15 Züge hinterher: a[t] passt zu b[t+15].
	if lag != 15 {
		t.Fatalf("Versatz %d, erwartet 15", lag)
	}
}

func TestKorrelationOhneZusammenhangBleibtKlein(t *testing.T) {
	const length = 200

	a := pulse(length, 40, 4, 1.0)
	b := pulse(length, 160, 4, 1.0)

	// Die Ausschläge liegen weit auseinander — innerhalb des erlaubten
	// Versatzes darf sich kein Zusammenhang ergeben.
	r, _ := correlate(a, b, MaxLag)

	if math.Abs(r) > 0.5 {
		t.Fatalf("Korrelation %v, klein erwartet", r)
	}
}

func TestKonstanteSpurKorreliertNicht(t *testing.T) {
	const length = 100

	constant := make([]float64, length)

	for i := range constant {
		constant[i] = 0.5
	}

	r, _ := correlate(constant, pulse(length, 50, 5, 1.0), MaxLag)

	if r != 0 {
		t.Fatalf("Korrelation %v, erwartet 0 bei konstanter Spur", r)
	}
}

// lcg ist ein deterministischer Pseudozufallsgenerator (splitmix64). Kein
// math/rand, damit die Tests ohne Seed-Verwaltung reproduzierbar bleiben.
//
// Splitmix und nicht ein einfacher linearer Kongruenzgenerator: Dessen
// Ströme sind für arithmetisch verwandte Startwerte untereinander
// korreliert. In einem Test, der gerade die *Unabhängigkeit* von Spuren
// voraussetzt, wäre das ein stiller Selbstbetrug.
func lcg(seed uint64) func() float64 {
	state := seed

	return func() float64 {
		state += 0x9E3779B97F4A7C15
		z := state
		z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
		z = (z ^ (z >> 27)) * 0x94D049BB133111EB
		z ^= z >> 31

		return float64(z>>11)/float64(uint64(1)<<53)*2 - 1
	}
}

// driver erzeugt eine stoßartige, aperiodische Spur — so verhält sich die
// Salienz einer Form: Sie flackert, wenn um sie gekämpft wird, und liegt
// sonst still.
//
// Bewusst NICHT aus Sinusanteilen zusammengesetzt: Periodische Spuren
// hebeln den zyklischen Surrogattest aus, weil eine Verschiebung dort nur
// die Phase ändert und den Zusammenhang gar nicht zerstört. Genau diese
// Voraussetzung — Aperiodizität — muss der Test abbilden.
func driver(length int, seed uint64) []float64 {
	next := lcg(seed)
	raw := make([]float64, length)

	for i := range raw {
		raw[i] = next()
	}

	return smooth(raw, 2)
}

// smooth glättet mit einem gleitenden Mittel, damit aus weißem Rauschen
// Stöße von einigen Zügen Länge werden.
func smooth(series []float64, radius int) []float64 {
	out := make([]float64, len(series))

	for i := range series {
		var sum float64
		var n int

		for k := -radius; k <= radius; k++ {
			j := i + k

			if j >= 0 && j < len(series) {
				sum += series[j]
				n++
			}
		}

		out[i] = sum / float64(n)
	}

	return out
}

// noisy legt eine unabhängige Störung über eine Spur.
func noisy(series []float64, seed uint64, amplitude float64) []float64 {
	noise := driver(len(series), seed)
	out := make([]float64, len(series))

	for t, v := range series {
		out[t] = v + amplitude*noise[t]
	}

	return out
}

// shiftBy verschiebt eine Spur um lag nach hinten (b[t] = a[t-lag]).
func shiftBy(series []float64, lag int) []float64 {
	out := make([]float64, len(series))

	for t := range out {
		src := t - lag

		if src >= 0 && src < len(series) {
			out[t] = series[src]
		}
	}

	return out
}

func TestSurrogattestVerwirftScheinkorrelationen(t *testing.T) {
	// Zwanzig Spuren ohne jeden Zusammenhang. Ohne Absicherung fände eine
	// Suche über 190 Paare und 41 Versätze hier reichlich "Kopplungen".
	const (
		length = 240
		count  = MaxTraces
	)

	traces := make([]trace, count)

	for i := range traces {
		traces[i] = trace{
			label:    "Form" + string(rune('a'+i)),
			salience: driver(length, uint64(i)*7919+11),
		}
	}

	edges := couple(traces, MaxLag)

	// Bei kontrollierter Fehltrefferquote dürfen höchstens vereinzelte
	// Kanten durchkommen, keine Kaskade.
	if len(edges) > 3 {
		t.Fatalf("%d Kanten zwischen unabhängigen Spuren — zu viele", len(edges))
	}
}

func TestEchteKopplungUeberlebtDenSurrogattest(t *testing.T) {
	const (
		length = 240
		lag    = 8
	)

	// Leiter und Kampf teilen sich einen gemeinsamen Verlauf, um acht Züge
	// versetzt — der Fall, den der Graph finden soll.
	common := driver(length, 4242)

	traces := []trace{
		{label: "Leiter", salience: noisy(common, 101, 0.35)},
		{label: "Kampf", salience: noisy(shiftBy(common, lag), 202, 0.35)},
	}

	// Unabhängige Spuren als Umfeld — der Test soll die Kopplung im Rauschen
	// finden, nicht in der Zweisamkeit.
	for i := 0; i < MaxTraces-2; i++ {
		traces = append(traces, trace{
			label:    "Rausch" + string(rune('a'+i)),
			salience: driver(length, uint64(i)*104729+5003),
		})
	}

	edges := couple(traces, MaxLag)
	found := false

	for _, e := range edges {
		if (e.From == "Leiter" && e.To == "Kampf") ||
			(e.From == "Kampf" && e.To == "Leiter") {
			found = true

			if e.Lag != lag {
				t.Errorf("Versatz %d, erwartet %d", e.Lag, lag)
			}
		}
	}

	if !found {
		t.Fatalf("echte Kopplung nicht gefunden, %d Kanten insgesamt", len(edges))
	}
}

func TestFDRHaeltDieFehltrefferQuote(t *testing.T) {
	// Lauter große p-Werte: nichts darf durchkommen.
	high := make([]float64, 100)

	for i := range high {
		high[i] = 0.4 + float64(i)/1000
	}

	for i, keep := range keepByFDR(high, 0.05) {
		if keep {
			t.Fatalf("p-Wert %v wurde behalten", high[i])
		}
	}

	// Ein einzelner sehr kleiner p-Wert muss dagegen bestehen.
	mixed := append([]float64{0.0001}, high...)
	kept := keepByFDR(mixed, 0.05)

	if !kept[0] {
		t.Fatal("deutlich signifikanter p-Wert wurde verworfen")
	}
}

func TestKomponentenFassenGekoppelteFormenZusammen(t *testing.T) {
	labels := []string{"A", "B", "C", "D"}
	edges := []Coupling{
		{From: "A", To: "B", Correlation: 0.9},
		{From: "C", To: "D", Correlation: 0.8},
	}

	got := components(labels, edges)

	if len(got) != 2 {
		t.Fatalf("%d Komponenten, erwartet 2: %v", len(got), got)
	}

	for _, comp := range got {
		if len(comp) != 2 {
			t.Fatalf("Komponente %v hat %d Mitglieder, erwartet 2", comp, len(comp))
		}
	}
}

func TestKomponentenSindDeterministisch(t *testing.T) {
	labels := []string{"D", "C", "B", "A"}
	edges := []Coupling{
		{From: "A", To: "B", Correlation: 0.9},
		{From: "B", To: "C", Correlation: 0.7},
	}

	first := components(labels, edges)
	second := components(labels, edges)

	if len(first) != len(second) {
		t.Fatal("unterschiedliche Komponentenzahl")
	}

	for i := range first {
		if len(first[i]) != len(second[i]) {
			t.Fatal("Komponenten unterscheiden sich")
		}

		for j := range first[i] {
			if first[i][j] != second[i][j] {
				t.Fatalf("Komponente %d weicht ab: %v vs %v", i, first[i], second[i])
			}
		}
	}

	// A, B und C hängen zusammen, D steht allein.
	if len(first[0]) != 3 || len(first[1]) != 1 {
		t.Fatalf("unerwartete Aufteilung: %v", first)
	}
}
