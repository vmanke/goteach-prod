// Client-Seite des Auftragsbetriebs: Diese Instanz (eine ohne
// KataGo-Binary) reicht die Partie an den Engine-Host weiter und fragt
// später das Ergebnis ab. Beides sind kurze Aufrufe — die lange Rechnung
// bleibt drüben.
package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vmanke/goteach-prod/board"
	"github.com/vmanke/goteach-prod/teaching"
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

// submitLocalAnalyzeJob nimmt den Auftrag auf dieser Instanz an und
// antwortet sofort mit der ID — dieselbe Antwort wie beim Weiterreichen an
// einen Engine-Host, damit ein Client die beiden Betriebsarten nicht
// auseinanderhalten muss.
func submitLocalAnalyzeJob(w http.ResponseWriter, r *http.Request,
	game *board.Game, opt teaching.Options) {

	id, err := startLocalJob(game, opt)

	switch {
	case errors.Is(err, errTooManyJobs):
		httpError(w, http.StatusTooManyRequests,
			"zu viele offene Aufträge (%d); später erneut versuchen", maxLiveJobs)

		return

	case err != nil:
		httpError(w, http.StatusInternalServerError, "Auftrag anlegen: %v", err)

		return
	}

	statusURL := analyzeStatusPath + "?id=" + url.QueryEscape(id)

	// Ohne JavaScript trägt die Statusseite das Warten, wie beim
	// weitergereichten Auftrag auch.
	if wantsHTML(r) {
		http.Redirect(w, r, statusURL, http.StatusSeeOther)

		return
	}

	writeJSON(w, http.StatusAccepted, analyzeAcceptedReply{
		JobID:     id,
		Status:    jobPending,
		StatusURL: statusURL,
		Hint: "Analyse läuft. Status unter statusUrl abfragen; eine " +
			"vollständige Partie dauert Minuten. Der Auftrag liegt im " +
			"Speicher dieser Instanz: ein Neustart oder eine halbe Stunde " +
			"ohne Abholung, und er ist fort.",
	})
}

// handleAnalyzeStatus bedient GET /analyze/status?id=… und reicht den
// Zustand des Auftrags durch — vom Engine-Host, wenn dorthin delegiert
// wird, sonst aus dem eigenen Register. Der Engine-Token bleibt dabei
// serverseitig.
func handleAnalyzeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		httpError(w, http.StatusMethodNotAllowed, "GET erwartet")

		return
	}

	id := strings.TrimSpace(r.URL.Query().Get("id"))

	if id == "" {
		httpError(w, http.StatusBadRequest, "Parameter \"id\" fehlt")

		return
	}

	var (
		reply  jobStatusReply
		status int
		err    error
	)

	if katagoRemoteConfigured() {
		reply, status, err = fetchJob(id)
	} else {
		// Der Auftrag liegt hier. Gehört er zu einer anderen Fly-Maschine,
		// muss die Anfrage dorthin — sonst liefe sie in ein 404, sobald
		// mehr als eine Maschine läuft.
		if target := replayTarget(id); target != "" {
			w.Header().Set("fly-replay", "instance="+target)
			w.WriteHeader(http.StatusConflict)

			return
		}

		// Der eigene Auftrag führt seinen Feed schon während der Rechnung
		// mit — der Leser bekommt also den Stand von jetzt, nicht erst das
		// Endergebnis. Ob er vollständig ist, sagt der Abschluss-Satz
		// darin; der Client entscheidet das ohnehin selbst, weil ein
		// abgerissener Strom für ihn genauso aussieht.
		if wantsLines(r) {
			serveLocalFeed(w, id)

			return
		}

		reply, status, err = localJob(id)
	}

	if err != nil {
		if wantsHTML(r) {
			writeStatusPage(w, jobStatusReply{ID: id, Status: jobError,
				Error: err.Error()})

			return
		}

		httpError(w, status, "%v", err)

		return
	}

	// Kompaktformat vor allem anderen: der Client, der es anfordert, hat
	// bewusst keinen JSON-Parser, und ein Accept-Header entscheidet das
	// nicht so eindeutig wie ein ausdrücklicher Parameter.
	if wantsLines(r) {
		writeStatusLines(w, reply)

		return
	}

	if wantsHTML(r) {
		writeStatusPage(w, reply)

		return
	}

	writeJSON(w, http.StatusOK, reply)
}

// wantsLines meldet, ob der Aufrufer das Kompaktformat verlangt.
func wantsLines(r *http.Request) bool {
	return strings.EqualFold(r.URL.Query().Get("format"), "lines")
}

// serveLocalFeed liefert den Feed eines eigenen Auftrags, so weit er
// gediehen ist.
//
// Solange der Kopf fehlt, steht die Engine noch: dann 202 mit der
// bisherigen Rechenzeit im Header, damit der Leser etwas anzuzeigen hat.
// Sobald ein Kopf da ist, geht der Feed hinaus — unvollständig ist er
// nicht schlimmer als ein Strom, der noch läuft, und der Client liest
// beides mit demselben Leser.
func serveLocalFeed(w http.ResponseWriter, id string) {
	j, ok := jobs.get(id)

	if !ok {
		httpError(w, http.StatusNotFound,
			"Auftrag %q unbekannt (abgelaufen oder Dienst neu gestartet)", id)

		return
	}

	feed, _ := jobs.feed(id)

	if len(feed) == 0 {
		w.Header().Set(elapsedHeader,
			strconv.FormatFloat(time.Since(j.Created).Seconds(), 'f', 1, 64))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)

		return
	}

	w.Header().Set(elapsedHeader,
		strconv.FormatFloat(time.Since(j.Created).Seconds(), 'f', 1, 64))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(feed)
}

// elapsedHeader trägt die bisherige Rechenzeit einer noch laufenden
// Analyse. Ein Header, weil der Körper im Kompaktformat dann leer bleibt
// und ein Client ohne JSON-Parser sonst nichts über den Fortschritt
// erführe.
const elapsedHeader = "X-Goteach-Elapsed"

// writeStatusLines beantwortet die Statusabfrage im Kompaktformat.
//
// Der wasm-Client der Vereinsseite hat keinen JSON-Parser — das
// lines-Format existiert genau deshalb. Ohne diesen Weg nützte ihm der
// Auftragsbetrieb nichts: Er bekäme eine Auftrags-ID und könnte das
// Ergebnis dahinter nicht lesen.
//
// Der Zustand steckt im Statuscode, nicht im Körper: 202 solange
// gerechnet wird, 200 mit dem fertigen Feed. So muss der Client nichts
// auswerten, was er nicht ohnehin schon liest.
func writeStatusLines(w http.ResponseWriter, reply jobStatusReply) {
	switch {
	case reply.Status == jobPending || reply.Status == jobRunning:
		w.Header().Set(elapsedHeader,
			strconv.FormatFloat(reply.Elapsed, 'f', 1, 64))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)

	case reply.Status == jobError:
		httpError(w, http.StatusBadGateway, "%s", reply.Error)

	case reply.Result == nil:
		httpError(w, http.StatusBadGateway,
			"Auftrag meldet sich fertig, liefert aber keinen Report")

	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, linesBody(*reply.Result))
	}
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
