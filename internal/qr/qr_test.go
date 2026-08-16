package qr

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEncodeMatchesReference vergleicht Modul für Modul mit einer unabhängigen
// Implementierung (siehe testdata/README.md) — und zwar für jede der acht
// Masken, weil damit alles außer der Maskenwahl belegt ist: Kodierung,
// Fehlerkorrektur, Blockverschränkung, Platzierung und Formatinformation.
func TestEncodeMatchesReference(t *testing.T) {
	files, err := filepath.Glob("testdata/*.txt")

	if err != nil || len(files) == 0 {
		t.Fatalf("keine Vergleichsmatrizen gefunden: %v", err)
	}

	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			text, version, wanted := readReference(t, file)

			for mask, want := range wanted {
				m, err := encode([]byte(text), mask)

				if err != nil {
					t.Fatalf("encode(%q, Maske %d): %v", text, mask, err)
				}

				if m.Version() != version {
					t.Fatalf("Version %d, erwartet %d", m.Version(), version)
				}

				if m.Mask() != mask {
					t.Fatalf("Maske %d wurde nicht übernommen: %d", mask, m.Mask())
				}

				compare(t, m, want, mask)
			}
		})
	}
}

// readReference liest eine Vergleichsdatei: Zeichenkette, Version und die
// acht Matrizen in der Reihenfolge der Masken.
func readReference(t *testing.T, file string) (string, int, [8][]string) {
	t.Helper()

	raw, err := os.ReadFile(file)

	if err != nil {
		t.Fatalf("lesen: %v", err)
	}

	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")

	if len(lines) < 3 {
		t.Fatalf("Vergleichsdatei zu kurz: %d Zeilen", len(lines))
	}

	var version int

	if _, err := fmt.Sscanf(lines[1], "version=%d", &version); err != nil {
		t.Fatalf("Kopfzeile %q: %v", lines[1], err)
	}

	var (
		matrices [8][]string
		current  = -1
	)

	for _, line := range lines[2:] {
		var mask int

		if _, err := fmt.Sscanf(line, "mask=%d", &mask); err == nil {
			current = mask

			continue
		}

		if current < 0 {
			t.Fatalf("Matrixzeile vor der ersten Maskenangabe: %q", line)
		}

		matrices[current] = append(matrices[current], line)
	}

	for mask, m := range matrices {
		if len(m) == 0 {
			t.Fatalf("Maske %d fehlt in %s", mask, file)
		}
	}

	return lines[0], version, matrices
}

// compare vergleicht die Matrix mit der Vorlage und meldet den ersten
// abweichenden Punkt.
func compare(t *testing.T, m Matrix, want []string, mask int) {
	t.Helper()

	if m.Size() != len(want) {
		t.Fatalf("Maske %d: Größe %d, erwartet %d", mask, m.Size(), len(want))
	}

	for y, row := range want {
		if len(row) != m.Size() {
			t.Fatalf("Maske %d, Zeile %d: %d Module, erwartet %d",
				mask, y, len(row), m.Size())
		}

		for x, c := range row {
			if got := m.Dark(x, y); got != (c == '1') {
				t.Fatalf("Maske %d, Modul (%d,%d): dunkel=%v, erwartet %v",
					mask, x, y, got, c == '1')
			}
		}
	}
}

// TestMaskSelection prüft, dass die selbst gewählte Maske tatsächlich die
// kleinste Strafsumme trägt — die Wahl selbst ist keine Frage der
// Lesbarkeit, wohl aber, dass die Regeln angewandt werden.
func TestMaskSelection(t *testing.T) {
	for _, text := range []string{
		"goteach",
		"https://flascheleer-berlin.de/analyse",
		strings.Repeat("d", 106),
	} {
		chosen, err := Encode([]byte(text))

		if err != nil {
			t.Fatalf("Encode(%q): %v", text, err)
		}

		info := versions[chosen.Version()]
		g := layout(chosen.Version(), info,
			interleave(info, encodeData(info, []byte(text))))
		scores := g.maskScores()
		best := 0

		for mask, score := range scores {
			if score < scores[best] {
				best = mask
			}
		}

		if chosen.Mask() != best {
			t.Errorf("%q: gewählt wurde Maske %d, günstiger wäre %d",
				text, chosen.Mask(), best)
		}
	}
}

