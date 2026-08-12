package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
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

// pollEngineJob fragt den Auftrag auf dem Engine-Host ab, bis er fertig
// ist. Deadline statt fester Rundenzahl, mit kurzer Pause dazwischen: Eine
// Zählschleife ohne Schlaf verbrennt CPU und wäre obendrein flaky — sie
// gibt auf, wenn dem Worker nur Millisekunden fehlen.
func pollEngineJob(t *testing.T, id string) jobStatusReply {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for {
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

		if time.Now().After(deadline) {
			t.Fatalf("Auftrag blieb %q", reply.Status)
		}

		time.Sleep(10 * time.Millisecond)
	}
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

// Kennt der Engine-Host die Auftragsroute nicht (älterer Stand oder
// fehlendes KATAGO_ENGINE_TOKEN), muss die Meldung das benennen — ein
// blankes "HTTP 404" schickt den Betreiber auf die falsche Fährte.
func TestSubmitJobReportsVersionSkew(t *testing.T) {
	// Ein Host von vor dem Auftragsbetrieb: die Route gibt es dort nicht.
	stub := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}))
	defer stub.Close()

	env := map[string]string{
		"KATAGO_REMOTE_URL":   stub.URL,
		"KATAGO_REMOTE_TOKEN": "tok",
	}

	req := httptest.NewRequest(http.MethodPost, "/analyze",
		strings.NewReader(demoSGF))

	rr := serveEnv(t, req, env)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("Status = %d, erwartet 502", rr.Code)
	}

	body := rr.Body.String()

	for _, want := range []string{"Auftragsbetrieb", "deployen", "KATAGO_ENGINE_TOKEN"} {
		if !strings.Contains(body, want) {
			t.Errorf("Meldung ohne %q: %s", want, body)
		}
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

// Fertige Aufträge dürfen keine neuen blockieren. Vorher zählte add die
// gesamte Map, sodass nach maxLiveJobs Analysen eine halbe Stunde lang
// jeder neue Auftrag 429 bekam — obwohl die Maschine längst frei war.
func TestFinishedJobsDoNotBlockNewOnes(t *testing.T) {
	store := newJobStore()

	// Mehr fertige Aufträge ablegen, als die Warteschlange zulässt.
	for i := 0; i < maxLiveJobs+5; i++ {
		j := &job{
			ID:      "fertig-" + strconv.Itoa(i),
			Status:  jobDone,
			Created: time.Now(),
			Done:    time.Now(),
		}

		if !store.add(j) {
			t.Fatalf("fertiger Auftrag %d wurde abgewiesen", i)
		}
	}

	if !store.add(&job{ID: "neu", Status: jobPending, Created: time.Now()}) {
		t.Fatal("neuer Auftrag abgewiesen, obwohl nur fertige vorliegen")
	}
}

// Offene Aufträge zählen dagegen sehr wohl: Über der Grenze wird
// abgewiesen, statt eine Schlange anzunehmen, die niemand mehr abholt.
func TestOpenJobsHitTheLimit(t *testing.T) {
	store := newJobStore()

	for i := 0; i < maxLiveJobs; i++ {
		j := &job{
			ID:      "offen-" + strconv.Itoa(i),
			Status:  jobPending,
			Created: time.Now(),
		}

		if !store.add(j) {
			t.Fatalf("offener Auftrag %d unter der Grenze abgewiesen", i)
		}
	}

	if store.add(&job{ID: "zuviel", Status: jobPending, Created: time.Now()}) {
		t.Fatalf("Auftrag %d+1 wurde angenommen, erwartet Abweisung", maxLiveJobs)
	}
}

// Der Speicher bleibt trotzdem begrenzt: Über maxRetainedJobs fliegen die
// ältesten FERTIGEN Aufträge hinaus.
func TestRetentionTrimsOldestFinished(t *testing.T) {
	store := newJobStore()
	base := time.Now().Add(-time.Hour)

	for i := 0; i < maxRetainedJobs+10; i++ {
		// Done aufsteigend, damit "ältester" eindeutig ist; TTL-Eviction
		// greift nicht, weil evictLocked auf jobTTL prüft.
		store.add(&job{
			ID:      "j-" + strconv.Itoa(i),
			Status:  jobDone,
			Created: base,
			Done:    time.Now().Add(time.Duration(i) * time.Millisecond),
		})
	}

	if len(store.jobs) > maxRetainedJobs {
		t.Fatalf("%d Aufträge vorgehalten, Grenze ist %d",
			len(store.jobs), maxRetainedJobs)
	}

	// Der älteste muss weg sein, ein junger noch da.
	if _, ok := store.jobs["j-0"]; ok {
		t.Error("ältester fertiger Auftrag wurde nicht verworfen")
	}

	last := "j-" + strconv.Itoa(maxRetainedJobs+9)

	if _, ok := store.jobs[last]; !ok {
		t.Errorf("jüngster Auftrag %q fehlt", last)
	}
}
