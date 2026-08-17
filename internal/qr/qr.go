// Package qr erzeugt QR-Codes für den Aushang — ohne Fremdabhängigkeit und
// ohne fremden Bilddienst. Ein extern erzeugter Code ließe die Zieladresse
// durch Dritte laufen und bräche, sobald jener Dienst abschaltet; auf einem
// gedruckten Blatt fällt das erst auf, wenn niemand mehr durchkommt.
//
// Zuschnitt: Byte-Modus, Fehlerkorrektur M, Versionen 1 bis 6 (bis 106 Byte).
// Ab Version 7 verlangt die Norm zusätzlich eingelegte Versionsblöcke; die
// entfallen hier, und zu lange Eingaben liefern einen Fehler statt eines
// stillschweigend falschen Codes. Die Adressen des Aushangs brauchen
// Version 3.
package qr

import "fmt"

// MaxBytes ist die größte Nutzlast dieses Erzeugers (Version 6, ECC M).
const MaxBytes = 106

// eccLevelM ist die Kennung der Fehlerkorrekturstufe M in der
// Formatinformation. Nur diese Stufe wird unterstützt.
const eccLevelM = 0b00

// versionInfo beschreibt eine Version bei Fehlerkorrektur M. Für die
// Versionen 1 bis 6 sind alle Blöcke gleich groß, weshalb hier eine
// Blockgruppe genügt.
type versionInfo struct {
	blocks       int // Zahl der Blöcke
	dataPerBlock int // Datencodewörter je Block
	eccPerBlock  int // Fehlerkorrekturcodewörter je Block
	alignCenter  int // Mittelpunkt des Ausrichtungsmusters, 0 = keines
	remainder    int // Restbits nach den Codewörtern
}

// versions ist ab Index 1 belegt; Index 0 bleibt leer.
var versions = [7]versionInfo{
	{},
	{blocks: 1, dataPerBlock: 16, eccPerBlock: 10, alignCenter: 0, remainder: 0},
	{blocks: 1, dataPerBlock: 28, eccPerBlock: 16, alignCenter: 18, remainder: 7},
	{blocks: 1, dataPerBlock: 44, eccPerBlock: 26, alignCenter: 22, remainder: 7},
	{blocks: 2, dataPerBlock: 32, eccPerBlock: 18, alignCenter: 26, remainder: 7},
	{blocks: 2, dataPerBlock: 43, eccPerBlock: 24, alignCenter: 30, remainder: 7},
	{blocks: 4, dataPerBlock: 27, eccPerBlock: 16, alignCenter: 34, remainder: 7},
}

// dataCodewords ist die Zahl der Datencodewörter der Version.
func (v versionInfo) dataCodewords() int { return v.blocks * v.dataPerBlock }

// capacity ist die Nutzlast in Byte: Datencodewörter abzüglich der 12 Bit
// für Moduskennung und Längenfeld, abgerundet auf ganze Byte.
func (v versionInfo) capacity() int { return (v.dataCodewords()*8 - 12) / 8 }

// Matrix ist das fertige Modulraster einschließlich Funktionsmustern.
// Zeile 0 liegt oben, Spalte 0 links.
type Matrix struct {
	size    int
	version int
	mask    int
	dark    []bool
}

// Size ist die Kantenlänge in Modulen (ohne Ruhezone).
func (m Matrix) Size() int { return m.size }

// Version ist die verwendete QR-Version (1 bis 6).
func (m Matrix) Version() int { return m.version }

// Mask ist die gewählte Maske (0 bis 7).
func (m Matrix) Mask() int { return m.mask }

// Dark sagt, ob das Modul an (x, y) dunkel ist. Punkte außerhalb gelten als
// hell, damit Aufrufer die Ruhezone ohne Sonderfall zeichnen können.
func (m Matrix) Dark(x, y int) bool {
	if x < 0 || y < 0 || x >= m.size || y >= m.size {
		return false
	}

	return m.dark[y*m.size+x]
}

