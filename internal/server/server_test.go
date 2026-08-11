package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vmanke/goteach-prod/internal/auth"
	"github.com/vmanke/goteach-prod/shapes"
	"github.com/vmanke/goteach-prod/teaching"
)

const demoSGF = "(;GM[1]FF[4]SZ[19]KM[7.5]RU[Chinese]" +
	";B[pd];W[dp];B[pq];W[dd];B[qk];W[nc];B[pf];W[jd];B[cf];W[ch])"

// serveEnv schickt den Request durch den kompletten Router (inkl.
// Recovery). Engine-, Remote- und Auth-Umgebung werden neutralisiert;
// env überschreibt gezielt einzelne Variablen.
func serveEnv(t *testing.T, req *http.Request,
	env map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	base := map[string]string{
		"KATAGO_PATH":         "",
		"KATAGO_MODEL":        "",
		"KATAGO_REMOTE_URL":   "",
		"KATAGO_REMOTE_TOKEN": "",
		"KATAGO_ENGINE_TOKEN": "",
		"AUTH_USERS":          "",
		"AUTH_JWT_SECRET":     "",
		"AUTH_TOKEN_TTL":      "",
	}

	for k, v := range env {
		base[k] = v
	}

	for k, v := range base {
		t.Setenv(k, v)
	}

	rr := httptest.NewRecorder()
	Handler().ServeHTTP(rr, req)

	return rr
}

// serve ist serveEnv ohne Überschreibungen: offener Dienst, Mock-Engine.
func serve(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()

	return serveEnv(t, req, nil)
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

// testUserHash ist der Hash von "geheim" mit bewusst wenigen Iterationen
// (1000), damit die Tests schnell bleiben.
const testUserHash = "pbkdf2-sha256$1000$zkno905x1hUwGSVbn4Wh3Q$" +
	"7AfR3P7tExD8D8EGAiypD2tie_z7cXyX3vGxu_NTsfs"

// authEnv liefert die Umgebung einer Instanz mit aktivem Login.
func authEnv() map[string]string {
	return map[string]string{
		"AUTH_USERS":      "alice:" + testUserHash,
		"AUTH_JWT_SECRET": "test-secret",
	}
}

func loginBody(username, password string) *strings.Reader {
	body, _ := json.Marshal(map[string]string{
		"username": username, "password": password,
	})

	return strings.NewReader(string(body))
}

func TestLoginIssuesToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/login",
		loginBody("alice", "geheim"))

	rr := serveEnv(t, req, authEnv())

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, Body: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Token     string `json:"token"`
		TokenType string `json:"tokenType"`
		ExpiresAt string `json:"expiresAt"`
	}

	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Antwort kein JSON: %v", err)
	}

	if resp.Token == "" || resp.TokenType != "Bearer" || resp.ExpiresAt == "" {
		t.Errorf("Login-Antwort unvollständig: %+v", resp)
	}

	if _, err := auth.VerifyHS256([]byte("test-secret"),
		resp.Token, time.Now()); err != nil {
		t.Errorf("ausgestelltes Token ungültig: %v", err)
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	cases := map[string][2]string{
		"falsches Passwort": {"alice", "falsch"},
		"unbekannter User":  {"mallory", "geheim"},
	}

	for name, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/login",
			loginBody(c[0], c[1]))

		rr := serveEnv(t, req, authEnv())

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s: Status = %d, erwartet 401", name, rr.Code)
		}

		// Einheitliche Fehlermeldung: kein User-Enumeration.
		if !strings.Contains(rr.Body.String(),
			"Benutzername oder Passwort falsch") {
			t.Errorf("%s: uneinheitliche Fehlermeldung: %s", name, rr.Body.String())
		}
	}
}

func TestLoginBadRequests(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader("kein json"))

	if rr := serveEnv(t, req, authEnv()); rr.Code != http.StatusBadRequest {
		t.Errorf("kaputtes JSON: Status = %d, erwartet 400", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/login", nil)

	if rr := serveEnv(t, req, authEnv()); rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: Status = %d, erwartet 405", rr.Code)
	}
}

