package server

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/vmanke/goteach-prod/board"
)

func TestAushangRenders(t *testing.T) {
	rr := serve(t, httptest.NewRequest(http.MethodGet, "/aushang", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, erwartet 200", rr.Code)
	}

	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}

	body := rr.Body.String()

	for _, want := range []string{
		"flascheleer-berlin.de/analyse", // der Weg, den das Blatt weist
		"Zug für Zug erklärt",           // die Schlagzeile
		"@page",                         // ohne Druckformat kein Aushang
		"size: A3 portrait",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Aushang ohne %q", want)
		}
	}

	// Beide QR-Codes müssen im Blatt stehen: der große auf die
	// Vereinsseite, der kleine auf die Beispielpartie dieser Instanz.
	if n := strings.Count(body, `aria-label="QR-Code auf `); n != 2 {
		t.Errorf("%d QR-Codes im Blatt, erwartet 2", n)
	}

	if !strings.Contains(body, `class="gb-wood"`) {
		t.Error("das Brett der Vereinsseite fehlt")
	}

	if !strings.Contains(body, `aria-label="QR-Code auf `+clubAnalyseURL+`"`) {
		t.Error("QR-Code auf die Vereinsseite fehlt")
	}

	if !strings.Contains(body, `aria-label="QR-Code auf http://example.com/partie"`) {
		t.Error("QR-Code auf die Beispielpartie fehlt oder zeigt woandershin")
	}
}

// TestAushangIstNichtGemeinsamZwischenspeicherbar: Das Blatt hängt am Host
// des Requests. Ein gemeinsamer Zwischenspeicher dürfte es nicht unter
// demselben Pfad an einen anderen Host weiterreichen, sonst zeigt der
// gedruckte QR-Code auf eine fremde Instanz.
func TestAushangIstNichtGemeinsamZwischenspeicherbar(t *testing.T) {
	rr := serve(t, httptest.NewRequest(http.MethodGet, "/aushang", nil))
	cc := rr.Header().Get("Cache-Control")

	if strings.Contains(cc, "public") {
		t.Errorf("Cache-Control = %q — host-abhängige Antwort darf nicht public sein", cc)
	}

	if !strings.Contains(cc, "private") && cc != "" {
		t.Errorf("Cache-Control = %q, erwartet private oder gar keine Angabe", cc)
	}
}

// TestAushangZwischenspeichertNichtJeHost: Der Host stammt vom Aufrufer.
// Würde je Host etwas dauerhaft abgelegt, wüchse der Speicher unbegrenzt.
// Geprüft wird die beobachtbare Seite davon: Jeder Host bekommt seinen
// eigenen, richtigen Code — auch nach vielen verschiedenen Hosts.
func TestAushangZwischenspeichertNichtJeHost(t *testing.T) {
	for _, host := range []string{"a.example", "b.example", "c.example", "a.example"} {
		req := httptest.NewRequest(http.MethodGet, "/aushang", nil)
		req.Host = host

		body := serve(t, req).Body.String()

		if !strings.Contains(body, `aria-label="QR-Code auf http://`+host+`/partie"`) {
			t.Errorf("Host %s: Blatt zeigt nicht auf die eigene Beispielpartie", host)
		}
	}
}

// TestAushangFollowsForwardedProto sichert, dass der QR-Code auf die
// Beispielpartie hinter einem TLS-terminierenden Proxy nicht auf http
// zeigt — sonst führte das gedruckte Blatt ins Leere.
func TestAushangFollowsForwardedProto(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/aushang", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Host = "goteach-prod.fly.dev"

	body := serve(t, req).Body.String()

	if !strings.Contains(body, "https://goteach-prod.fly.dev/partie") {
		t.Error("Verweis auf die Beispielpartie folgt nicht dem Proxy")
	}

	if strings.Contains(body, "http://goteach-prod.fly.dev") {
		t.Error("Blatt enthält noch eine http-Adresse der eigenen Instanz")
	}
}

// externalReference findet Verweise, die beim Anzeigen oder Drucken nachgeladen
// würden. Das Blatt muss ohne Netz vollständig sein: Es wird gedruckt, und
// ein fehlendes Bild fiele erst an der Wand auf.
var externalReference = regexp.MustCompile(
	`(?i)(src|@import)\s*=?\s*["']?https?://|url\(\s*["']?https?://|<(img|script|link)\b`)

func TestAushangLoadsNothingExternal(t *testing.T) {
	body := serve(t, httptest.NewRequest(http.MethodGet, "/aushang", nil)).Body.String()

	if m := externalReference.FindString(body); m != "" {
		t.Errorf("Aushang zieht etwas nach: %q", m)
	}
}

func TestAushangGETOnly(t *testing.T) {
	rr := serve(t, httptest.NewRequest(http.MethodPost, "/aushang", nil))

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, erwartet 405", rr.Code)
	}

	if allow := rr.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Errorf("Allow = %q", allow)
	}
}

