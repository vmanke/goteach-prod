package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// engineEnv ist die Umgebung eines Engine-Hosts (rechnet selbst, hier mit
// dem Mock, und nimmt Aufträge entgegen).
func engineEnv() map[string]string {
	return map[string]string{"KATAGO_ENGINE_TOKEN": "tok"}
}

// postJob legt einen Auftrag direkt auf dem Engine-Host an.
func postJob(t *testing.T, body string, token string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/engine/jobs",
		strings.NewReader(body))

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	return serveEnv(t, req, engineEnv())
}

// Ohne KATAGO_ENGINE_TOKEN existiert die Route nach außen nicht.
func TestEngineJobsWithoutTokenIsHidden(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/engine/jobs",
		strings.NewReader(`{"sgf":"x"}`))

	rr := serveEnv(t, req, map[string]string{})

	if rr.Code != http.StatusNotFound {
		t.Fatalf("Status = %d, erwartet 404", rr.Code)
	}
}

// Falsches Token → 401, unabhängig vom Body.
func TestEngineJobsWrongToken(t *testing.T) {
	rr := postJob(t, `{"sgf":"`+demoSGF+`"}`, "falsch")

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("Status = %d, erwartet 401", rr.Code)
	}
}

// Der Auftrag durchläuft pending/running bis done und liefert den Report.
func TestEngineJobLifecycle(t *testing.T) {
	rr := postJob(t, `{"sgf":"`+demoSGF+`","params":{"visits":"1"}}`, "tok")

	if rr.Code != http.StatusAccepted {
		t.Fatalf("Status = %d, erwartet 202. Body: %s", rr.Code, rr.Body.String())
	}

	var created jobStatusReply

	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("Antwort kein JSON: %v", err)
	}

	if created.ID == "" {
		t.Fatal("keine Auftrags-ID")
	}

	if created.Status != jobPending {
		t.Errorf("Status = %q, erwartet %q", created.Status, jobPending)
	}

	reply := pollEngineJob(t, created.ID)

	if reply.Status != jobDone {
		t.Fatalf("Status = %q, Fehler: %s", reply.Status, reply.Error)
	}

	if reply.Result == nil || len(reply.Result.Reports) != 10 {
		t.Fatalf("Report unvollständig: %+v", reply.Result)
	}

	// Der Mock rechnet — das muss bis zum Ergebnis durchschlagen, sonst
	// sähen synthetische Zahlen wie echte Analyse aus.
	if !reply.Result.Synthetic {
		t.Error("synthetic-Flag fehlt, obwohl der Mock gerechnet hat")
	}
}

// pollEngineJob fragt den Auftrag auf dem Engine-Host ab, bis er fertig ist.
func pollEngineJob(t *testing.T, id string) jobStatusReply {
	t.Helper()

	for i := 0; i < 1000; i++ {
		req := httptest.NewRequest(http.MethodGet, "/engine/jobs?id="+id, nil)
		req.Header.Set("Authorization", "Bearer tok")

		rr := serveEnv(t, req, engineEnv())

		if rr.Code != http.StatusOK {
			t.Fatalf("Abfrage = %d, Body: %s", rr.Code, rr.Body.String())
		}

		var reply jobStatusReply

		if err := json.Unmarshal(rr.Body.Bytes(), &reply); err != nil {
			t.Fatalf("Status kein JSON: %v", err)
		}

		if reply.Status == jobDone || reply.Status == jobError {
			return reply
		}
	}

	t.Fatal("Auftrag wurde nicht fertig")

	return jobStatusReply{}
}

// Unbekannte ID → 404 mit Hinweis, statt stiller leerer Antwort.
func TestEngineJobUnknownID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/engine/jobs?id=gibtsnicht", nil)
	req.Header.Set("Authorization", "Bearer tok")

	rr := serveEnv(t, req, engineEnv())

	if rr.Code != http.StatusNotFound {
		t.Fatalf("Status = %d, erwartet 404", rr.Code)
	}

	if !strings.Contains(rr.Body.String(), "unbekannt") {
		t.Errorf("Meldung ohne Hinweis: %s", rr.Body.String())
	}
}