func TestLoginWithoutAuthConfigured(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/login",
		loginBody("alice", "geheim"))

	if rr := serve(t, req); rr.Code != http.StatusNotFound {
		t.Fatalf("Status = %d, erwartet 404", rr.Code)
	}
}

// AUTH_USERS ohne AUTH_JWT_SECRET ist Fehlkonfiguration: fail closed.
func TestAuthMisconfigured(t *testing.T) {
	env := map[string]string{"AUTH_USERS": "alice:" + testUserHash}

	req := httptest.NewRequest(http.MethodPost, "/analyze",
		strings.NewReader(demoSGF))

	if rr := serveEnv(t, req, env); rr.Code != http.StatusInternalServerError {
		t.Errorf("/analyze: Status = %d, erwartet 500", rr.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/login",
		loginBody("alice", "geheim"))

	if rr := serveEnv(t, req, env); rr.Code != http.StatusInternalServerError {
		t.Errorf("/login: Status = %d, erwartet 500", rr.Code)
	}
}

func TestAnalyzeRequiresAuth(t *testing.T) {
	// Ohne Token: 401 mit WWW-Authenticate.
	req := httptest.NewRequest(http.MethodPost, "/analyze",
		strings.NewReader(demoSGF))

	rr := serveEnv(t, req, authEnv())

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("ohne Token: Status = %d, erwartet 401", rr.Code)
	}

	if rr.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("WWW-Authenticate = %q, erwartet Bearer",
			rr.Header().Get("WWW-Authenticate"))
	}

	// Mit frischem Token aus /login: 200.
	loginReq := httptest.NewRequest(http.MethodPost, "/login",
		loginBody("alice", "geheim"))
	loginRR := serveEnv(t, loginReq, authEnv())

	var login struct {
		Token string `json:"token"`
	}

	if err := json.Unmarshal(loginRR.Body.Bytes(), &login); err != nil ||
		login.Token == "" {
		t.Fatalf("Login fehlgeschlagen: %s", loginRR.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/analyze",
		strings.NewReader(demoSGF))
	req.Header.Set("Authorization", "Bearer "+login.Token)

	rr = serveEnv(t, req, authEnv())

	if rr.Code != http.StatusOK {
		t.Fatalf("mit Token: Status = %d, Body: %s", rr.Code, rr.Body.String())
	}

	if resp := decodeAnalyze(t, rr); resp.Moves != 10 {
		t.Errorf("Moves = %d, erwartet 10", resp.Moves)
	}
}

// Ein gültig signiertes Token nützt nichts mehr, sobald der Benutzer aus
// AUTH_USERS entfernt wurde — Revocation wirkt sofort, nicht erst bei exp.
func TestAnalyzeRejectsRemovedUser(t *testing.T) {
	now := time.Now()
	token := auth.SignHS256([]byte("test-secret"), auth.Claims{
		Sub: "alice", Iss: "goteach",
		Iat: now.Unix(), Exp: now.Add(time.Hour).Unix(),
	})

	env := map[string]string{
		"AUTH_USERS":      "bob:" + testUserHash, // alice existiert nicht mehr
		"AUTH_JWT_SECRET": "test-secret",
	}

	req := httptest.NewRequest(http.MethodPost, "/analyze",
		strings.NewReader(demoSGF))
	req.Header.Set("Authorization", "Bearer "+token)

	if rr := serveEnv(t, req, env); rr.Code != http.StatusUnauthorized {
		t.Fatalf("Status = %d, erwartet 401", rr.Code)
	}
}

