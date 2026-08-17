package server

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
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
	if n := strings.Count(body, "<svg"); n != 2 {
		t.Errorf("%d QR-Codes im Blatt, erwartet 2", n)
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
