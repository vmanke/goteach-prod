package qr

import (
	"fmt"
	"strings"
)

// SVG zeichnet die Matrix als eigenständiges Inline-SVG: ein Pfad auf
// weißem Grund, kein externer Verweis, keine Datei daneben. Die Farben
// stehen fest und folgen nicht dem Farbschema der Seite — Lesegeräte
// erwarten dunkel auf hell, und ein im Dunkelmodus umgedrehter Code wird
// vielerorts nicht gelesen.
//
// quiet ist die Ruhezone in Modulen; die Norm verlangt vier, und weniger
// macht den Code für manche Lesegeräte unbrauchbar. label wird als
// Alternativtext gesetzt, damit die Zieladresse auch vorgelesen wird.
func SVG(m Matrix, quiet int, label string) string {
	if quiet < 4 {
		quiet = 4
	}

	span := m.size + 2*quiet

	var path strings.Builder

	// Waagerechte Läufe zusammenfassen: ein Rechteck je Lauf statt je Modul
	// hält das Markup klein genug, um es in die Seite zu schreiben.
	for y := 0; y < m.size; y++ {
		for x := 0; x < m.size; {
			if !m.Dark(x, y) {
				x++

				continue
			}

			run := 1

			for x+run < m.size && m.Dark(x+run, y) {
				run++
			}

			fmt.Fprintf(&path, "M%d %dh%dv1h-%dz",
				x+quiet, y+quiet, run, run)

			x += run
		}
	}

	return fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" `+
			`role="img" aria-label="%s" shape-rendering="crispEdges">`+
			`<rect width="%d" height="%d" fill="#fff"/>`+
			`<path d="%s" fill="#000"/></svg>`,
		span, span, escapeAttr(label), span, span, path.String())
}

// escapeAttr entschärft die Zeichen, die in einem Attributwert stören.
var attrEscaper = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

func escapeAttr(s string) string { return attrEscaper.Replace(s) }
