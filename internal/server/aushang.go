// Aushang: ein A3-Blatt zum Ausdrucken und Aufhängen, das zur Nutzung des
// Dienstes einlädt. Es wird serverseitig gerendert, weil die QR-Codes darin
// erzeugt werden — der Code auf die Beispielpartie zeigt auf die Instanz,
// welche das Blatt ausliefert, und stimmt damit auch für Vorschauen.
package server

import (
	"bytes"
	"html/template"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/vmanke/goteach-prod/internal/qr"
)

// clubAnalyseURL ist die öffentliche Analyseseite des Vereins — der Weg,
// den ein Mitglied nehmen soll. Der Dienst selbst hat keine Startseite für
// Laufkundschaft; deshalb steht hier die Vereinsseite und nicht baseURL.
const clubAnalyseURL = "https://flascheleer-berlin.de/analyse"

// aushangTmpl wird wie homeTmpl beim Start geparst, damit ein
// Template-Fehler in den Tests auffällt und nicht erst im Betrieb.
var aushangTmpl = template.Must(template.ParseFS(assetsFS, "assets/aushang.html.tmpl"))

// aushangData füttert assets/aushang.html.tmpl.
type aushangData struct {
	ClubURL    string
	ClubText   string
	PartieURL  string
	PartieText string
	QRClub     template.HTML
	QRPartie   template.HTML
	NoEngine   bool
}

// qrClub ist der Code auf die Vereinsseite. Nur er wird zwischengespeichert,
// weil nur seine Adresse feststeht. Ein Zwischenspeicher je Adresse wüchse
// dagegen unbegrenzt: Die Adresse der Beispielpartie enthält r.Host, und den
// bestimmt der Aufrufer.
var qrClub = sync.OnceValue(func() template.HTML { return qrSVG(clubAnalyseURL) })

// qrSVG liefert den Code zu url als Inline-SVG. Schlägt die Erzeugung fehl
// — etwa weil die Adresse die Kapazität übersteigt —, bleibt das Feld leer;
// das Blatt trägt die Adresse ohnehin auch im Klartext.
//
// Erzeugung statt Zwischenspeicher kostet 0,4 ms je Code (gemessen für eine
// Adresse in Version 3) und damit weniger, als ein mitwachsender Speicher
// wert wäre.
func qrSVG(url string) template.HTML {
	m, err := qr.Encode([]byte(url))

	if err != nil {
		log.Printf("goteach-server: QR-Code für %q: %v", url, err)

		return ""
	}

	return template.HTML(qr.SVG(m, 4, "QR-Code auf "+url))
}

// displayURL macht aus einer URL die Fassung fürs Auge: ohne Schema und
// ohne abschließenden Schrägstrich.
func displayURL(url string) string {
	return strings.TrimSuffix(
		strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://"), "/")
}

// handleAushang bedient GET /aushang.
func handleAushang(w http.ResponseWriter, r *http.Request) {
	if !allowGetHead(w, r) {
		return
	}

	partieURL := baseURL(r) + "/partie"

	data := aushangData{
		ClubURL:    clubAnalyseURL,
		ClubText:   displayURL(clubAnalyseURL),
		PartieURL:  partieURL,
		PartieText: displayURL(partieURL),
		QRClub:     qrClub(),
		QRPartie:   qrSVG(partieURL),
		NoEngine:   !engineAvailable(),
	}

	// Erst in einen Puffer: ein Template-Fehler ergibt einen sauberen 500
	// statt einer halb geschriebenen Seite.
	var buf bytes.Buffer

	if err := aushangTmpl.Execute(&buf, data); err != nil {
		log.Printf("goteach-server: Aushang-Template: %v", err)
		httpError(w, http.StatusInternalServerError, "Template-Fehler")

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// private: Die Antwort hängt am Host des Requests (Adresse und QR-Code
	// der Beispielpartie). Ein gemeinsamer Zwischenspeicher vor dem Dienst
	// dürfte sie sonst unter demselben Pfad an einen anderen Host ausliefern
	// — der gedruckte Code zeigte dann auf die falsche Instanz.
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(buf.Bytes())
}
