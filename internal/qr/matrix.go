package qr

// Aufbau des Modulrasters: Funktionsmuster setzen, Codewörter im
// Zickzack einlegen, alle acht Masken nach den vier Strafregeln bewerten
// und die beste behalten.

// grid führt neben den Modulen mit, welche davon Funktionsmuster sind —
// diese bleiben von der Maske unberührt.
type grid struct {
	size     int
	dark     []bool
	function []bool
}

func newGrid(size int) *grid {
	return &grid{
		size:     size,
		dark:     make([]bool, size*size),
		function: make([]bool, size*size),
	}
}

func (g *grid) at(x, y int) bool { return g.dark[y*g.size+x] }

func (g *grid) set(x, y int, dark bool) { g.dark[y*g.size+x] = dark }

// setFunction setzt ein Modul und kennzeichnet es als Funktionsmuster.
func (g *grid) setFunction(x, y int, dark bool) {
	g.set(x, y, dark)
	g.function[y*g.size+x] = true
}

func (g *grid) isFunction(x, y int) bool { return g.function[y*g.size+x] }

// layout setzt Funktionsmuster und legt die Codewörter ein — der Stand
// vor der Maskierung.
func layout(version int, info versionInfo, codewords []byte) *grid {
	g := newGrid(version*4 + 17)

	g.drawFunctionPatterns(version, info)
	g.reserveFormat()
	g.placeCodewords(codewords, info.remainder)

	return g
}

// buildMatrix erzeugt das Raster aus den verschränkten Codewörtern.
// mask < 0 überlässt die Maskenwahl den Strafregeln.
func buildMatrix(version int, info versionInfo, codewords []byte, mask int) Matrix {
	g := layout(version, info, codewords)
	size := g.size

	if mask < 0 || mask > 7 {
		mask = g.chooseMask()
	}

	g.applyMask(mask)
	g.drawFormat(mask)

	return Matrix{size: size, version: version, mask: mask, dark: g.dark}
}

// drawFunctionPatterns zeichnet Sucher, Trenner, Taktmuster, das dunkle
// Modul und — ab Version 2 — das Ausrichtungsmuster.
func (g *grid) drawFunctionPatterns(version int, info versionInfo) {
	last := g.size - 1

	for _, c := range [][2]int{{0, 0}, {last - 6, 0}, {0, last - 6}} {
		g.drawFinder(c[0], c[1])
	}

	// Taktmuster in Zeile und Spalte 6.
	for i := 8; i < g.size-8; i++ {
		dark := i%2 == 0
		g.setFunction(i, 6, dark)
		g.setFunction(6, i, dark)
	}

	// Ausrichtungsmuster: für die Versionen 2 bis 6 genau eines, weil die
	// übrigen Kombinationen der Mittelpunkte auf den Suchern lägen.
	if info.alignCenter > 0 {
		g.drawAlignment(info.alignCenter, info.alignCenter)
	}

	// Das dunkle Modul steht fest bei (8, 4·Version+9).
	g.setFunction(8, version*4+9, true)
}

// drawFinder zeichnet ein Suchermuster mit linker oberer Ecke (x0, y0)
// samt Trennstreifen.
func (g *grid) drawFinder(x0, y0 int) {
	for dy := -1; dy <= 7; dy++ {
		for dx := -1; dx <= 7; dx++ {
			x, y := x0+dx, y0+dy

			if x < 0 || y < 0 || x >= g.size || y >= g.size {
				continue
			}

			ring := max(abs(dx-3), abs(dy-3))
			g.setFunction(x, y, ring != 2 && ring <= 3)
		}
	}
}

// drawAlignment zeichnet ein Ausrichtungsmuster um (cx, cy).
func (g *grid) drawAlignment(cx, cy int) {
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			g.setFunction(cx+dx, cy+dy, max(abs(dx), abs(dy)) != 1)
		}
	}
}

// reserveFormat hält die Felder der Formatinformation frei, damit die
// Codewörter sie nicht belegen; ihr Inhalt folgt nach der Maskenwahl.
func (g *grid) reserveFormat() {
	for i := 0; i < 15; i++ {
		x1, y1 := formatPos1(i, g.size)
		x2, y2 := formatPos2(i, g.size)
		g.setFunction(x1, y1, false)
		g.setFunction(x2, y2, false)
	}
}