// Fehlende ID → 400.
func TestEngineJobMissingID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/engine/jobs", nil)
	req.Header.Set("Authorization", "Bearer tok")

	rr := serveEnv(t, req, engineEnv())

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("Status = %d, erwartet 400", rr.Code)
	}
}

// Kaputtes SGF wird beim Anlegen abgewiesen, nicht erst im Worker — sonst
// erführe der Aufrufer den Eingabefehler erst nach dem Polling.
func TestEngineJobRejectsBadSGF(t *testing.T) {
	rr := postJob(t, `{"sgf":"kein sgf"}`, "tok")

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("Status = %d, erwartet 400. Body: %s", rr.Code, rr.Body.String())
	}
}

// Ungültige Parameter ebenfalls sofort.
func TestEngineJobRejectsBadParams(t *testing.T) {
	rr := postJob(t, `{"sgf":"`+demoSGF+`","params":{"visits":"viele"}}`, "tok")

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("Status = %d, erwartet 400. Body: %s", rr.Code, rr.Body.String())
	}
}

// Ohne Engine-Host gibt es keinen Auftragsbetrieb: /analyze rechnet
// weiterhin selbst und /analyze/status existiert nicht. Das ist der
// Regressionsschutz für lokale Nutzung, CLI und Mock.
func TestAnalyzeStatusWithoutRemote(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/analyze/status?id=egal", nil)
	rr := serveEnv(t, req, map[string]string{})

	if rr.Code != http.StatusNotFound {
		t.Fatalf("Status = %d, erwartet 404", rr.Code)
	}
}

// Ohne id-Parameter meldet die Statusroute das, statt den Engine-Host zu
// fragen.
func TestAnalyzeStatusMissingID(t *testing.T) {
	stub := httptest.NewServer(Handler())
	defer stub.Close()

	env := map[string]string{
		"KATAGO_ENGINE_TOKEN": "tok",
		"KATAGO_REMOTE_URL":   stub.URL,
		"KATAGO_REMOTE_TOKEN": "tok",
	}

	req := httptest.NewRequest(http.MethodGet, "/analyze/status", nil)
	rr := serveEnv(t, req, env)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("Status = %d, erwartet 400", rr.Code)
	}
}

// Ohne JavaScript liefert die Statusroute eine Seite, die sich selbst
// nachlädt, statt JSON.
func TestAnalyzeStatusHTMLPage(t *testing.T) {
	stub := httptest.NewServer(Handler())
	defer stub.Close()

	env := map[string]string{
		"KATAGO_ENGINE_TOKEN": "tok",
		"KATAGO_REMOTE_URL":   stub.URL,
		"KATAGO_REMOTE_TOKEN": "tok",
	}

	post := httptest.NewRequest(http.MethodPost, "/analyze",
		strings.NewReader(demoSGF))

	rr := serveEnv(t, post, env)

	var accepted analyzeAcceptedReply

	if err := json.Unmarshal(rr.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("Annahme kein JSON: %v — Body: %s", err, rr.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, accepted.StatusURL, nil)
	req.Header.Set("Accept", "text/html")

	page := serveEnv(t, req, env)

	if page.Code != http.StatusOK {
		t.Fatalf("Status = %d, Body: %s", page.Code, page.Body.String())
	}

	if ct := page.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, erwartet text/html", ct)
	}

	body := page.Body.String()

	// Entweder läuft der Auftrag noch (dann muss die Seite sich selbst
	// nachladen) oder er ist fertig (dann steht das Ergebnis da).
	if !strings.Contains(body, "http-equiv=\"refresh\"") &&
		!strings.Contains(body, "Analyse fertig") {
		t.Fatalf("Seite lädt weder nach noch zeigt sie ein Ergebnis:\n%s", body)
	}
}
