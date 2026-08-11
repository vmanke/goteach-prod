// Auftragsbetrieb auf dem Engine-Host: POST /engine/jobs nimmt eine Partie
// entgegen, rechnet sie im Hintergrund und gibt sofort eine Auftrags-ID
// zurück; GET /engine/jobs?id=… liefert Status und am Ende den Report.
//
// Warum überhaupt: Eine vollständige Partie kostet Minuten. Die aufrufende
// Instanz läuft auf Vercel und wird dort nach 300 Sekunden hart beendet —
// synchron ist das nicht zu gewinnen. Der Auftrag liegt deshalb hier, wo
// kein solches Limit gilt.
//
// Warum auf Teaching- statt auf Engine-Ebene: teaching.AnalyzeGame ruft den
// Analyzer je nach RefineVisits mehrfach auf. Läge der Auftrag eine Ebene
// tiefer (/engine/analyze), müsste der Aufrufer weiterhin die ganze
// Pipeline abwarten und wäre nichts gewonnen.
//
// Abgesichert wie /engine/analyze: Shared-Secret KATAGO_ENGINE_TOKEN,
// Vergleich in konstanter Zeit, ohne Token existiert die Route nach außen
// nicht (404).
package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/vmanke/goteach-prod/board"
	"github.com/vmanke/goteach-prod/teaching"
)

// jobTTL ist die Vorhaltezeit fertiger Aufträge. Lang genug, dass ein
// Client mit Backoff das Ergebnis sicher abholt, kurz genug, dass der
// Speicher einer schlafenden Maschine nicht vollläuft.
const jobTTL = 30 * time.Minute

// maxLiveJobs deckelt die Warteschlange. Darüber wird abgewiesen, statt
// Aufträge anzunehmen, die ohnehin niemand mehr rechtzeitig bekommt.
const maxLiveJobs = 32

// Auftragszustände.
const (
	jobPending = "pending"
	jobRunning = "running"
	jobDone    = "done"
	jobError   = "error"
)

// job ist ein Analyseauftrag samt Ergebnis.
type job struct {
	ID      string
	Status  string
	Err     string
	Result  *analyzeResponse
	Created time.Time
	Done    time.Time
}

// jobStore hält die Aufträge im Speicher.
//
// Bewusst keine Datenbank: Ein Auftrag ist nur so lange interessant, wie
// der Client auf ihn wartet. Die Grenze davon steht in der fly.toml —
// hält die Maschine an, sind die Aufträge weg und müssen neu gestellt
// werden.
type jobStore struct {
	mu   sync.Mutex
	jobs map[string]*job

	// slot serialisiert die Rechnung: KataGo sättigt alle Kerne, eine
	// zweite Analyse gleichzeitig macht beide langsamer. Wartende
	// Aufträge bleiben so lange "pending".
	slot chan struct{}
}

func newJobStore() *jobStore {
	return &jobStore{
		jobs: map[string]*job{},
		slot: make(chan struct{}, 1),
	}
}

// jobs ist das Register dieser Instanz.
var jobs = newJobStore()

// newJobID erzeugt eine Auftrags-ID. Läuft der Dienst auf Fly, wird die
// Machine-ID vorangestellt: Bei mehreren Maschinen landet die Abfrage
// sonst womöglich auf einer anderen als der rechnenden, die den Auftrag
// gar nicht kennt (siehe replayTarget).
func newJobID() (string, error) {
	buf := make([]byte, 12)

	if _, err := rand.Read(buf); err != nil {
		return "", err
	}

	id := hex.EncodeToString(buf)

	if machine := os.Getenv("FLY_MACHINE_ID"); machine != "" {
		return machine + "." + id, nil
	}

	return id, nil
}

// replayTarget liefert die Machine-ID aus einer Auftrags-ID, wenn der
// Auftrag zu einer ANDEREN Maschine gehört als der, die gerade antwortet.
// Leer heißt: selbst zuständig.
func replayTarget(id string) string {
	machine, _, ok := strings.Cut(id, ".")

	if !ok {
		return ""
	}

	self := os.Getenv("FLY_MACHINE_ID")

	if self == "" || machine == self {
		return ""
	}

	return machine
}

// evictLocked entfernt abgelaufene Aufträge; Aufrufer hält die Sperre.
func (s *jobStore) evictLocked(now time.Time) {
	for id, j := range s.jobs {
		if j.Status != jobDone && j.Status != jobError {
			continue
		}

		if now.Sub(j.Done) > jobTTL {
			delete(s.jobs, id)
		}
	}
}

// add legt einen Auftrag an; false heißt: Warteschlange voll.
func (s *jobStore) add(j *job) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.evictLocked(time.Now())

	if len(s.jobs) >= maxLiveJobs {
		return false
	}

	s.jobs[j.ID] = j

	return true
}

// get liefert eine Kopie des Auftrags (nicht den Zeiger — der Worker
// schreibt weiter daran).
func (s *jobStore) get(id string) (job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	j, ok := s.jobs[id]

	if !ok {
		return job{}, false
	}

	return *j, true
}

// update ändert einen Auftrag unter der Sperre.
func (s *jobStore) update(id string, fn func(*job)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if j, ok := s.jobs[id]; ok {
		fn(j)
	}
}