// Ein Token mit fremdem Aussteller wird abgelehnt, auch wenn Signatur,
// Ablauf und Benutzername stimmen.
func TestAnalyzeRejectsForeignIssuer(t *testing.T) {
	now := time.Now()
	token := auth.SignHS256([]byte("test-secret"), auth.Claims{
		Sub: "alice", Iss: "anderer-dienst",
		Iat: now.Unix(), Exp: now.Add(time.Hour).Unix(),
	})

	req := httptest.NewRequest(http.MethodPost, "/analyze",
		strings.NewReader(demoSGF))
	req.Header.Set("Authorization", "Bearer "+token)

	if rr := serveEnv(t, req, authEnv()); rr.Code != http.StatusUnauthorized {
		t.Fatalf("Status = %d, erwartet 401", rr.Code)
	}
}

func TestAnalyzeRejectsExpiredToken(t *testing.T) {
	now := time.Now()
	expired := auth.SignHS256([]byte("test-secret"), auth.Claims{
		Sub: "alice", Iss: "goteach",
		Iat: now.Add(-2 * time.Hour).Unix(),
		Exp: now.Add(-time.Hour).Unix(),
	})

	req := httptest.NewRequest(http.MethodPost, "/analyze",
		strings.NewReader(demoSGF))
	req.Header.Set("Authorization", "Bearer "+expired)

	if rr := serveEnv(t, req, authEnv()); rr.Code != http.StatusUnauthorized {
		t.Fatalf("Status = %d, erwartet 401", rr.Code)
	}
}

const engineQueryJSON = `{
  "request": {"size": 19, "komi": 7.5, "rules": "chinese",
    "moves": [["B","Q16"],["W","D4"]]},
  "turns": [0, 1, 2]
}`

func TestEnginePassthrough(t *testing.T) {
	env := map[string]string{"KATAGO_ENGINE_TOKEN": "tok"}

	// Ohne Token-Env existiert die Route nach außen nicht.
	req := httptest.NewRequest(http.MethodPost, "/engine/analyze",
		strings.NewReader(engineQueryJSON))

	if rr := serve(t, req); rr.Code != http.StatusNotFound {
		t.Errorf("unkonfiguriert: Status = %d, erwartet 404", rr.Code)
	}

	// Falsches Token: 401.
	req = httptest.NewRequest(http.MethodPost, "/engine/analyze",
		strings.NewReader(engineQueryJSON))
	req.Header.Set("Authorization", "Bearer falsch")

	if rr := serveEnv(t, req, env); rr.Code != http.StatusUnauthorized {
		t.Errorf("falsches Token: Status = %d, erwartet 401", rr.Code)
	}

	// Korrektes Token: Mock antwortet, synthetic true, ein Result pro Turn.
	req = httptest.NewRequest(http.MethodPost, "/engine/analyze",
		strings.NewReader(engineQueryJSON))
	req.Header.Set("Authorization", "Bearer tok")

	rr := serveEnv(t, req, env)

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, Body: %s", rr.Code, rr.Body.String())
	}

	var reply engineReply

	if err := json.Unmarshal(rr.Body.Bytes(), &reply); err != nil {
		t.Fatalf("Antwort kein JSON: %v", err)
	}

	if !reply.Synthetic || len(reply.Results) != 3 {
		t.Errorf("synthetic = %v, Results = %d, erwartet true/3",
			reply.Synthetic, len(reply.Results))
	}

	// Kaputte Anfragen: 400.
	for name, body := range map[string]string{
		"kein JSON":  "quatsch",
		"size fehlt": `{"request":{"komi":7.5},"turns":[0]}`,
		"turns leer": `{"request":{"size":19},"turns":[]}`,
	} {
		req = httptest.NewRequest(http.MethodPost, "/engine/analyze",
			strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok")

		if rr := serveEnv(t, req, env); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: Status = %d, erwartet 400", name, rr.Code)
		}
	}
}