// formatPos1 ist die i-te Stelle der Formatinformation um den linken
// oberen Sucher (i = 0 ist das höchstwertige Bit).
func formatPos1(i, size int) (int, int) {
	_ = size

	if i < 6 {
		return 8, i
	}

	if i == 6 {
		return 8, 7
	}

	if i == 7 {
		return 8, 8
	}

	if i == 8 {
		return 7, 8
	}

	return 14 - i, 8
}

// formatPos2 ist die zweite, verteilte Kopie derselben Stelle.
func formatPos2(i, size int) (int, int) {
	if i < 8 {
		return size - 1 - i, 8
	}

	return 8, size - 15 + i
}

// placeCodewords legt die Bits im Zickzack von rechts unten ein und
// überspringt die Taktspalte 6. Die remainder Restmodule der Version
// bleiben hell; sie tragen keine Daten.
func (g *grid) placeCodewords(codewords []byte, remainder int) {
	_ = remainder

	bit := 0
	upward := true

	for right := g.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5 // Taktspalte überspringen
		}

		for row := 0; row < g.size; row++ {
			y := row

			if upward {
				y = g.size - 1 - row
			}

			for dx := 0; dx < 2; dx++ {
				x := right - dx

				if g.isFunction(x, y) {
					continue
				}

				dark := false

				if bit < len(codewords)*8 {
					dark = codewords[bit/8]>>(7-bit%8)&1 == 1
				}

				g.set(x, y, dark)

				bit++
			}
		}

		upward = !upward
	}
}

// maskCondition ist die Bedingung der Maske n für das Modul (x, y).
func maskCondition(n, x, y int) bool {
	switch n {
	case 0:
		return (y+x)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (y+x)%3 == 0
	case 4:
		return (y/2+x/3)%2 == 0
	case 5:
		return y*x%2+y*x%3 == 0
	case 6:
		return (y*x%2+y*x%3)%2 == 0
	default:
		return ((y+x)%2+y*x%3)%2 == 0
	}
}

// applyMask kippt alle Datenmodule, auf welche die Bedingung zutrifft.
func (g *grid) applyMask(n int) {
	for y := 0; y < g.size; y++ {
		for x := 0; x < g.size; x++ {
			if g.isFunction(x, y) || !maskCondition(n, x, y) {
				continue
			}

			g.set(x, y, !g.at(x, y))
		}
	}
}

// chooseMask probiert alle acht Masken und nimmt die mit der kleinsten
// Strafsumme; bei Gleichstand gewinnt die kleinere Nummer.
//
// Die Formatinformation bleibt dabei ungeschrieben, ihre Felder zählen
// also als hell: Die Norm bewertet das maskierte Symbol, und die
// Formatbits stehen erst fest, wenn die Maske gewählt ist.
func (g *grid) chooseMask() int {
	scores := g.maskScores()
	best := 0

	for n, score := range scores {
		if score < scores[best] {
			best = n
		}
	}

	return best
}

// maskScores bewertet alle acht Masken auf demselben Stand.
func (g *grid) maskScores() [8]int {
	var scores [8]int

	for n := range scores {
		g.applyMask(n)
		scores[n] = g.penalty()
		g.applyMask(n) // Maske ist selbstinvers
	}

	return scores
}

// penalty summiert die vier Strafregeln der Norm.
func (g *grid) penalty() int {
	score := 0

	// Regel 1: Läufe gleicher Farbe ab fünf Modulen, zeilen- und spaltenweise.
	for i := 0; i < g.size; i++ {
		score += runPenalty(g, i, true) + runPenalty(g, i, false)
	}

	// Regel 2: gleichfarbige 2×2-Blöcke.
	for y := 0; y < g.size-1; y++ {
		for x := 0; x < g.size-1; x++ {
			c := g.at(x, y)

			if c == g.at(x+1, y) && c == g.at(x, y+1) && c == g.at(x+1, y+1) {
				score += 3
			}
		}
	}

	// Regel 3: das sucherähnliche Muster, je Fund 40.
	score += 40 * finderLikeCount(g)

	// Regel 4: Abweichung des Dunkelanteils von der Hälfte.
	dark := 0

	for _, d := range g.dark {
		if d {
			dark++
		}
	}

	// Ganzzahlig gerechnet: |Anteil − 50 %| / 5 % = |20·dunkel − 10·gesamt| / gesamt.
	total := len(g.dark)
	score += 10 * (abs(dark*20-total*10) / total)

	return score
}

