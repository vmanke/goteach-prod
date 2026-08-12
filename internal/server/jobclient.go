// Client-Seite des Auftragsbetriebs: Diese Instanz (eine ohne
// KataGo-Binary) reicht die Partie an den Engine-Host weiter und fragt
// später das Ergebnis ab. Beides sind kurze Aufrufe — die lange Rechnung
// bleibt drüben.
package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// jobClientTimeout deckelt Anlegen und Abfragen. Beides sind schnelle
// Aufrufe; der lange Timeout von katago.Remote hat hier nichts zu suchen.
const jobClientTimeout = 30 * time.Second

// jobParams sind die Query-Parameter, die den Auftrag beeinflussen. Sie
// werden unverändert an den Engine-Host durchgereicht, der sie mit
// demselben optionsFrom auswertet und dabei erneut deckelt.
var jobParams = []string{"visits", "tau", "from", "to", "rules", "komi"}

var jobHTTP = &http.Client{Timeout: jobClientTimeout}

// engineBase liefert die Basis-URL des Engine-Hosts ohne Schrägstrich.
func engineBase() string {
	return strings.TrimRight(os.Getenv("KATAGO_REMOTE_URL"), "/")
}

// collectJobParams sammelt die relevanten Parameter aus der Anfrage.
func collectJobParams(get func(string) string) map[string]string {
	params := map[string]string{}

	for _, k := range jobParams {
		if v := strings.TrimSpace(get(k)); v != "" {
			params[k] = v
		}
	}

	return params
}

// engineRequest baut eine authentifizierte Anfrage an den Engine-Host.
func engineRequest(method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, engineBase()+path, body)

	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization",
		"Bearer "+os.Getenv("KATAGO_REMOTE_TOKEN"))

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return req, nil
}

// maxDrainBytes deckelt das Leerlesen: Wer mehr schickt, verliert die
// Wiederverwendung — das ist billiger, als beliebig viel zu lesen.
const maxDrainBytes = 1 << 20

// closeBody gibt die Verbindung in den Pool zurück. net/http kann sie nur
// wiederverwenden, wenn der Body bis zum Ende gelesen wurde; die Aufrufer
// hier lesen ihn aber nur begrenzt (LimitReader) oder — im Fall eines
// erkannten Fehlerstatus — gar nicht.
func closeBody(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))
	_ = resp.Body.Close()
}

// engineErrorText liest die Fehlermeldung einer Nicht-200-Antwort.
func engineErrorText(resp *http.Response) string {
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))

	var e struct {
		Error string `json:"error"`
	}

	if json.Unmarshal(msg, &e) == nil && e.Error != "" {
		return e.Error
	}

	return fmt.Sprintf("HTTP %d", resp.StatusCode)
}

// submitJob legt den Auftrag auf dem Engine-Host an und liefert dessen ID.
func submitJob(sgf string, params map[string]string) (string, error) {
	buf, err := json.Marshal(jobRequest{SGF: sgf, Params: params})

	if err != nil {
		return "", err
	}

	req, err := engineRequest(http.MethodPost, "/engine/jobs",
		bytes.NewReader(buf))

	if err != nil {
		return "", err
	}

	resp, err := jobHTTP.Do(req)

	if err != nil {
		return "", err
	}

	defer closeBody(resp)

	// 404 heißt hier nicht "nichts gefunden", sondern: Der Engine-Host
	// kennt die Route nicht. Ohne diesen Hinweis liest der Betreiber bloß
	// "HTTP 404" und sucht am falschen Ende.
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf(
			"Engine-Host kennt den Auftragsbetrieb nicht (%s/engine/jobs): "+
				"entweder läuft dort ein älterer Stand — dann neu deployen — "+
				"oder KATAGO_ENGINE_TOKEN fehlt", engineBase())
	}

	if resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("%s", engineErrorText(resp))
	}

	var reply jobStatusReply

	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).
		Decode(&reply); err != nil {
		return "", fmt.Errorf("Antwort unlesbar: %w", err)
	}

	if reply.ID == "" {
		return "", fmt.Errorf("Engine-Host lieferte keine Auftrags-ID")
	}

	return reply.ID, nil
}