// TestAushangWarnsWithoutEngine: Wer ein Blatt aufhängt, während der Dienst
// synthetisch rechnet, wirbt für eine Analyse, die keine ist. Das Blatt
// sagt das dann selbst.
func TestAushangWarnsWithoutEngine(t *testing.T) {
	withoutEngine := serve(t, httptest.NewRequest(http.MethodGet, "/aushang", nil)).Body.String()

	if !strings.Contains(withoutEngine, "Hinweis für den Aushang") {
		t.Error("ohne Engine fehlt der Hinweis auf synthetische Zahlen")
	}

	withEngine := serveEnv(t, httptest.NewRequest(http.MethodGet, "/aushang", nil),
		map[string]string{
			"KATAGO_REMOTE_URL":   "https://engine.example.com",
			"KATAGO_REMOTE_TOKEN": "geheim",
		}).Body.String()

	if strings.Contains(withEngine, "Hinweis für den Aushang") {
		t.Error("mit Engine steht der Hinweis trotzdem auf dem Blatt")
	}
}

func TestPartiePageServed(t *testing.T) {
	rr := serve(t, httptest.NewRequest(http.MethodGet, "/partie", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, erwartet 200", rr.Code)
	}

	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}

	body := rr.Body.String()

	for _, want := range []string{"const M = [", "flascheleer-berlin.de/analyse", "/aushang"} {
		if !strings.Contains(body, want) {
			t.Errorf("Partieseite ohne %q", want)
		}
	}
}

func TestSitemapListsAushangAndPartie(t *testing.T) {
	body := serve(t, httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil)).Body.String()

	for _, want := range []string{
		"<loc>http://example.com/aushang</loc>",
		"<loc>http://example.com/partie</loc>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("sitemap.xml ohne %s", want)
		}
	}
}

// aushangStone findet die Steine des gezeichneten Bretts.
var aushangStone = regexp.MustCompile(
	`<circle class="gb-stone gb-(black|white)" cx="(\d+)" cy="(\d+)"`)

// aushangMove ist der Zug, dessen Stellung das Blatt zeigt.
const aushangMove = 94

// TestAushangBrettZeigtDieStellung rechnet die gezeichnete Stellung aus der
// Zugliste der Beispielpartie nach. Ein Blatt, das mit nachgerechneten
// Zahlen wirbt, darf kein Brett zeigen, das die Partie nie hatte.
func TestAushangBrettZeigtDieStellung(t *testing.T) {
	moves, _ := loadPartie(t)
	b := board.New(13)

	for _, z := range moves[:aushangMove] {
		colour := board.White

		if z.Black {
			colour = board.Black
		}

		move := board.Move{Color: colour, Pass: z.Point == ""}

		if !move.Pass {
			p, _, err := board.FromGTP(z.Point, 13)

			if err != nil {
				t.Fatalf("Zug %d: %v", z.Number, err)
			}

			move.Point = p
		}

		if err := b.Play(move); err != nil {
			t.Fatalf("Zug %d: %v", z.Number, err)
		}
	}

	want := map[string]string{}

	for _, c := range []board.Color{board.Black, board.White} {
		name := "black"

		if c == board.White {
			name = "white"
		}

		for _, p := range b.Stones(c) {
			// Das SVG zählt die Schnittpunkte ab eins.
			want[fmt.Sprintf("%d/%d", p.X+1, p.Y+1)] = name
		}
	}

	body := serve(t, httptest.NewRequest(http.MethodGet, "/aushang", nil)).Body.String()
	got := map[string]string{}

	for _, m := range aushangStone.FindAllStringSubmatch(body, -1) {
		got[m[2]+"/"+m[3]] = m[1]
	}

	if len(got) != len(want) {
		t.Fatalf("%d gezeichnete Steine, nach Zug %d sind es %d",
			len(got), aushangMove, len(want))
	}

	for at, colour := range want {
		if got[at] != colour {
			t.Errorf("Punkt %s: gezeichnet %q, nachgerechnet %q", at, got[at], colour)
		}
	}
}

// TestAushangBrettUnterschrift prüft die Behauptung unter dem Brett: Zug 94
// ist Weiß G2, kostet 11,2 Punkte und ist der teuerste Zug der Partie.
func TestAushangBrettUnterschrift(t *testing.T) {
	moves, _ := loadPartie(t)
	z := moves[aushangMove-1]

	if z.Black || z.Point != "G2" {
		t.Errorf("Zug %d ist %v %s, die Unterschrift nennt Weiß G2",
			aushangMove, z.Black, z.Point)
	}

	if fmt.Sprintf("%.1f", math.Abs(z.Delta)) != "11.2" {
		t.Errorf("Zug %d kostet %.1f Punkte, die Unterschrift nennt 11,2",
			aushangMove, math.Abs(z.Delta))
	}

	for _, other := range moves {
		if other.Delta < z.Delta {
			t.Errorf("Zug %d kostet %.1f Punkte und damit mehr als Zug %d",
				other.Number, math.Abs(other.Delta), aushangMove)
		}
	}

	body := serve(t, httptest.NewRequest(http.MethodGet, "/aushang", nil)).Body.String()

	for _, want := range []string{"nach Zug 94", "Weiß G2", "11,2"} {
		if !strings.Contains(body, want) {
			t.Errorf("die Unterschrift nennt %q nicht mehr", want)
		}
	}
}
