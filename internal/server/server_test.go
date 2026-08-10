package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strings"
	"testing"
)

const demoSGF = "(;GM[1]FF[4]SZ[19]KM[7.5]RU[Chinese]" +
	";B[pd];W[dp];B[pq];W[dd];B[qk];W[nc];B[pf];W[jd];B[cf];W[ch])"

// serve schickt den Request durch den kompletten Router (inkl. Recovery).
func serve(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	t.Setenv("KATAGO_PATH", "")
	t.Setenv("KATAGO_MODEL", "")

	rr := httptest.NewRecorder()
	Handler().ServeHTTP(rr, req)

	return rr
}

func TestHomeHTMLForBrowsers(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept",
		"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	rr := serve(t, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, erwartet 200", rr.Code)
	}

	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, erwartet text/html", ct)
	}

	body := rr.Body.String()

	for _, want := range []string{
		"<title>goteach",
		`id="goteach-state"`,
		`property="og:title"`,
		`application/ld+json`,
		`rel="canonical" href="http://example.com/"`,
		`id="analyze-form"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("HTML enthält %q nicht", want)
		}
	}
}

func TestHomeCanonicalRespectsForwardedProto(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	req.Header.Set("X-Forwarded-Proto", "https")

	rr := serve(t, req)

	if !strings.Contains(rr.Body.String(),
		`rel="canonical" href="https://example.com/"`) {
		t.Error("Canonical nutzt X-Forwarded-Proto nicht")
	}
}

func TestHomeJSONForAPIClients(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	rr := serve(t, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, erwartet 200", rr.Code)
	}

	var info map[string]any

	if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil {
		t.Fatalf("keine JSON-Antwort: %v", err)
	}

	if info["service"] != "goteach" {
		t.Errorf("service = %v, erwartet goteach", info["service"])
	}
}

func TestHomeUnknownPath(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/nope", nil)

	if rr := serve(t, req); rr.Code != http.StatusNotFound {
		t.Fatalf("Status = %d, erwartet 404", rr.Code)
	}
}

func TestAPIInfo(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	req.Header.Set("Accept", "text/html")

	rr := serve(t, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, erwartet 200", rr.Code)
	}

	if !strings.Contains(rr.Body.String(), `"service": "goteach"`) {
		t.Error("/api liefert keine JSON-Dienstinfo")
	}
}

func decodeAnalyze(t *testing.T, rr *httptest.ResponseRecorder) analyzeResponse {
	t.Helper()

	var resp analyzeResponse

	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Antwort kein JSON: %v — Body: %s", err, rr.Body.String())
	}

	return resp
}

func TestAnalyzeRawBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/analyze",
		strings.NewReader(demoSGF))

	rr := serve(t, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, Body: %s", rr.Code, rr.Body.String())
	}

	resp := decodeAnalyze(t, rr)

	if !resp.Synthetic {
		t.Error("ohne Engine muss synthetic true sein")
	}

	if resp.Moves != 10 || len(resp.Reports) != 10 {
		t.Errorf("Moves = %d, Reports = %d, erwartet je 10",
			resp.Moves, len(resp.Reports))
	}
}

func TestAnalyzeMultipartUpload(t *testing.T) {
	var buf bytes.Buffer

	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("sgf", "partie.sgf")

	if err != nil {
		t.Fatal(err)
	}

	if _, err := fw.Write([]byte(demoSGF)); err != nil {
		t.Fatal(err)
	}

	_ = mw.WriteField("from", "1")
	_ = mw.WriteField("to", "3")
	_ = mw.WriteField("download", "1")
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/analyze", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	rr := serve(t, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, Body: %s", rr.Code, rr.Body.String())
	}

	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, erwartet attachment (download=1)", cd)
	}

	if resp := decodeAnalyze(t, rr); len(resp.Reports) != 3 {
		t.Errorf("Reports = %d, erwartet 3 (from=1, to=3)", len(resp.Reports))
	}
}

// Browser senden für ein leeres File-Input einen Part mit leerem Dateinamen;
// das Textfeld gleichen Namens muss trotzdem gefunden werden.
func TestAnalyzeMultipartTextFallback(t *testing.T) {
	var buf bytes.Buffer

	mw := multipart.NewWriter(&buf)

	head := textproto.MIMEHeader{}
	head.Set("Content-Disposition", `form-data; name="sgf"; filename=""`)
	head.Set("Content-Type", "application/octet-stream")

	if _, err := mw.CreatePart(head); err != nil {
		t.Fatal(err)
	}

	_ = mw.WriteField("sgf", demoSGF)
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/analyze", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	rr := serve(t, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, Body: %s", rr.Code, rr.Body.String())
	}

	if resp := decodeAnalyze(t, rr); resp.Moves != 10 {
		t.Errorf("Moves = %d, erwartet 10", resp.Moves)
	}
}

func TestAnalyzeFormURLEncoded(t *testing.T) {
	form := url.Values{"sgf": {demoSGF}, "visits": {"10"}}
	req := httptest.NewRequest(http.MethodPost, "/analyze",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rr := serve(t, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, Body: %s", rr.Code, rr.Body.String())
	}

	if resp := decodeAnalyze(t, rr); resp.Moves != 10 {
		t.Errorf("Moves = %d, erwartet 10", resp.Moves)
	}
}

// curl -d/--data-binary setzt application/x-www-form-urlencoded als
// Default; ein roher SGF-Body muss trotzdem funktionieren.
func TestAnalyzeRawBodyWithCurlDefaultContentType(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/analyze",
		strings.NewReader(demoSGF))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rr := serve(t, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, Body: %s", rr.Code, rr.Body.String())
	}

	if resp := decodeAnalyze(t, rr); resp.Moves != 10 {
		t.Errorf("Moves = %d, erwartet 10", resp.Moves)
	}
}

func TestAnalyzeDownloadViaQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/analyze?download=1",
		strings.NewReader(demoSGF))

	rr := serve(t, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, Body: %s", rr.Code, rr.Body.String())
	}

	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, "goteach-report.json") {
		t.Errorf("Content-Disposition = %q, erwartet Dateiname", cd)
	}
}

func TestAnalyzeEmptyBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/analyze", nil)

	if rr := serve(t, req); rr.Code != http.StatusBadRequest {
		t.Fatalf("Status = %d, erwartet 400", rr.Code)
	}
}

func TestAnalyzeTooLarge(t *testing.T) {
	big := strings.Repeat("x", maxSGFBytes+10)
	req := httptest.NewRequest(http.MethodPost, "/analyze",
		strings.NewReader(big))

	if rr := serve(t, req); rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("Status = %d, erwartet 413", rr.Code)
	}
}

func TestAnalyzeInvalidVisits(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/analyze?visits=abc",
		strings.NewReader(demoSGF))

	if rr := serve(t, req); rr.Code != http.StatusBadRequest {
		t.Fatalf("Status = %d, erwartet 400", rr.Code)
	}
}

func TestParseOGSGameID(t *testing.T) {
	valid := map[string]string{
		"12345678":                               "12345678",
		"https://online-go.com/game/12345678":    "12345678",
		"http://www.online-go.com/game/view/999": "999",
		"online-go.com/game/42":                  "42",
		"https://online-go.com/game/77?move=12":  "77",
		"  https://online-go.com/game/1234/  ":   "1234",
	}

	for ref, want := range valid {
		got, err := parseOGSGameID(ref)

		if err != nil || got != want {
			t.Errorf("parseOGSGameID(%q) = %q, %v — erwartet %q", ref, got, err, want)
		}
	}

	invalid := []string{
		"", "abc", "https://evil.example/game/1",
		"https://online-go.com/review/123", "online-go.com.evil.example/game/1",
	}

	for _, ref := range invalid {
		if got, err := parseOGSGameID(ref); err == nil {
			t.Errorf("parseOGSGameID(%q) = %q, erwartet Fehler", ref, got)
		}
	}
}

func TestAnalyzeFromOGS(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/games/123/sgf" {
				http.NotFound(w, r)

				return
			}

			_, _ = w.Write([]byte(demoSGF))
		}))
	defer stub.Close()

	orig := ogsBaseURL
	ogsBaseURL = stub.URL

	defer func() { ogsBaseURL = orig }()

	req := httptest.NewRequest(http.MethodPost,
		"/analyze?ogs="+url.QueryEscape("https://online-go.com/game/123"), nil)

	rr := serve(t, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, Body: %s", rr.Code, rr.Body.String())
	}

	if resp := decodeAnalyze(t, rr); resp.Moves != 10 {
		t.Errorf("Moves = %d, erwartet 10", resp.Moves)
	}

	// Unbekannte Partie → 502 mit OGS-Hinweis.
	req = httptest.NewRequest(http.MethodPost, "/analyze?ogs=999", nil)

	if rr := serve(t, req); rr.Code != http.StatusBadGateway {
		t.Errorf("unbekannte Partie: Status = %d, erwartet 502", rr.Code)
	}

	// Kaputte Referenz → 400.
	req = httptest.NewRequest(http.MethodPost, "/analyze?ogs=nonsense", nil)

	if rr := serve(t, req); rr.Code != http.StatusBadRequest {
		t.Errorf("kaputte Referenz: Status = %d, erwartet 400", rr.Code)
	}
}

func TestValuesGetterQueryWins(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/analyze?rules=&visits=10", nil)
	get := valuesGetter(req, url.Values{
		"rules":  {"chinese"},
		"visits": {"99"},
		"komi":   {"6.5"},
	})

	if got := get("rules"); got != "" {
		t.Errorf("rules = %q, leerer Query-Wert muss Formularwert überstimmen", got)
	}

	if got := get("visits"); got != "10" {
		t.Errorf("visits = %q, Query muss gewinnen", got)
	}

	if got := get("komi"); got != "6.5" {
		t.Errorf("komi = %q, ohne Query-Schlüssel gilt das Formular", got)
	}
}

func TestSEOEndpointsMethodNotAllowed(t *testing.T) {
	for _, path := range []string{"/robots.txt", "/sitemap.xml"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rr := serve(t, req)

		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: Status = %d, erwartet 405", path, rr.Code)
		}

		if allow := rr.Header().Get("Allow"); allow != "GET, HEAD" {
			t.Errorf("%s: Allow = %q, erwartet \"GET, HEAD\"", path, allow)
		}
	}
}

func TestRobotsAndSitemap(t *testing.T) {
	rr := serve(t, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))

	if !strings.Contains(rr.Body.String(),
		"Sitemap: http://example.com/sitemap.xml") {
		t.Errorf("robots.txt ohne Sitemap-Zeile: %s", rr.Body.String())
	}

	rr = serve(t, httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil))

	if !strings.Contains(rr.Body.String(), "<loc>http://example.com/</loc>") {
		t.Errorf("sitemap.xml ohne loc-Eintrag: %s", rr.Body.String())
	}
}

func TestStaticAssets(t *testing.T) {
	cases := []struct{ path, contentType, marker string }{
		{"/app.js", "text/javascript", "goteach-state"},
		{"/style.css", "text/css", "--bg"},
		{"/favicon.svg", "image/svg+xml", "<svg"},
	}

	for _, tc := range cases {
		rr := serve(t, httptest.NewRequest(http.MethodGet, tc.path, nil))

		if rr.Code != http.StatusOK {
			t.Errorf("%s: Status = %d", tc.path, rr.Code)

			continue
		}

		if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, tc.contentType) {
			t.Errorf("%s: Content-Type = %q", tc.path, ct)
		}

		if !strings.Contains(rr.Body.String(), tc.marker) {
			t.Errorf("%s: Inhalt ohne %q", tc.path, tc.marker)
		}
	}
}

func TestHealthz(t *testing.T) {
	rr := serve(t, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rr.Code != http.StatusOK || rr.Body.String() != "ok\n" {
		t.Fatalf("healthz: Status = %d, Body = %q", rr.Code, rr.Body.String())
	}
}

func TestAnalyzeBremstNachDemStossAus(t *testing.T) {
	// Ein Lauf bindet die Engine minutenlang; ohne Begrenzung genügen ein
	// paar Anfragen, um den Dienst lahmzulegen. Deshalb hier *ein* Handler
	// über mehrere Anfragen — der Zustand des Begrenzers ist der Prüfpunkt.
	t.Setenv("KATAGO_PATH", "")
	t.Setenv("KATAGO_MODEL", "")

	handler := Handler()

	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/analyze",
			strings.NewReader(demoSGF))
		req.RemoteAddr = "203.0.113.9:4444"

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		return rr
	}

	for i := 0; i < 3; i++ {
		if rr := post(); rr.Code != http.StatusOK {
			t.Fatalf("Anfrage %d: Status %d, Body: %s", i+1, rr.Code, rr.Body.String())
		}
	}

	rr := post()

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("Status %d, erwartet 429", rr.Code)
	}

	if rr.Header().Get("Retry-After") == "" {
		t.Error("Retry-After fehlt — der Client weiß sonst nicht, wann er darf")
	}

	if !strings.Contains(rr.Body.String(), "zu viele Anfragen") {
		t.Errorf("unerwarteter Body: %s", rr.Body.String())
	}
}

func TestBegrenzungTrenntDieClients(t *testing.T) {
	t.Setenv("KATAGO_PATH", "")
	t.Setenv("KATAGO_MODEL", "")

	handler := Handler()

	post := func(addr string) int {
		req := httptest.NewRequest(http.MethodPost, "/analyze",
			strings.NewReader(demoSGF))
		req.RemoteAddr = addr

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		return rr.Code
	}

	for i := 0; i < 4; i++ {
		post("203.0.113.9:4444")
	}

	if code := post("198.51.100.4:4444"); code != http.StatusOK {
		t.Fatalf("zweiter Client bekam %d — die Sperre gilt pro Client", code)
	}
}

func TestForwardedForZaehltNurMitVertrauen(t *testing.T) {
	// Ohne gesetztes GOTEACH_TRUST_PROXY darf ein selbstgesetzter Header die
	// Begrenzung nicht aushebeln.
	t.Setenv("KATAGO_PATH", "")
	t.Setenv("KATAGO_MODEL", "")
	t.Setenv("GOTEACH_TRUST_PROXY", "")

	handler := Handler()

	post := func(forwarded string) int {
		req := httptest.NewRequest(http.MethodPost, "/analyze",
			strings.NewReader(demoSGF))
		req.RemoteAddr = "203.0.113.9:4444"
		req.Header.Set("X-Forwarded-For", forwarded)

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		return rr.Code
	}

	for i := 0; i < 3; i++ {
		if code := post("10.0.0.1"); code != http.StatusOK {
			t.Fatalf("Anfrage %d: Status %d", i+1, code)
		}
	}

	// Neue erfundene Adresse, gleiche Peer-Adresse: muss trotzdem greifen.
	if code := post("10.0.0.99"); code != http.StatusTooManyRequests {
		t.Fatalf("Status %d, erwartet 429 — Header wurde geglaubt", code)
	}
}