// fetchJob fragt Status und gegebenenfalls Ergebnis ab. Der zweite
// Rückgabewert ist der HTTP-Status, den diese Instanz weitergeben soll.
func fetchJob(id string) (jobStatusReply, int, error) {
	req, err := engineRequest(http.MethodGet,
		"/engine/jobs?id="+url.QueryEscape(id), nil)

	if err != nil {
		return jobStatusReply{}, http.StatusInternalServerError, err
	}

	resp, err := jobHTTP.Do(req)

	if err != nil {
		return jobStatusReply{}, http.StatusBadGateway, err
	}

	defer closeBody(resp)

	if resp.StatusCode == http.StatusNotFound {
		return jobStatusReply{}, http.StatusNotFound,
			fmt.Errorf("Auftrag unbekannt (abgelaufen oder Engine neu gestartet)")
	}

	if resp.StatusCode != http.StatusOK {
		return jobStatusReply{}, http.StatusBadGateway,
			fmt.Errorf("%s", engineErrorText(resp))
	}

	var reply jobStatusReply

	// Der fertige Report enthält Ownership-nahe Daten und wird groß;
	// dasselbe Limit wie beim Remote-Analyzer.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).
		Decode(&reply); err != nil {
		return jobStatusReply{}, http.StatusBadGateway,
			fmt.Errorf("Antwort unlesbar: %w", err)
	}

	return reply, http.StatusOK, nil
}

// analyzeStatusPath ist die Route, über die der Client sein Ergebnis holt.
const analyzeStatusPath = "/analyze/status"

// analyzeAcceptedReply ist die 202-Antwort von POST /analyze im
// Auftragsbetrieb.
type analyzeAcceptedReply struct {
	JobID     string `json:"jobId"`
	Status    string `json:"status"`
	StatusURL string `json:"statusUrl"`
	Hint      string `json:"hint"`
}

// wantsHTML meldet, ob der Aufrufer eine Seite statt JSON erwartet — das
// ist der Formular-Post ohne JavaScript.
func wantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// submitAnalyzeJob reicht die Partie an den Engine-Host weiter und
// antwortet sofort mit der Auftrags-ID.
func submitAnalyzeJob(w http.ResponseWriter, r *http.Request,
	sgf string, get func(string) string) {

	// Derselbe doppelte Boden wie in startAnalyzer: Fehlkonfiguration soll
	// die Ursache nennen, statt als Verbindungsfehler oder 401 zu erscheinen.
	if err := validateRemoteEnv(); err != nil {
		httpError(w, http.StatusBadGateway, "%v", err)

		return
	}

	id, err := submitJob(sgf, collectJobParams(get))

	if err != nil {
		httpError(w, http.StatusBadGateway, "Auftrag anlegen: %v", err)

		return
	}

	statusURL := analyzeStatusPath + "?id=" + url.QueryEscape(id)

	// Ohne JavaScript trägt die Seite selbst das Warten: Sie lädt sich
	// nach und nach neu, bis das Ergebnis da ist.
	if wantsHTML(r) {
		http.Redirect(w, r, statusURL, http.StatusSeeOther)

		return
	}

	writeJSON(w, http.StatusAccepted, analyzeAcceptedReply{
		JobID:     id,
		Status:    jobPending,
		StatusURL: statusURL,
		Hint: "Analyse läuft auf dem Engine-Host. Status unter statusUrl " +
			"abfragen; eine vollständige Partie dauert Minuten.",
	})
}

// handleAnalyzeStatus bedient GET /analyze/status?id=… und reicht den
// Zustand des Auftrags durch. Der Engine-Token bleibt dabei serverseitig.
func handleAnalyzeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		httpError(w, http.StatusMethodNotAllowed, "GET erwartet")

		return
	}

	if !katagoRemoteConfigured() {
		httpError(w, http.StatusNotFound,
			"kein Auftragsbetrieb: diese Instanz rechnet selbst (siehe POST /analyze)")

		return
	}

	id := strings.TrimSpace(r.URL.Query().Get("id"))

	if id == "" {
		httpError(w, http.StatusBadRequest, "Parameter \"id\" fehlt")

		return
	}

	reply, status, err := fetchJob(id)

	if err != nil {
		if wantsHTML(r) {
			writeStatusPage(w, jobStatusReply{ID: id, Status: jobError,
				Error: err.Error()})

			return
		}

		httpError(w, status, "%v", err)

		return
	}

	if wantsHTML(r) {
		writeStatusPage(w, reply)

		return
	}

	writeJSON(w, http.StatusOK, reply)
}