// Zwei Instanzen im selben Prozess: der Stub spielt den Docker-Host mit
// Engine-Passthrough (lokaler Mock), die äußere Instanz delegiert per
// KATAGO_REMOTE_URL — das synthetic-Flag des Hosts muss durchkommen.
func TestAnalyzeViaRemoteEngine(t *testing.T) {
	stub := httptest.NewServer(Handler())
	defer stub.Close()

	env := map[string]string{
		"KATAGO_ENGINE_TOKEN": "tok",
		"KATAGO_REMOTE_URL":   stub.URL,
		"KATAGO_REMOTE_TOKEN": "tok",
	}

	req := httptest.NewRequest(http.MethodPost, "/analyze",
		strings.NewReader(demoSGF))

	rr := serveEnv(t, req, env)

	// Mit Engine-Host wird nicht mehr synchron gerechnet: Der Auftrag geht
	// hinüber, die Antwort ist sofort da und trägt nur die ID.
	if rr.Code != http.StatusAccepted {
		t.Fatalf("Status = %d, erwartet 202. Body: %s", rr.Code, rr.Body.String())
	}

	var accepted analyzeAcceptedReply

	if err := json.Unmarshal(rr.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("Antwort kein JSON: %v — Body: %s", err, rr.Body.String())
	}

	if accepted.JobID == "" || accepted.StatusURL == "" {
		t.Fatalf("unvollständige Annahme: %+v", accepted)
	}

	reply := pollJob(t, accepted.StatusURL, env)

	if reply.Status != jobDone {
		t.Fatalf("Status = %q, Fehler: %s", reply.Status, reply.Error)
	}

	resp := reply.Result

	if resp == nil {
		t.Fatal("fertiger Auftrag ohne Ergebnis")
	}

	if resp.Moves != 10 || len(resp.Reports) != 10 {
		t.Errorf("Moves = %d, Reports = %d, erwartet je 10",
			resp.Moves, len(resp.Reports))
	}

	if !resp.Synthetic {
		t.Error("synthetic-Flag des Engine-Hosts nicht durchgereicht")
	}
}

// pollJob fragt den Auftragsstatus ab, bis er fertig ist. Der Mock rechnet
// in Millisekunden; das Zeitlimit fängt nur einen hängenden Test ab.
func pollJob(t *testing.T, statusURL string,
	env map[string]string) jobStatusReply {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for {
		req := httptest.NewRequest(http.MethodGet, statusURL, nil)
		rr := serveEnv(t, req, env)

		if rr.Code != http.StatusOK {
			t.Fatalf("Status-Abfrage = %d, Body: %s", rr.Code, rr.Body.String())
		}

		var reply jobStatusReply

		if err := json.Unmarshal(rr.Body.Bytes(), &reply); err != nil {
			t.Fatalf("Status kein JSON: %v — Body: %s", err, rr.Body.String())
		}

		if reply.Status == jobDone || reply.Status == jobError {
			return reply
		}

		if time.Now().After(deadline) {
			t.Fatalf("Auftrag blieb %q", reply.Status)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// KATAGO_REMOTE_URL ohne KATAGO_REMOTE_TOKEN ist Fehlkonfiguration und
// scheitert mit klarer Ursache statt eines 401→502 beim Engine-Host.
func TestAnalyzeRemoteURLWithoutToken(t *testing.T) {
	env := map[string]string{"KATAGO_REMOTE_URL": "http://localhost:9"}

	req := httptest.NewRequest(http.MethodPost, "/analyze",
		strings.NewReader(demoSGF))

	rr := serveEnv(t, req, env)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("Status = %d, erwartet 502", rr.Code)
	}

	if !strings.Contains(rr.Body.String(), "KATAGO_REMOTE_TOKEN") {
		t.Errorf("Fehlermeldung nennt die Ursache nicht: %s", rr.Body.String())
	}
}

// Remote-Fehler sind Gateway-Fehler (502), keine internen Fehler.
func TestAnalyzeRemoteEngineDown(t *testing.T) {
	env := map[string]string{
		"KATAGO_REMOTE_URL":   "http://127.0.0.1:1",
		"KATAGO_REMOTE_TOKEN": "tok",
	}

	req := httptest.NewRequest(http.MethodPost, "/analyze",
		strings.NewReader(demoSGF))

	if rr := serveEnv(t, req, env); rr.Code != http.StatusBadGateway {
		t.Fatalf("Status = %d, erwartet 502", rr.Code)
	}
}

func TestAnalyzeLinesFormat(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/analyze?format=lines",
		strings.NewReader(demoSGF))

	rr := serve(t, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, Body: %s", rr.Code, rr.Body.String())
	}

	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, erwartet text/plain", ct)
	}

	records := strings.Split(rr.Body.String(), linesRecordSep)

	// Kopfsatz, Versionssatz, ein Satz je Zug (keine Vorgabesteine, der
	// Mock findet auf der Demo-Partie keine Stränge).
	if len(records) != 12 {
		t.Fatalf("Datensätze = %d, erwartet 12", len(records))
	}

	head := strings.Split(records[0], linesFieldSep)

	// Der Kopfsatz bleibt Satz 0 mit exakt 6 Feldern — Bestandsclients
	// destrukturieren ihn hart.
	if len(head) != 6 {
		t.Fatalf("Kopfsatz hat %d Felder, erwartet 6: %q", len(head), records[0])
	}

	if head[0] != "H" || head[1] != "19" || head[2] != "7.5" || head[5] != "true" {
		t.Errorf("Kopfsatz unerwartet: %q", records[0])
	}

	if records[1] != "V"+linesFieldSep+"2" {
		t.Errorf("Versionssatz unerwartet: %q", records[1])
	}

	first := strings.Split(records[2], linesFieldSep)

	// Auch der Zugsatz bleibt bei exakt 10 Feldern.
	if len(first) != 10 || first[0] != "M" || first[1] != "1" || first[2] != "Schwarz" {
		t.Errorf("erster Zugsatz unerwartet: %q", records[2])
	}

	// Der Lehrtext ist das letzte Feld und darf keine Steuerzeichen mehr
	// tragen, sonst zerfällt der Datensatz beim Splitten.
	if text := first[9]; text == "" || strings.ContainsAny(text, "\n\x1e\x1f") {
		t.Errorf("Lehrtext leer oder mit Steuerzeichen: %q", text)
	}
}