// TestPenaltyRules prüft die vier Regeln an von Hand nachrechenbaren
// Mustern, damit ein Fehler in der Bewertung nicht unbemerkt bleibt.
func TestPenaltyRules(t *testing.T) {
	t.Run("Regel1 Läufe", func(t *testing.T) {
		// Ein 7×7-Feld, dessen erste Zeile ganz dunkel ist: 7 waagerecht
		// (3+2) und sieben senkrechte Läufe der Länge 1, die nicht zählen.
		g := blank(7)

		for x := 0; x < 7; x++ {
			g.set(x, 0, true)
		}

		// Waagerecht: ein Lauf 7 → 3+2 = 5. Senkrecht: je Spalte ein Lauf
		// von 6 hellen Modulen → 7 × (3+1) = 28. Übrige Zeilen: je 7 helle
		// → 6 × 5 = 30.
		if got, want := runPenaltyAll(g), 5+28+30; got != want {
			t.Errorf("Regel 1: %d, erwartet %d", got, want)
		}
	})

	t.Run("Regel3 Sucherkern", func(t *testing.T) {
		// Der Kern am linken Rand zählt ohne weitere Bedingung.
		line := []bool{true, false, true, true, true, false, true,
			true, true, true, true}

		if got := patternHits(line); got != 1 {
			t.Errorf("Kern am Rand: %d Funde, erwartet 1", got)
		}

		// In der Mitte ohne helle Umgebung zählt er nicht.
		inner := append([]bool{true, true, true, true}, line...)

		if got := patternHits(inner); got != 0 {
			t.Errorf("Kern ohne Ruhefläche: %d Funde, erwartet 0", got)
		}

		// Mit vier hellen Modulen davor zählt er wieder.
		light := append([]bool{false, false, false, false}, line...)

		if got := patternHits(light); got != 1 {
			t.Errorf("Kern mit Ruhefläche: %d Funde, erwartet 1", got)
		}
	})

	t.Run("Regel4 Dunkelanteil", func(t *testing.T) {
		// Genau die Hälfte dunkel: keine Strafe.
		g := blank(10)

		for i := 0; i < 50; i++ {
			g.set(i%10, i/10, true)
		}

		if got := darkPenalty(g); got != 0 {
			t.Errorf("50 Prozent: %d, erwartet 0", got)
		}

		// 60 Prozent dunkel: zwei Fünf-Prozent-Schritte → 20.
		for i := 50; i < 60; i++ {
			g.set(i%10, i/10, true)
		}

		if got := darkPenalty(g); got != 20 {
			t.Errorf("60 Prozent: %d, erwartet 20", got)
		}
	})
}

// blank liefert ein Raster ohne Funktionsmuster für die Regeltests.
func blank(size int) *grid { return newGrid(size) }

// runPenaltyAll summiert Regel 1 über alle Zeilen und Spalten.
func runPenaltyAll(g *grid) int {
	score := 0

	for i := 0; i < g.size; i++ {
		score += runPenalty(g, i, true) + runPenalty(g, i, false)
	}

	return score
}

// darkPenalty rechnet Regel 4 einzeln nach.
func darkPenalty(g *grid) int {
	dark := 0

	for _, d := range g.dark {
		if d {
			dark++
		}
	}

	total := len(g.dark)

	return 10 * (abs(dark*20-total*10) / total)
}

// TestReedSolomonSyndrome prüft die Fehlerkorrektur ohne fremde Vorlage:
// Daten- und Prüfcodewörter zusammen bilden ein Vielfaches des
// Generatorpolynoms, also müssen alle Syndrome verschwinden.
func TestReedSolomonSyndrome(t *testing.T) {
	data := []byte("Züge zählen, nicht raten")

	for _, n := range []int{10, 16, 18, 24, 26} {
		code := append(append([]byte{}, data...), reedSolomon(data, n)...)

		for i := 0; i < n; i++ {
			// Auswertung des Codewortpolynoms an der Stelle 2^i.
			var acc byte

			for _, c := range code {
				acc = gfMul(acc, gfExp[i]) ^ c
			}

			if acc != 0 {
				t.Fatalf("n=%d: Syndrom %d ist %d statt 0", n, i, acc)
			}
		}
	}
}