// run rechnet den Auftrag. Läuft in einer eigenen Goroutine und wartet
// zuerst auf den freien Rechenplatz.
func (s *jobStore) run(id string, game *board.Game, opt teaching.Options) {
	s.slot <- struct{}{}
	defer func() { <-s.slot }()

	s.update(id, func(j *job) { j.Status = jobRunning })

	// Immer die LOKALE Engine (oder der Mock) — nie katago.Remote, sonst
	// reichen sich zwei Instanzen die Arbeit im Kreis weiter.
	resp, _, err := computeAnalysis(game, opt, startLocalAnalyzer)

	s.update(id, func(j *job) {
		j.Done = time.Now()

		if err != nil {
			j.Status = jobError
			j.Err = err.Error()

			return
		}

		j.Status = jobDone
		j.Result = &resp
	})

	if err != nil {
		log.Printf("goteach-server: Auftrag %s fehlgeschlagen: %v", id, err)
	}
}

// jobRequest ist der Body von POST /engine/jobs.
//
// Die Optionen reisen als rohe Parameter statt als teaching.Options: Die
// Struktur enthält Funktionsfelder (Polish, PolishStrand) und ist damit
// nicht serialisierbar. So wird außerdem auf beiden Seiten dasselbe
// optionsFrom benutzt, und die Deckel gelten hier erneut.
type jobRequest struct {
	SGF    string            `json:"sgf"`
	Params map[string]string `json:"params"`
}

// jobStatusReply ist die Antwort von GET /engine/jobs?id=… und zugleich
// das, was die aufrufende Instanz an ihren Client durchreicht.
type jobStatusReply struct {
	ID      string           `json:"id"`
	Status  string           `json:"status"`
	Elapsed float64          `json:"elapsedSeconds"`
	Error   string           `json:"error,omitempty"`
	Result  *analyzeResponse `json:"result,omitempty"`
}

// handleEngineJobs bedient POST /engine/jobs und GET /engine/jobs?id=…
func handleEngineJobs(w http.ResponseWriter, r *http.Request) {
	token := os.Getenv("KATAGO_ENGINE_TOKEN")

	if token == "" {
		httpError(w, http.StatusNotFound,
			"Auftragsbetrieb nicht konfiguriert (KATAGO_ENGINE_TOKEN fehlt)")

		return
	}

	got, ok := bearerToken(r)

	if !ok || !tokenEqual(got, token) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		httpError(w, http.StatusUnauthorized, "Engine-Token falsch oder fehlt")

		return
	}

	switch r.Method {
	case http.MethodPost:
		createJob(w, r)

	case http.MethodGet:
		readJob(w, r)

	default:
		w.Header().Set("Allow", "GET, POST")
		httpError(w, http.StatusMethodNotAllowed, "GET oder POST erwartet")
	}
}

// createJob nimmt einen Auftrag an und antwortet sofort mit der ID.
func createJob(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxEngineBytes+1))

	if err != nil || len(body) > maxEngineBytes {
		httpError(w, http.StatusRequestEntityTooLarge,
			"Body unlesbar oder größer als %d Bytes", maxEngineBytes)

		return
	}

	var q jobRequest

	if err := json.Unmarshal(body, &q); err != nil {
		httpError(w, http.StatusBadRequest, "JSON unlesbar: %v", err)

		return
	}

	if len(q.SGF) == 0 || len(q.SGF) > maxSGFBytes {
		httpError(w, http.StatusBadRequest,
			"sgf fehlt oder ist größer als %d Bytes", maxSGFBytes)

		return
	}

	game, err := board.ParseSGF(q.SGF)

	if err != nil {
		httpError(w, http.StatusBadRequest, "SGF: %v", err)

		return
	}

	// Deckel erneut anwenden: dem entfernten Aufrufer wird nicht vertraut.
	opt, err := optionsFrom(func(k string) string { return q.Params[k] })

	if err != nil {
		httpError(w, http.StatusBadRequest, "%v", err)

		return
	}

	id, err := newJobID()

	if err != nil {
		httpError(w, http.StatusInternalServerError, "ID erzeugen: %v", err)

		return
	}

	j := &job{ID: id, Status: jobPending, Created: time.Now()}

	if !jobs.add(j) {
		httpError(w, http.StatusTooManyRequests,
			"zu viele offene Aufträge (%d); später erneut versuchen", maxLiveJobs)

		return
	}

	go jobs.run(id, game, opt)

	writeJSON(w, http.StatusAccepted, jobStatusReply{ID: id, Status: jobPending})
}

// readJob liefert Status und, sobald fertig, das Ergebnis.
func readJob(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))

	if id == "" {
		httpError(w, http.StatusBadRequest, "Parameter \"id\" fehlt")

		return
	}

	// Gehört der Auftrag zu einer anderen Fly-Maschine, muss die Anfrage
	// dorthin. Ohne das liefe sie in ein 404, sobald mehr als eine
	// Maschine läuft.
	if target := replayTarget(id); target != "" {
		w.Header().Set("fly-replay", "instance="+target)
		w.WriteHeader(http.StatusConflict)

		return
	}

	j, ok := jobs.get(id)

	if !ok {
		httpError(w, http.StatusNotFound,
			"Auftrag %q unbekannt (abgelaufen oder Maschine neu gestartet)", id)

		return
	}

	reply := jobStatusReply{
		ID:      j.ID,
		Status:  j.Status,
		Error:   j.Err,
		Result:  j.Result,
		Elapsed: time.Since(j.Created).Seconds(),
	}

	if j.Status == jobDone || j.Status == jobError {
		reply.Elapsed = j.Done.Sub(j.Created).Seconds()
	}

	writeJSON(w, http.StatusOK, reply)
}