// Handicap-Partie: P-Sätze stehen zwischen V und dem ersten M, ein Satz
// je Vorgabestein, in SGF-Reihenfolge.
func TestAnalyzeLinesFormatSetupStones(t *testing.T) {
	sgf := "(;GM[1]FF[4]SZ[9]KM[0.5]AB[cc][gg];W[ee];B[cf])"
	req := httptest.NewRequest(http.MethodPost, "/analyze?format=lines",
		strings.NewReader(sgf))

	rr := serve(t, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, Body: %s", rr.Code, rr.Body.String())
	}

	records := strings.Split(rr.Body.String(), linesRecordSep)

	// H, V, 2×P, 2×M.
	if len(records) != 6 {
		t.Fatalf("Datensätze = %d, erwartet 6: %q", len(records), records)
	}

	wantP := [][2]string{{"Schwarz", "C7"}, {"Schwarz", "G3"}}

	for i, want := range wantP {
		fields := strings.Split(records[2+i], linesFieldSep)

		if len(fields) != 3 || fields[0] != "P" ||
			fields[1] != want[0] || fields[2] != want[1] {
			t.Errorf("P-Satz %d unerwartet: %q", i, records[2+i])
		}
	}

	if fields := strings.Split(records[4], linesFieldSep); fields[0] != "M" {
		t.Errorf("nach den P-Sätzen kein Zugsatz: %q", records[4])
	}
}