// Encode kodiert data im Byte-Modus mit Fehlerkorrektur M und wählt die
// kleinste passende Version sowie die Maske mit der kleinsten Strafsumme.
func Encode(data []byte) (Matrix, error) { return encode(data, -1) }

// encode kodiert mit fester Maske; mask < 0 wählt sie nach den
// Strafregeln. Die feste Wahl braucht der Test gegen eine fremde
// Vorlage: Alle acht Masken ergeben lesbare Symbole, und welche eine
// Implementierung nimmt, hängt an Auslegungsfragen der Bewertung —
// verglichen werden soll die Kodierung, nicht diese Wahl.
func encode(data []byte, mask int) (Matrix, error) {
	if len(data) == 0 {
		return Matrix{}, fmt.Errorf("qr: leere Eingabe")
	}

	version := 0

	for v := 1; v <= 6; v++ {
		if len(data) <= versions[v].capacity() {
			version = v

			break
		}
	}

	if version == 0 {
		return Matrix{}, fmt.Errorf(
			"qr: %d Byte übersteigen die unterstützten %d Byte (Version 6, ECC M)",
			len(data), MaxBytes)
	}

	info := versions[version]
	codewords := interleave(info, encodeData(info, data))

	return buildMatrix(version, info, codewords, mask), nil
}

// encodeData baut den Bitstrom aus Moduskennung, Länge, Nutzlast,
// Abschluss und Füllbytes und gibt ihn als Codewörter zurück.
func encodeData(info versionInfo, data []byte) []byte {
	var bits bitBuffer

	bits.write(0b0100, 4)    // Byte-Modus
	bits.write(len(data), 8) // Längenfeld, 8 Bit bis Version 9

	for _, b := range data {
		bits.write(int(b), 8)
	}

	total := info.dataCodewords() * 8

	// Abschluss: bis zu vier Nullbits, dann auf volle Byte auffüllen.
	if rest := total - bits.len(); rest > 0 {
		bits.write(0, min(4, rest))
	}

	for bits.len()%8 != 0 {
		bits.write(0, 1)
	}

	// Füllbytes im vorgeschriebenen Wechsel.
	pad := []int{0xEC, 0x11}

	for i := 0; bits.len() < total; i++ {
		bits.write(pad[i%2], 8)
	}

	return bits.bytes()
}

// interleave verschränkt Daten- und Fehlerkorrekturcodewörter über die
// Blöcke, wie es die Norm für die Endfolge verlangt.
func interleave(info versionInfo, data []byte) []byte {
	blocksData := make([][]byte, info.blocks)
	blocksECC := make([][]byte, info.blocks)

	for i := range blocksData {
		blocksData[i] = data[i*info.dataPerBlock : (i+1)*info.dataPerBlock]
		blocksECC[i] = reedSolomon(blocksData[i], info.eccPerBlock)
	}

	out := make([]byte, 0, len(data)+info.blocks*info.eccPerBlock)

	for i := 0; i < info.dataPerBlock; i++ {
		for b := range blocksData {
			out = append(out, blocksData[b][i])
		}
	}

	for i := 0; i < info.eccPerBlock; i++ {
		for b := range blocksECC {
			out = append(out, blocksECC[b][i])
		}
	}

	return out
}

// bitBuffer sammelt Bits von links nach rechts.
type bitBuffer struct {
	data []byte
	n    int
}

func (b *bitBuffer) len() int { return b.n }

func (b *bitBuffer) write(value, width int) {
	for i := width - 1; i >= 0; i-- {
		if b.n%8 == 0 {
			b.data = append(b.data, 0)
		}

		if value>>i&1 == 1 {
			b.data[b.n/8] |= 1 << (7 - b.n%8)
		}

		b.n++
	}
}

func (b *bitBuffer) bytes() []byte { return b.data }