// runPenalty wertet Regel 1 für eine Zeile (row) oder Spalte aus.
func runPenalty(g *grid, i int, row bool) int {
	score, run := 0, 1

	for k := 1; k < g.size; k++ {
		prev, cur := g.at(k-1, i), g.at(k, i)

		if !row {
			prev, cur = g.at(i, k-1), g.at(i, k)
		}

		if prev == cur {
			run++

			continue
		}

		score += runScore(run)
		run = 1
	}

	return score + runScore(run)
}

func runScore(run int) int {
	if run < 5 {
		return 0
	}

	return 3 + run - 5
}

// finderLikeCount zählt das sucherähnliche Muster in Zeilen wie Spalten.
func finderLikeCount(g *grid) int {
	count := 0
	line := make([]bool, g.size)

	for i := 0; i < g.size; i++ {
		for k := 0; k < g.size; k++ {
			line[k] = g.at(k, i)
		}

		count += patternHits(line)

		for k := 0; k < g.size; k++ {
			line[k] = g.at(i, k)
		}

		count += patternHits(line)
	}

	return count
}

// finderCore ist das Verhältnis 1:1:3:1:1, also der Kern eines Suchers.
var finderCore = []bool{true, false, true, true, true, false, true}

// patternHits zählt die Funde in einer Zeile oder Spalte: der Kern zählt,
// wenn ihm vier helle Module vorangehen oder folgen. Am Symbolrand tritt
// die Ruhezone an deren Stelle, weshalb ein Fund dort ohne weitere
// Bedingung zählt. Nach einem gezählten Fund setzt die Suche hinter dem
// Kern auf, damit dasselbe Muster nicht mehrfach in die Wertung geht.
func patternHits(line []bool) int {
	size := len(line)
	count := 0

	for idx := 0; idx <= size-7; {
		if !matchesAt(line, finderCore, idx) {
			idx++

			continue
		}

		next := idx + 7

		if idx == 0 || idx == size-7 ||
			allLight(line[max(idx-4, 0):idx]) ||
			allLight(line[next:min(next+4, size)]) {
			count++
		} else {
			next = idx + 4
		}

		idx = next
	}

	return count
}

// allLight sagt, ob der Abschnitt nur helle Module enthält; ein leerer
// Abschnitt am Rand gilt als hell, weil dort die Ruhezone liegt.
func allLight(section []bool) bool {
	for _, d := range section {
		if d {
			return false
		}
	}

	return true
}

// matchesAt prüft want ab Position start; ein Überstand über den Rand
// gilt als Fehlschlag.
func matchesAt(line, want []bool, start int) bool {
	if start < 0 || start+len(want) > len(line) {
		return false
	}

	for i, v := range want {
		if line[start+i] != v {
			return false
		}
	}

	return true
}

// drawFormat schreibt die 15 Bit Formatinformation an beide Stellen.
func (g *grid) drawFormat(mask int) {
	bits := formatBits(eccLevelM, mask)

	// Die Stellenfolge trägt das niederwertigste Bit zuerst.
	for i := 0; i < 15; i++ {
		dark := bits>>i&1 == 1
		x1, y1 := formatPos1(i, g.size)
		x2, y2 := formatPos2(i, g.size)
		g.setFunction(x1, y1, dark)
		g.setFunction(x2, y2, dark)
	}
}

// formatBits baut die Formatinformation: fünf Datenbits, zehn BCH-Bits,
// das Ganze gegen 0x5412 verschlüsselt, damit sie nie ganz hell wird.
func formatBits(ecc, mask int) int {
	data := ecc<<3 | mask
	rest := data << 10

	for i := 14; i >= 10; i-- {
		if rest>>i&1 == 1 {
			rest ^= 0b10100110111 << (i - 10)
		}
	}

	return (data<<10 | rest) ^ 0b101010000010010
}

func abs(v int) int {
	if v < 0 {
		return -v
	}

	return v
}