// Der S-Satz eines Erzählstrangs: 16 Felder, Zahlenfelder parsen, die
// Zugliste bleibt im Zugbereich der Partie.
func TestLinesBodyStrandRecord(t *testing.T) {
	resp := analyzeResponse{
		Size:  19,
		Komi:  6.5,
		Moves: 40,
		Strands: []teaching.Strand{{
			ID:       1,
			Area:     "obere Seite",
			FromMove: 12,
			ToMove:   31,
			Moves:    []int{12, 13, 17, 31},
			Shapes: []shapes.Instance{
				{Name: "leeres Dreieck"},
				{Name: "Leiter"},
				{Name: "leeres Dreieck"},
			},
			PointsLost: map[string]float64{"Schwarz": 4.2, "Weiß": 1.5},
			Captures:   3,
			Worst: &teaching.MoveRef{
				Number: 17, Player: "Schwarz", Coord: "Q14",
				PointsLost: 3.1, Category: "Fehler",
			},
			Text: "Der Kampf um die obere Seite\nkostete Schwarz am meisten.",
		}},
	}

	records := strings.Split(linesBody(resp), linesRecordSep)
	last := records[len(records)-1]
	fields := strings.Split(last, linesFieldSep)

	if len(fields) != 16 || fields[0] != "S" {
		t.Fatalf("S-Satz hat %d Felder, erwartet 16: %q", len(fields), last)
	}

	for _, idx := range []int{1, 3, 4, 5, 9} {
		if _, err := strconv.Atoi(fields[idx]); err != nil {
			t.Errorf("Feld %d ist keine Ganzzahl: %q", idx, fields[idx])
		}
	}

	for _, idx := range []int{7, 8, 13} {
		if _, err := strconv.ParseFloat(fields[idx], 64); err != nil {
			t.Errorf("Feld %d ist keine Zahl: %q", idx, fields[idx])
		}
	}

	for _, number := range strings.Split(fields[6], ",") {
		n, err := strconv.Atoi(number)

		if err != nil || n < 1 || n > resp.Moves {
			t.Errorf("Zugliste außerhalb der Partie: %q", fields[6])
		}
	}

	if fields[5] != "4" || fields[10] != "17" || fields[11] != "Q14" ||
		fields[12] != "Fehler" {
		t.Errorf("S-Satz-Felder unerwartet: %q", last)
	}

	// Formnamen dedupliziert, Text ohne Steuerzeichen.
	if fields[14] != "leeres Dreieck,Leiter" {
		t.Errorf("Formen = %q, erwartet dedupliziert", fields[14])
	}

	if strings.ContainsAny(fields[15], "\n\x1e\x1f") {
		t.Errorf("Strang-Text mit Steuerzeichen: %q", fields[15])
	}
}

func TestCORSAllowsTheClubSiteOnly(t *testing.T) {
	// Erlaubter Absender: Origin wird gespiegelt, Vary gesetzt.
	req := httptest.NewRequest(http.MethodPost, "/analyze",
		strings.NewReader(demoSGF))
	req.Header.Set("Origin", "https://flascheleer-berlin.de")

	rr := serve(t, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://flascheleer-berlin.de" {
		t.Errorf("Allow-Origin = %q, erwartet die Vereinsseite", got)
	}

	if !strings.Contains(strings.Join(rr.Header().Values("Vary"), ","), "Origin") {
		t.Error("Vary: Origin fehlt")
	}

	// Fremder Absender: keine CORS-Freigabe.
	req = httptest.NewRequest(http.MethodPost, "/analyze",
		strings.NewReader(demoSGF))
	req.Header.Set("Origin", "https://evil.example")

	rr = serve(t, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("fremder Origin wurde freigegeben: %q", got)
	}
}

func TestCORSPreflight(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/analyze", nil)
	req.Header.Set("Origin", "https://flascheleer-berlin.de")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)

	rr := serve(t, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("Preflight-Status = %d, erwartet 204", rr.Code)
	}

	if got := rr.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "POST") {
		t.Errorf("Allow-Methods = %q, POST fehlt", got)
	}
}