// writeStatusPage rendert den Auftragszustand als schlichte Seite.
//
// Sie hält die Zusage ein, dass der Dienst ohne JavaScript nutzbar bleibt:
// Solange gerechnet wird, lädt sich die Seite per meta-refresh selbst neu;
// am Ende steht das Ergebnis da. Bewusst ohne eigenes Rendering-Framework
// — die Erzählstränge sind bereits fertiger Text.
func writeStatusPage(w http.ResponseWriter, reply jobStatusReply) {
	var b strings.Builder

	b.WriteString("<!doctype html><html lang=\"de\"><head>")
	b.WriteString("<meta charset=\"utf-8\">")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">")

	pending := reply.Status == jobPending || reply.Status == jobRunning

	if pending {
		b.WriteString("<meta http-equiv=\"refresh\" content=\"5\">")
	}

	b.WriteString("<title>goteach — Analyse</title>")
	b.WriteString("<link rel=\"stylesheet\" href=\"/style.css\">")
	b.WriteString("</head><body><main>")

	switch {
	case pending:
		fmt.Fprintf(&b, "<h1>Analyse läuft</h1><p>Seit %.0f Sekunden. "+
			"Diese Seite aktualisiert sich alle 5 Sekunden von selbst; "+
			"eine vollständige Partie dauert Minuten.</p>",
			reply.Elapsed)

	case reply.Status == jobError:
		fmt.Fprintf(&b, "<h1>Analyse fehlgeschlagen</h1><p>%s</p>",
			html.EscapeString(reply.Error))

	case reply.Result == nil:
		b.WriteString("<h1>Kein Ergebnis</h1><p>Der Auftrag meldet sich " +
			"fertig, liefert aber keinen Report.</p>")

	default:
		writeReportHTML(&b, reply)
	}

	b.WriteString("<p><a href=\"/\">Neue Analyse</a></p>")
	b.WriteString("</main></body></html>")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, b.String())
}

// writeReportHTML schreibt das fertige Ergebnis in die Seite.
func writeReportHTML(b *strings.Builder, reply jobStatusReply) {
	res := reply.Result

	fmt.Fprintf(b, "<h1>Analyse fertig</h1><p>Brett %d×%d, Komi %.1f, "+
		"%d Züge, %.0f Sekunden Rechenzeit.</p>",
		res.Size, res.Size, res.Komi, res.Moves, reply.Elapsed)

	if res.Synthetic {
		b.WriteString("<p><strong>ACHTUNG: SYNTHETISCHE WERTE.</strong> " +
			"Auf dem Engine-Host lief keine echte KataGo-Engine, sondern " +
			"der Mock. Die Zahlen taugen nicht als Teaching.</p>")
	}

	if len(res.Strands) == 0 {
		b.WriteString("<p>Keine Erzählstränge gefunden — die Partie verlief " +
			"ohne erkennbar zusammenhängende Kämpfe.</p>")
	} else {
		fmt.Fprintf(b, "<h2>Erzählstränge (%d)</h2>", len(res.Strands))

		for i := range res.Strands {
			s := &res.Strands[i]
			text := s.Text

			if s.TextLLM != "" {
				text = s.TextLLM
			}

			fmt.Fprintf(b, "<p>%s</p>", html.EscapeString(text))
		}
	}

	if len(res.Reports) > 0 {
		fmt.Fprintf(b, "<h2>Züge (%d)</h2><pre>", len(res.Reports))

		for i := range res.Reports {
			b.WriteString(html.EscapeString(res.Reports[i].Text))
			b.WriteString("\n\n")
		}

		b.WriteString("</pre>")
	}
}