// TestCapacityLimit belegt, dass zu lange Eingaben abgewiesen werden,
// statt einen Code zu liefern, der die Nutzlast stillschweigend verliert.
func TestCapacityLimit(t *testing.T) {
	if _, err := Encode(make([]byte, MaxBytes)); err != nil {
		t.Fatalf("%d Byte sollten passen: %v", MaxBytes, err)
	}

	if _, err := Encode(make([]byte, MaxBytes+1)); err == nil {
		t.Fatalf("%d Byte hätten abgewiesen werden müssen", MaxBytes+1)
	}

	if _, err := Encode(nil); err == nil {
		t.Fatal("leere Eingabe hätte abgewiesen werden müssen")
	}
}

// TestVersionChoice prüft, dass jeweils die kleinste passende Version
// gewählt wird — ein Code größer als nötig ist auf Papier schlechter
// lesbar.
func TestVersionChoice(t *testing.T) {
	limits := map[int]int{1: 14, 2: 26, 3: 42, 4: 62, 5: 84, 6: 106}

	for version, capacity := range limits {
		m, err := Encode(make([]byte, capacity))

		if err != nil {
			t.Fatalf("%d Byte: %v", capacity, err)
		}

		if m.Version() != version {
			t.Errorf("%d Byte ergeben Version %d, erwartet %d",
				capacity, m.Version(), version)
		}

		if want := version*4 + 17; m.Size() != want {
			t.Errorf("Version %d: Größe %d, erwartet %d",
				m.Version(), m.Size(), want)
		}
	}
}

// TestFunctionPatterns prüft die Funktionsmuster, die jedes Lesegerät zuerst
// sucht: die drei Sucher und das Taktmuster.
func TestFunctionPatterns(t *testing.T) {
	m, err := Encode([]byte("https://flascheleer-berlin.de/analyse"))

	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	last := m.Size() - 1

	for _, corner := range [][2]int{{0, 0}, {last - 6, 0}, {0, last - 6}} {
		for dy := 0; dy < 7; dy++ {
			for dx := 0; dx < 7; dx++ {
				ring := max(abs(dx-3), abs(dy-3))
				want := ring != 2

				if got := m.Dark(corner[0]+dx, corner[1]+dy); got != want {
					t.Fatalf("Sucher bei (%d,%d), Modul (%d,%d): %v, erwartet %v",
						corner[0], corner[1], dx, dy, got, want)
				}
			}
		}
	}

	for i := 8; i < m.Size()-8; i++ {
		if m.Dark(i, 6) != (i%2 == 0) || m.Dark(6, i) != (i%2 == 0) {
			t.Fatalf("Taktmuster bei %d falsch", i)
		}
	}
}

// TestSVGIsSelfContained sichert die Eigenschaft, auf die es beim Aushang
// ankommt: kein Verweis nach außen, und die Ruhezone bleibt erhalten.
func TestSVGIsSelfContained(t *testing.T) {
	m, err := Encode([]byte("https://flascheleer-berlin.de/analyse"))

	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	svg := SVG(m, 4, `Adresse "mit" <Sonderzeichen> & Co`)

	// Die Namensraum-Kennung ist keine Bezugsquelle und wird vor der
	// Prüfung entfernt; alles andere Verweisende wäre eine.
	withoutNamespace := strings.ReplaceAll(svg,
		`xmlns="http://www.w3.org/2000/svg"`, "")

	for _, forbidden := range []string{"http://", "https://", "url(", "<image"} {
		if strings.Contains(withoutNamespace, forbidden) {
			t.Errorf("SVG enthält %q — der Code hinge dann an einer fremden Quelle", forbidden)
		}
	}

	if want := fmt.Sprintf(`viewBox="0 0 %d %d"`, m.Size()+8, m.Size()+8); !strings.Contains(svg, want) {
		t.Errorf("Ruhezone fehlt: %s nicht gefunden", want)
	}

	if strings.Contains(svg, `& Co`) || !strings.Contains(svg, "&amp; Co") {
		t.Error("Alternativtext ist nicht maskiert")
	}

	// Ruhezone unter der Norm wird angehoben statt hingenommen.
	if !strings.Contains(SVG(m, 0, "x"),
		fmt.Sprintf(`viewBox="0 0 %d %d"`, m.Size()+8, m.Size()+8)) {
		t.Error("Ruhezone unter vier Modulen wurde nicht angehoben")
	}
}
