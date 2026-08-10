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
	"time"

	"github.com/vmanke/goteach-prod/internal/auth"
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

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, Body: %s", rr.Code, rr.Body.String())
	}

	resp := decodeAnalyze(t, rr)

	if resp.Moves != 10 || len(resp.Reports) != 10 {
		t.Errorf("Moves = %d, Reports = %d, erwartet je 10",
			resp.Moves, len(resp.Reports))
	}

	if !resp.Synthetic {
		t.Error("synthetic-Flag des Engine-Hosts nicht durchgereicht")
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
