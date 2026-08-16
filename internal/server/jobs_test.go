package server

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vmanke/goteach-prod/teaching"
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

// Eine Panic in der Rechen-Goroutine risse in Go den ganzen Prozess mit
// allen offenen Verbindungen um — withRecover sitzt am Handler und sieht
// eine fremde Goroutine nicht. Der Auftrag muss stattdessen als Fehler
// enden und der Dienst weiterlaufen.
//
// Dass dieser Test überhaupt zurückkehrt, ist bereits die halbe Aussage:
// ohne das recover stürbe der Testprozess hier.
func TestAJobSurvivesAPanicInTheAnalysis(t *testing.T) {
	store := newJobStore()
	id := "test"

	store.jobs[id] = &job{ID: id, Status: jobPending, Created: time.Now()}

	done := make(chan struct{})

	go func() {
		defer close(done)

		store.run(id, nil, teaching.Options{})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Auftrag kam nicht zurück")
	}

	j, ok := store.get(id)

	if !ok {
		t.Fatal("Auftrag verschwunden")
	}

	if j.Status != jobError {
		t.Errorf("Status = %q, erwartet %q", j.Status, jobError)
	}

	if j.Err == "" {
		t.Error("Fehlschlag ohne Begründung")
	}

	// Der Rechenplatz muss zurückgegeben sein, sonst blockiert die Panic
	// jeden weiteren Auftrag für immer.
	select {
	case store.slot <- struct{}{}:
		<-store.slot
	default:
		t.Error("Rechenplatz nach der Panic nicht freigegeben")
	}
}

// Das Neue am Auftrag: sein Feed ist lesbar, WÄHREND gerechnet wird. Ohne
// das müsste der Auftragsbetrieb zwischen Fortschrittsanzeige und
// Wiederaufnahme wählen — hier steht, dass er das nicht muss.
//
// Am Register statt über HTTP, weil die Mock-Engine zu schnell ist, um
// einen Zwischenstand verlässlich zu treffen.
func TestAJobsFeedIsReadableWhileItGrows(t *testing.T) {
	store := newJobStore()
	id := "test"

	store.jobs[id] = &job{ID: id, Status: jobRunning, Created: time.Now()}

	// Der Zwischenstand reist in der Antwort mit, solange kein Ergebnis da
	// ist — das ist der Weg, den beide Betriebsarten nehmen.
	feedOf := func() string {
		j, ok := store.get(id)

		if !ok {
			t.Fatal("Auftrag verschwunden")
		}

		return jobReply(j).Feed
	}

	if got := feedOf(); got != "" {
		t.Fatalf("frischer Auftrag: Feed = %q", got)
	}

	store.appendRecord(id, linesRecord("H", "19", "7.5", "Japanisch", "180", "false"))

	half := feedOf()

	if !strings.HasPrefix(half, "H"+linesFieldSep) {
		t.Fatalf("nach dem Kopfsatz: Feed = %q", half)
	}

	store.appendRecord(id, linesRecord("M", "1", "Schwarz", "Q16"))

	if full := feedOf(); len(full) <= len(half) {
		t.Errorf("Feed wuchs nicht: %d → %d Bytes", len(half), len(full))
	}

	// Ist der Auftrag fertig, trägt die Antwort den Report statt des
	// Feeds — beides zu schicken verdoppelte sie.
	store.update(id, func(j *job) {
		j.Status = jobDone
		j.Done = time.Now()
	})

	if got := feedOf(); got != "" {
		t.Errorf("fertiger Auftrag schickt den Feed noch mit: %q", got)
	}
}

// Ohne Engine-Host bleibt /analyze synchron — das ist der
// Regressionsschutz für lokale Nutzung, CLI und Mock.
//
// Die Statusroute gibt es dort inzwischen trotzdem: Sie bedient die
// Aufträge, die diese Instanz auf Wunsch (mode=job) selbst annimmt. Eine
// unbekannte ID ist deshalb weiterhin 404, aber aus einem anderen Grund
// als früher — nicht "die Route existiert hier nicht", sondern "diesen
// Auftrag kennt niemand". Der Test hält beides auseinander.
func TestAnalyzeWithoutRemoteStaysSynchronous(t *testing.T) {
	rr := serveEnv(t, httptest.NewRequest(http.MethodPost, "/analyze",
		strings.NewReader(demoSGF)), map[string]string{})

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, erwartet 200 (synchron)", rr.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/analyze/status?id=egal", nil)
	rr = serveEnv(t, req, map[string]string{})

	if rr.Code != http.StatusNotFound {
		t.Fatalf("unbekannte ID: Status = %d, erwartet 404", rr.Code)
	}

	if !strings.Contains(rr.Body.String(), "unbekannt") {
		t.Errorf("404 ohne Grund im Body: %s", rr.Body.String())
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

// Ein nicht geleerter Body kostet die Verbindung: net/http legt sie nur
// zurück in den Pool, wenn sie zu Ende gelesen wurde. Der 404-Zweig in
// submitJob liest den Body nicht — ohne Leerlesen baut jeder Versuch eine
// neue TCP-Verbindung auf.
func TestSubmitJobReusesConnection(t *testing.T) {
	var mu sync.Mutex

	conns := map[net.Conn]bool{}

	stub := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			// Fehlerantwort mit Inhalt: nur so ist überhaupt etwas übrig,
			// das liegen bleiben könnte.
			http.Error(w, "404 page not found", http.StatusNotFound)
		}))

	stub.Config.ConnState = func(c net.Conn, s http.ConnState) {
		if s == http.StateNew {
			mu.Lock()
			conns[c] = true
			mu.Unlock()
		}
	}

	defer stub.Close()

	t.Setenv("KATAGO_REMOTE_URL", stub.URL)
	t.Setenv("KATAGO_REMOTE_TOKEN", "tok")

	for i := 0; i < 3; i++ {
		if _, err := submitJob(demoSGF, nil); err == nil {
			t.Fatal("404 muss einen Fehler liefern")
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if len(conns) != 1 {
		t.Errorf("%d TCP-Verbindungen für 3 Anfragen — Body bleibt liegen",
			len(conns))
	}
}