// Lange Rechnungen halten die Verbindung offen: nach dem Intervall geht
// Status 200 samt Füllzeichen hinaus, das Ergebnis kommt danach trotzdem
// vollständig an.
func TestComputeKeepingAliveHeartbeat(t *testing.T) {
	rr := httptest.NewRecorder()

	resp, _, started, err := computeKeepingAlive(rr, 5*time.Millisecond, "\n",
		func() (analyzeResponse, int, error) {
			time.Sleep(50 * time.Millisecond)

			return analyzeResponse{Size: 19}, http.StatusOK, nil
		})

	if err != nil {
		t.Fatalf("err = %v", err)
	}

	if !started {
		t.Fatal("Heartbeat hat die Antwort nicht begonnen")
	}

	if resp.Size != 19 {
		t.Fatalf("Size = %d, erwartet 19", resp.Size)
	}

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, erwartet 200", rr.Code)
	}

	if !strings.Contains(rr.Body.String(), "\n") {
		t.Fatal("kein Füllzeichen im Body")
	}

	if !rr.Flushed {
		t.Fatal("Füllzeichen wurden nicht geflusht")
	}
}

// Schnelle Ergebnisse — auch schnelle Fehler — bleiben unangetastet:
// nichts gesendet, der Statuscode aus compute steht dem Aufrufer noch
// als echter HTTP-Status zur Verfügung.
func TestComputeKeepingAliveFastPathWritesNothing(t *testing.T) {
	rr := httptest.NewRecorder()

	_, status, started, err := computeKeepingAlive(rr, time.Hour, "\n",
		func() (analyzeResponse, int, error) {
			return analyzeResponse{}, http.StatusBadGateway,
				errComputeFailed
		})

	if err == nil {
		t.Fatal("Fehler aus compute ging verloren")
	}

	if started {
		t.Fatal("Fast-Path hat die Antwort begonnen")
	}

	if status != http.StatusBadGateway {
		t.Fatalf("status = %d, erwartet 502", status)
	}

	if rr.Body.Len() != 0 {
		t.Fatal("Fast-Path hat Bytes geschrieben")
	}
}

var errComputeFailed = errors.New("kaputt")

// Eine Panic in der ausgelagerten Rechnung darf den Prozess nicht mehr
// umreißen: sie wird im Goroutine gefangen und kommt als Fehler zurück.
func TestComputeKeepingAliveSurvivesAPanic(t *testing.T) {
	rr := httptest.NewRecorder()

	_, status, _, err := computeKeepingAlive(rr, time.Hour, "\n",
		func() (analyzeResponse, int, error) {
			panic("kaputte Analyse")
		})

	if err == nil || !strings.Contains(err.Error(), "interner Fehler") {
		t.Fatalf("err = %v, erwartet interner Fehler", err)
	}

	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, erwartet 500", status)
	}
}

// NaN und ±Inf aus der Engine dürfen die JSON-Antwort nicht verhindern —
// encoding/json verweigert sie komplett, und der Client sähe nach den
// Heartbeat-Füllzeichen einen leeren Body ("unexpected end of data").
func TestNaNAndInfAreSanitizedOutOfTheResponse(t *testing.T) {
	resp := analyzeResponse{
		Size: 19,
		Komi: math.NaN(),
		Reports: []teaching.MoveReport{{
			PointsLost:    math.Inf(1),
			WinrateBefore: math.NaN(),
			WinrateAfter:  0.5,
		}},
		Strands: []teaching.Strand{{
			PointsLost: map[string]float64{"Schwarz": math.Inf(-1)},
		}},
	}

	clean := sanitizedResponse(resp)

	body, err := json.Marshal(clean)

	if err != nil {
		t.Fatalf("Marshal nach Bereinigung: %v", err)
	}

	if clean.Komi != 0 || clean.Reports[0].PointsLost != 0 ||
		clean.Reports[0].WinrateBefore != 0 ||
		clean.Strands[0].PointsLost["Schwarz"] != 0 {
		t.Fatalf("nicht bereinigt: %s", body)
	}

	if clean.Reports[0].WinrateAfter != 0.5 {
		t.Fatal("ein endlicher Wert wurde verändert")
	}
}
