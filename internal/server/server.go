// Package server stellt die Teaching-Analyse als HTTP-Dienst bereit.
// Die Entrypoints (main.go in der Repo-Wurzel — den baut das Dockerfile —
// und cmd/server/main.go) sind dünne Wrapper um Run.
//
// Endpunkte:
//
//	GET  /            Startseite (HTML für Browser, sonst Dienstinfo als JSON)
//	GET  /api         Dienstinfo (JSON)
//	GET  /healthz     Liveness-Check
//	POST /analyze     SGF (roh, Formularfeld oder Datei-Upload "sgf")
//	                  → Teaching-Reports als JSON; download=1 als Datei
//	GET  /robots.txt, /sitemap.xml, /app.js, /style.css, /favicon.svg
//
// Engine-Wahl über Umgebung: Sind KATAGO_PATH und KATAGO_MODEL gesetzt,
// wird pro Anfrage eine KataGo-Engine gestartet. Andernfalls läuft der
// Mock-Analyzer; die Antwort trägt dann "synthetic": true, denn ohne
// Engine sind alle Werte SYNTHETISCH und taugen nicht als Teaching.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"mime"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vmanke/goteach-prod/board"
	"github.com/vmanke/goteach-prod/internal/dotenv"
	"github.com/vmanke/goteach-prod/katago"
	"github.com/vmanke/goteach-prod/teaching"
)

// maxSGFBytes begrenzt den Request-Body; reale SGF-Partien liegen weit
// unter 1 MiB.
const maxSGFBytes = 1 << 20

// maxVisits deckelt den teuersten Parameter (Visits × Stellungen).
const maxVisits = 1000

// defaultVisits gilt, wenn der Parameter visits fehlt.
const defaultVisits = 50

// keepAliveInterval ist der Abstand der Füllbytes, mit denen /analyze
// lange Rechnungen am Leben hält — deutlich unter den ~60 Sekunden, nach
// denen Proxys (konkret: der von Fly.io) Verbindungen ohne Datenfluss
// kappen. KataGo rechnet je nach Partielänge Minuten und antwortet erst
// am Ende; ohne Heartbeat sieht der Client einen Netzwerkfehler.
const keepAliveInterval = 15 * time.Second

// Run startet den HTTP-Dienst und blockiert bis zum Serverfehler.
func Run() error {
	// Secrets ausschließlich aus Umgebung/.env (nie als Flag).
	if err := dotenv.Load(".env"); err != nil {
		return fmt.Errorf(".env: %w", err)
	}

	port := os.Getenv("PORT")

	if port == "" {
		port = "8080"
	}

	// Fehlkonfigurierte Auth fällt so beim Start auf, nicht erst beim
	// ersten Login (fail closed bleibt trotzdem: die Handler prüfen erneut).
	if err := validateAuthEnv(); err != nil {
		return fmt.Errorf("Auth: %w", err)
	}

	if err := validateRemoteEnv(); err != nil {
		return fmt.Errorf("Remote-Engine: %w", err)
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Printf("goteach-server: lausche auf :%s "+
		"(KataGo lokal: %v, Remote-Engine: %v, Auth: %v)",
		port, katagoConfigured(), katagoRemoteConfigured(), authEnabled())

	return srv.ListenAndServe()
}

// Handler baut den Router des Dienstes; Run und die Tests teilen ihn.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleHome)
	mux.HandleFunc("/api", handleInfo)
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/login", handleLogin)
	mux.HandleFunc("/analyze", requireAuth(handleAnalyze))
	mux.HandleFunc("/analyze/status", requireAuth(handleAnalyzeStatus))
	mux.HandleFunc("/engine/analyze", handleEngineAnalyze)
	mux.HandleFunc("/engine/jobs", handleEngineJobs)
	mux.HandleFunc("/robots.txt", handleRobots)
	mux.HandleFunc("/sitemap.xml", handleSitemap)
	mux.HandleFunc("/app.js", handleAsset("app.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("/style.css", handleAsset("style.css", "text/css; charset=utf-8"))
	mux.HandleFunc("/favicon.svg", handleAsset("favicon.svg", "image/svg+xml"))

	return withRecover(withCORS(mux))
}

// corsOrigins liest die erlaubten Cross-Origin-Absender aus
// GOTEACH_CORS_ORIGINS (kommagetrennt). Ohne Umgebung gilt die
// Vereinshomepage, die den Dienst aus dem Browser aufruft.
func corsOrigins() map[string]bool {
	raw := os.Getenv("GOTEACH_CORS_ORIGINS")

	if raw == "" {
		raw = "https://flascheleer-berlin.de,https://www.flascheleer-berlin.de"
	}

	out := map[string]bool{}

	for _, origin := range strings.Split(raw, ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			out[origin] = true
		}
	}

	return out
}

// withCORS beantwortet Cross-Origin-Aufrufe der erlaubten Absender.
// Nur gelistete Origins werden gespiegelt — kein Wildcard: der Dienst
// soll aus fremden Seiten heraus nicht still mitbenutzt werden. Vary
// verhindert, dass ein Cache die Origin-abhängige Antwort vermischt.
func withCORS(next http.Handler) http.Handler {
	allowed := corsOrigins()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Vary gilt für JEDE Antwort, nicht nur für erlaubte Absender: die
		// Antwort hängt vom Origin-Header ab, und ein gemeinsamer Cache
		// dürfte sonst eine Antwort ohne CORS-Header speichern und später
		// einem erlaubten Absender ausliefern.
		w.Header().Add("Vary", "Origin")

		if origin := r.Header.Get("Origin"); origin != "" && allowed[origin] {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)

			// Preflight endet hier: der Browser fragt nach, ob POST mit
			// Content-Type erlaubt ist, und braucht dafür keinen Body.
			if r.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", "GET, POST")
				// Authorization gehört dazu, seit /analyze hinter
				// optionalem JWT-Login liegen kann.
				h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				h.Set("Access-Control-Max-Age", "86400")
				w.WriteHeader(http.StatusNoContent)

				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// withRecover fängt Handler-Panics ab und antwortet mit 500-JSON statt
// eines Verbindungsabbruchs; der Serverprozess läuft in jedem Fall weiter.
func withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}

				log.Printf("goteach-server: Panic bei %s %s: %v",
					r.Method, r.URL.Path, rec)
				httpError(w, http.StatusInternalServerError, "interner Fehler")
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// katagoConfigured meldet, ob eine echte Engine per Umgebung verfügbar ist.
func katagoConfigured() bool {
	return os.Getenv("KATAGO_PATH") != "" && os.Getenv("KATAGO_MODEL") != ""
}

// katagoRemoteConfigured meldet, ob Analysen an einen entfernten
// Engine-Host delegiert werden (Instanz ohne Engine → Docker-Host).
func katagoRemoteConfigured() bool {
	return os.Getenv("KATAGO_REMOTE_URL") != ""
}

// engineAvailable meldet, ob es irgendeinen Weg zu echten Analysen gibt.
func engineAvailable() bool {
	return katagoConfigured() || katagoRemoteConfigured()
}

// validateRemoteEnv prüft die Remote-Konfiguration beim Serverstart.
// Eine URL ohne Token liefe sonst deterministisch in 401→502 beim
// Engine-Host — schwer zu diagnostizieren; der Passthrough verlangt
// immer ein Token.
func validateRemoteEnv() error {
	if katagoRemoteConfigured() && os.Getenv("KATAGO_REMOTE_TOKEN") == "" {
		return errors.New("KATAGO_REMOTE_TOKEN fehlt (KATAGO_REMOTE_URL ist gesetzt)")
	}

	return nil
}

// handleInfo liefert die Dienstinfo als JSON (GET /api).
func handleInfo(w http.ResponseWriter, r *http.Request) {
	if !allowGetHead(w, r) {
		return
	}

	serveInfo(w)
}

func serveInfo(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service":  "goteach",
		"katago":   engineAvailable(),
		"auth":     authEnabled(),
		"login":    "POST /login mit JSON {\"username\":…,\"password\":…} → JWT (nur bei aktiver Auth)",
		"frontend": "GET / (HTML im Browser)",
		"analyze": "POST /analyze mit SGF (roh, Formularfeld oder Datei-Upload \"sgf\") " +
			"oder ogs=<URL|ID> (online-go.com); Parameter: visits, tau, from, to, " +
			"rules, komi; download=1 für Datei-Download; bei aktiver Auth mit " +
			"Authorization: Bearer <Token>",
		"warnung": "ohne konfigurierte KataGo-Engine sind alle Werte synthetisch (Mock)",
		"healthz": "GET /healthz",
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if !allowGetHead(w, r) {
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}

// setupStone ist ein Vorgabestein (Handicap, SGF AB/AW) in der Antwort.
type setupStone struct {
	Player string `json:"player"` // "Schwarz" | "Weiß"
	Coord  string `json:"coord"`  // GTP
}

// analyzeResponse ist die JSON-Antwort von POST /analyze.
type analyzeResponse struct {
	Size      int     `json:"size"`
	Komi      float64 `json:"komi"`
	Rules     string  `json:"rules,omitempty"`
	Moves     int     `json:"moves"`
	Synthetic bool    `json:"synthetic"`
	// Setup sind die vorab gesetzten Steine — ohne sie kann kein Client
	// die Partie auf einem Brett nachspielen.
	Setup []setupStone `json:"setup,omitempty"`
	// Strands ist die Hauptsicht auf eine Partie: zusammenhängende
	// Abschnitte statt einer Wand aus Einzelzügen. Reports bleibt als
	// Detailebene darunter erhalten.
	Strands []teaching.Strand `json:"strands,omitempty"`

	Reports []teaching.MoveReport `json:"reports,omitempty"`
}

func handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		httpError(w, http.StatusMethodNotAllowed, "POST erwartet")

		return
	}

	data, get, status, err := inputFromRequest(w, r)

	if err != nil {
		httpError(w, status, "%v", err)

		return
	}

	// Ohne eigenes SGF darf die Partie per "ogs" von online-go.com kommen;
	// ein mitgeliefertes SGF hat immer Vorrang.
	if len(data) == 0 {
		ref := strings.TrimSpace(get("ogs"))

		if ref == "" {
			httpError(w, http.StatusBadRequest,
				"kein SGF übergeben (roher Body, Formularfeld oder Datei-Upload "+
					"\"sgf\") und keine OGS-Partie (Parameter \"ogs\")")

			return
		}

		id, err := parseOGSGameID(ref)

		if err != nil {
			httpError(w, http.StatusBadRequest, "%v", err)

			return
		}

		if data, err = fetchOGSSGF(r.Context(), id); err != nil {
			httpError(w, http.StatusBadGateway, "OGS: %v", err)

			return
		}
	}

	if len(data) > maxSGFBytes {
		httpError(w, http.StatusRequestEntityTooLarge,
			"SGF größer als %d Bytes", maxSGFBytes)

		return
	}

	game, err := board.ParseSGF(string(data))

	if err != nil {
		httpError(w, http.StatusBadRequest, "SGF: %v", err)

		return
	}

	opt, err := optionsFrom(get)

	if err != nil {
		httpError(w, http.StatusBadRequest, "%v", err)

		return
	}

	// Liegt die Engine auf einem anderen Host, wird hier nicht gerechnet:
	// Eine volle Partie kostet Minuten, und diese Instanz läuft unter einem
	// Zeitlimit ihrer Umgebung, das sie unweigerlich reißt. Der
	// Auftrag wandert deshalb zum Engine-Host, der ihn ohne solches Limit
	// abarbeitet; der Client holt das Ergebnis über /analyze/status ab.
	//
	// SGF und Optionen sind oben bereits geprüft — Eingabefehler meldet
	// diese Instanz also weiterhin sofort, nicht erst nach dem Polling.
	if katagoRemoteConfigured() {
		submitAnalyzeJob(w, r, string(data), get)

		return
	}

	// Kompaktformat für Clients ohne JSON-Parser (die Vereinshomepage
	// rendert im wasm-Client): ein Datensatz je Zug, Felder mit US (0x1f),
	// Datensätze mit RS (0x1e) getrennt — dieselbe Codec-Idee, die der
	// Client auch für seine eigenen Inseln verwendet.
	wantLines := strings.EqualFold(get("format"), "lines")

	// Dieses Format geht fortlaufend hinaus: Kopf und Vorgabesteine
	// sofort, dann jede Lehreinheit, sobald sie gerechnet ist. Eine lange
	// Partie füllt die Seite so von oben her, statt sie minutenlang leer
	// zu lassen.
	if wantLines {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		streamLines(w, game, opt, startAnalyzer)

		return
	}

	// Header vor der Rechnung festschreiben: beginnt der Heartbeat die
	// Antwort, lassen sie sich nicht mehr ändern. Das Füllzeichen ist
	// eines, das jeder Leser überliest — Whitespace vor einem JSON-Wert
	// ist gültiges JSON.
	filler := "\n"

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if wantsDownload(get) {
		w.Header().Set("Content-Disposition",
			`attachment; filename="goteach-report.json"`)
	}

	resp, status, started, err := computeKeepingAlive(w, keepAliveInterval, filler,
		func() (analyzeResponse, int, error) {
			return computeAnalysis(game, opt, startAnalyzer)
		})

	if err != nil {
		if !started {
			// Nichts gesendet: der Fehler bleibt ein echter HTTP-Status,
			// exakt wie vor dem Heartbeat.
			httpError(w, status, "%v", err)

			return
		}

		// Status 200 und Füllbytes sind draußen; der Fehler kann nur noch
		// im Body reisen — JSON-Leser prüfen "error".
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")

		if err := enc.Encode(map[string]string{"error": err.Error()}); err != nil {
			log.Printf("goteach-server: JSON-Antwort: %v", err)
		}

		return
	}

	// Erst kodieren, dann schreiben: encoding/json verweigert NaN/±Inf
	// komplett — mit Encoder direkt auf w bliebe dem Client dann ein Body
	// aus lauter Füllzeichen ohne jedes JSON. Die Werte werden vorab
	// bereinigt (eine Engine-Zahl außerhalb der Mathematik ist "kein
	// Ergebnis", nicht "kein Body"), und selbst ein danach noch
	// scheiternder Marshal wird zu einem lesbaren Fehlerobjekt.
	body, err := json.MarshalIndent(sanitizedResponse(resp), "", "  ")

	if err != nil {
		log.Printf("goteach-server: JSON-Antwort: %v", err)

		body = []byte(`{"error":"interner Fehler beim Kodieren der Antwort"}`)
	}

	if !started {
		w.WriteHeader(http.StatusOK)
	}

	_, _ = w.Write(append(body, '\n'))
}

// streamLines schreibt die Analyse fortlaufend im Kompaktformat.
//
// Reihenfolge: Kopf, Formatversion und Vorgabesteine gehen sofort hinaus —
// sie stehen fest, bevor die Engine rechnet. Danach je Zug ein Satz,
// sobald er fertig ist, am Ende die Erzählstränge und der Schlusspunkt.
//
// Fehler haben zwei Gesichter: Bevor etwas geschrieben ist, bleibt es ein
// echter HTTP-Status. Danach ist der Status längst 200 und der Fehler kann
// nur noch als E-Satz im Körper reisen — der Client sieht dann eine
// unvollständige Partie samt Grund.
func streamLines(w http.ResponseWriter, game *board.Game, opt teaching.Options,
	start func() (katago.Analyzer, bool, error)) {

	an, synthetic, err := start()

	if err != nil {
		httpError(w, http.StatusBadGateway, "KataGo-Start: %v", err)

		return
	}

	defer func() {
		if err := an.Close(); err != nil {
			log.Printf("goteach-server: Analyzer schließen: %v", err)
		}
	}()

	// Effektive Werte melden: Query-Parameter überschreiben die SGF-Werte.
	effKomi := game.Komi

	if opt.Komi != nil {
		effKomi = *opt.Komi
	}

	effRules := game.Rules

	if opt.Rules != "" {
		effRules = opt.Rules
	}

	// synthetic steht hier schon fest, weil dieser Pfad die Remote-Engine
	// nie sieht: Ist KATAGO_REMOTE_URL gesetzt, ist handleAnalyze längst
	// in den Auftrags-Weg abgebogen.
	head := headRecords(analyzeResponse{
		Size:      game.Size,
		Komi:      effKomi,
		Rules:     effRules,
		Moves:     len(game.Moves),
		Synthetic: synthetic,
		Setup:     setupStones(game),
	})

	flusher, _ := w.(http.Flusher)
	write := func(records ...string) {
		for _, rec := range records {
			_, _ = io.WriteString(w, rec)
			_, _ = io.WriteString(w, linesRecordSep)
		}

		if flusher != nil {
			flusher.Flush()
		}
	}

	w.WriteHeader(http.StatusOK)
	write(head...)

	// Die Rechnung läuft in einer eigenen Goroutine und schiebt fertige
	// Sätze herüber; geschrieben wird ausschließlich hier, sonst kämen
	// Heartbeat und Nutzlast einander ins Gehege. Der gepufferte Kanal
	// bremst die Analyse, wenn das Netz langsamer ist als die Engine.
	records := make(chan string, 32)
	done := make(chan error, 1)

	go func() {
		defer close(records)

		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("goteach-server: Panic in der Analyse: %v", rec)
				done <- fmt.Errorf("interner Fehler in der Analyse")
			}
		}()

		_, err := teaching.AnalyzeStream(game, an, opt, teaching.StreamHandler{
			Move: func(m *teaching.MoveReport) error {
				records <- moveRecord(m)

				return nil
			},
			Strands: func(strands []teaching.Strand) error {
				for i := range strands {
					records <- strandRecord(&strands[i])
				}

				return nil
			},
		})

		done <- err
	}()

	// Solange nichts fertig wird, hält ein leerer Datensatz die Verbindung
	// offen — Proxys kappen sonst nach etwa einer Minute ohne Datenfluss.
	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()

	moves := 0

	for {
		select {
		case rec, ok := <-records:
			if !ok {
				if err := <-done; err != nil {
					log.Printf("goteach-server: Analyse: %v", err)
					write(linesRecord("E", err.Error()))

					return
				}

				write(endRecord(moves))

				return
			}

			if strings.HasPrefix(rec, "M"+linesFieldSep) {
				moves++
			}

			write(rec)
			ticker.Reset(keepAliveInterval)

		case <-ticker.C:
			write()
		}
	}
}

// setupStones übersetzt die Vorgabesteine der Partie für die Antwort.
func setupStones(game *board.Game) []setupStone {
	var out []setupStone

	for _, s := range game.Setup {
		player := "Schwarz"

		if s.Color == board.White {
			player = "Weiß"
		}

		out = append(out, setupStone{
			Player: player,
			Coord:  board.ToGTP(s.Point, game.Size),
		})
	}

	return out
}

// sanitizedResponse ersetzt NaN und ±Inf in allen Gleitkommafeldern der
// Antwort rekursiv durch 0. encoding/json kann diese Werte nicht
// darstellen, und aus einer Engine können sie in Randlagen kommen.
func sanitizedResponse(resp analyzeResponse) analyzeResponse {
	sanitizeFloats(reflect.ValueOf(&resp).Elem())

	return resp
}

func sanitizeFloats(v reflect.Value) {
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		if f := v.Float(); (math.IsNaN(f) || math.IsInf(f, 0)) && v.CanSet() {
			v.SetFloat(0)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				sanitizeFloats(v.Field(i))
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			sanitizeFloats(v.Index(i))
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			entry := v.MapIndex(key)

			if entry.Kind() == reflect.Float64 || entry.Kind() == reflect.Float32 {
				if f := entry.Float(); math.IsNaN(f) || math.IsInf(f, 0) {
					v.SetMapIndex(key, reflect.ValueOf(0.0))
				}
			}
		}
	case reflect.Ptr:
		if !v.IsNil() {
			sanitizeFloats(v.Elem())
		}
	default:
	}
}

// computeAnalysis rechnet eine Partie durch und baut daraus die Antwort.
//
// start wählt den Analyzer und ist deshalb Parameter statt fester Aufruf:
// Der synchrone Pfad nimmt startAnalyzer (lokal oder Remote, je nach
// Umgebung), der Auftrags-Worker auf dem Engine-Host dagegen zwingend
// startLocalAnalyzer — sonst könnten sich zwei Instanzen die Arbeit im
// Kreis weiterreichen.
func computeAnalysis(game *board.Game, opt teaching.Options,
	start func() (katago.Analyzer, bool, error)) (analyzeResponse, int, error) {

	an, synthetic, err := start()

	if err != nil {
		return analyzeResponse{}, http.StatusBadGateway,
			fmt.Errorf("KataGo-Start: %w", err)
	}

	defer func() {
		if err := an.Close(); err != nil {
			log.Printf("goteach-server: Analyzer schließen: %v", err)
		}
	}()

	report, err := teaching.AnalyzeGame(game, an, opt)

	if err != nil {
		// Fehler des entfernten Engine-Hosts sind Gateway-Fehler,
		// kein internes Problem dieser Instanz.
		if errors.Is(err, katago.ErrRemote) {
			return analyzeResponse{}, http.StatusBadGateway,
				fmt.Errorf("Remote-Engine: %w", err)
		}

		return analyzeResponse{}, http.StatusInternalServerError,
			fmt.Errorf("Analyse: %w", err)
	}

	// Beim Remote-Analyzer weiß erst die Antwort des Engine-Hosts,
	// ob dort eine echte Engine oder der Mock gerechnet hat.
	if rm, ok := an.(*katago.Remote); ok {
		synthetic = rm.Synthetic()
	}

	// Effektive Werte melden: Query-Parameter (opt) überschreiben
	// die SGF-Werte – genau wie in teaching.Analyze verwendet.
	effKomi := game.Komi

	if opt.Komi != nil {
		effKomi = *opt.Komi
	}

	effRules := game.Rules

	if opt.Rules != "" {
		effRules = opt.Rules
	}

	return analyzeResponse{
		Size:      game.Size,
		Komi:      effKomi,
		Rules:     effRules,
		Moves:     len(game.Moves),
		Synthetic: synthetic,
		Setup:     setupStones(game),
		Strands:   report.Strands,
		Reports:   report.Moves,
	}, http.StatusOK, nil
}

// computeKeepingAlive führt compute aus. Dauert die Rechnung länger als
// interval, beginnt die Antwort vorzeitig mit Status 200, und alle
// interval geht ein Füllzeichen samt Flush hinaus, damit kein Proxy die
// Verbindung als tot einstuft. started meldet, ob das passiert ist —
// danach sind Status und Header festgeschrieben, und Fehler müssen im
// Body transportiert werden. Der zurückgegebene Status stammt aus
// compute und ist nur bei err != nil && !started von Belang.
func computeKeepingAlive(
	w http.ResponseWriter,
	interval time.Duration,
	filler string,
	compute func() (analyzeResponse, int, error),
) (resp analyzeResponse, status int, started bool, err error) {
	type outcome struct {
		resp   analyzeResponse
		status int
		err    error
	}

	done := make(chan outcome, 1)

	go func() {
		// Die Rechnung läuft nicht mehr im Handler-Goroutine, also fängt
		// withRecover ihre Panics nicht — ohne dieses Netz risse eine
		// Panic den ganzen Serverprozess mit allen Verbindungen um.
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("goteach-server: Panic in der Analyse: %v", rec)
				done <- outcome{analyzeResponse{}, http.StatusInternalServerError,
					fmt.Errorf("interner Fehler in der Analyse")}
			}
		}()

		r, s, err := compute()
		done <- outcome{r, s, err}
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case out := <-done:
			return out.resp, out.status, started, out.err
		case <-ticker.C:
			if !started {
				w.WriteHeader(http.StatusOK)

				started = true
			}

			// Ein abgesprungener Client bricht nichts ab: die Rechnung
			// läuft zu Ende, die Schreibfehler verpuffen.
			_, _ = io.WriteString(w, filler)

			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}
}

// Trennzeichen des Kompaktformats: US zwischen Feldern, RS zwischen
// Datensätzen — Steuerzeichen, die in Prosa nicht vorkommen und vom
// Feld-Reiniger ohnehin entfernt würden.
const (
	linesFieldSep  = "\x1f"
	linesRecordSep = "\x1e"
)

// cleanRecordField macht einen Text feldtauglich für das Kompaktformat:
// Zeilenumbrüche und Tabs werden Leerzeichen, alle übrigen Steuerzeichen
// (die beiden Trenner eingeschlossen) fallen weg.
func cleanRecordField(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}

		if r < 0x20 || r == 0x7f {
			return -1
		}

		return r
	}, s)
}

// linesVersion ist die Formatversion des V-Satzes.
//
//	2 — V/P/S ergänzt. Ohne V ≥ 2 darf ein Client kein Brett nachspielen:
//	    Ein Alt-Server ohne P-Sätze ließe eine Handicap-Partie falsch
//	    aussehen.
//	3 — Die Sätze gehen einzeln hinaus, und ein Z-Satz schließt sie ab.
//	    Ohne V ≥ 3 darf ein Client aus dem fehlenden Schlusspunkt NICHT
//	    auf eine abgerissene Leitung schließen — ältere Stände kannten
//	    ihn nicht.
const linesVersion = 3

// linesRecord fügt die Felder eines Datensatzes zusammen, jedes von
// Steuerzeichen befreit.
func linesRecord(fields ...string) string {
	for i, f := range fields {
		fields[i] = cleanRecordField(f)
	}

	return strings.Join(fields, linesFieldSep)
}

// Das Kompaktformat in der Satzfolge H, V, P*, M*, S*, Z:
//
//	H|size|komi|rules|moves|synthetic
//	V|version
//	P|player|coord                       je Vorgabestein (AB/AW, Handicap)
//	M|number|player|coord|category|pointsLost|winrateBefore|winrateAfter|bestMove|text
//	S|id|area|from|to|count|movesCSV|lostBlack|lostWhite|captures|worstNumber|worstCoord|worstCategory|worstLost|shapes|text
//	Z|moves                              Schlusspunkt: so viele M-Sätze waren es
//
// H- und M-Sätze bleiben bei exakt 6 bzw. 10 Feldern — Bestandsclients
// destrukturieren beide hart. Unbekannte Satztypen überspringt der Client
// still; neue Informationen kommen deshalb als neue Sätze, nie als neue
// Felder bestehender Sätze. Zahlen mit fester Präzision, Text von
// Steuerzeichen befreit — der Client darf splitten statt parsen.
//
// Die Sätze gehen einzeln hinaus, sobald sie fertig sind (streamLines);
// linesBody baut denselben Körper am Stück, für Tests und für Aufrufer,
// die das Ergebnis ohnehin schon vollständig in der Hand halten.
func linesBody(resp analyzeResponse) string {
	records := headRecords(resp)

	for i := range resp.Reports {
		records = append(records, moveRecord(&resp.Reports[i]))
	}

	for i := range resp.Strands {
		records = append(records, strandRecord(&resp.Strands[i]))
	}

	records = append(records, endRecord(len(resp.Reports)))

	return strings.Join(records, linesRecordSep)
}

// headRecords sind die Sätze vor dem ersten Zug: Kopf, Formatversion und
// die Vorgabesteine. Sie stehen fest, bevor die Engine rechnet, und gehen
// im Strom sofort hinaus.
func headRecords(resp analyzeResponse) []string {
	records := []string{
		linesRecord(
			"H",
			strconv.Itoa(resp.Size),
			strconv.FormatFloat(resp.Komi, 'f', 1, 64),
			resp.Rules,
			strconv.Itoa(resp.Moves),
			strconv.FormatBool(resp.Synthetic),
		),
		linesRecord("V", strconv.Itoa(linesVersion)),
	}

	for _, s := range resp.Setup {
		records = append(records, linesRecord("P", s.Player, s.Coord))
	}

	return records
}

// moveRecord baut den M-Satz einer Lehreinheit (10 Felder).
func moveRecord(m *teaching.MoveReport) string {
	coord := m.Coord

	if m.Pass {
		coord = "pass"
	}

	return linesRecord(
		"M",
		strconv.Itoa(m.Number),
		m.Player,
		coord,
		m.Category,
		strconv.FormatFloat(m.PointsLost, 'f', 1, 64),
		strconv.FormatFloat(m.WinrateBefore, 'f', 3, 64),
		strconv.FormatFloat(m.WinrateAfter, 'f', 3, 64),
		m.BestMove,
		m.Text,
	)
}

// endRecord schließt den Strom ab und nennt die Zahl der ausgelieferten
// Züge. Erst dieser Satz unterscheidet eine fertige Analyse von einer
// abgerissenen Verbindung — solange die Sätze am Stück kamen, tat das der
// vollständige Körper.
func endRecord(moves int) string {
	return linesRecord("Z", strconv.Itoa(moves))
}

// strandRecord baut den S-Satz eines Erzählstrangs (16 Felder).
func strandRecord(s *teaching.Strand) string {
	moves := make([]string, len(s.Moves))

	for i, n := range s.Moves {
		moves[i] = strconv.Itoa(n)
	}

	// Formnamen dedupliziert in Erstnennungs-Reihenfolge.
	var names []string
	seen := map[string]bool{}

	for _, sh := range s.Shapes {
		if !seen[sh.Name] {
			seen[sh.Name] = true
			names = append(names, sh.Name)
		}
	}

	worstNumber, worstCoord, worstCategory, worstLost := 0, "", "", 0.0

	if s.Worst != nil {
		worstNumber = s.Worst.Number
		worstCoord = s.Worst.Coord
		worstCategory = s.Worst.Category
		worstLost = s.Worst.PointsLost
	}

	return linesRecord(
		"S",
		strconv.Itoa(s.ID),
		s.Area,
		strconv.Itoa(s.FromMove),
		strconv.Itoa(s.ToMove),
		strconv.Itoa(len(s.Moves)),
		strings.Join(moves, ","),
		strconv.FormatFloat(s.PointsLost["Schwarz"], 'f', 1, 64),
		strconv.FormatFloat(s.PointsLost["Weiß"], 'f', 1, 64),
		strconv.Itoa(s.Captures),
		strconv.Itoa(worstNumber),
		worstCoord,
		worstCategory,
		strconv.FormatFloat(worstLost, 'f', 1, 64),
		strings.Join(names, ","),
		s.Text,
	)
}

// startAnalyzer liefert die Engine dieser Instanz. Eine lokale Binary
// (KATAGO_PATH/KATAGO_MODEL) gewinnt; sonst delegiert KATAGO_REMOTE_URL
// an einen entfernten Engine-Host. Ohne beides läuft der Mock; die
// Antwort trägt dann "synthetic": true.
func startAnalyzer() (katago.Analyzer, bool, error) {
	if !katagoConfigured() && katagoRemoteConfigured() {
		// Doppelter Boden zum Start-Check in Run: auch eingebettete Nutzung
		// des Handlers scheitert mit klarer Ursache statt 401→502.
		if err := validateRemoteEnv(); err != nil {
			return nil, false, err
		}

		// synthetic entscheidet der Engine-Host; handleAnalyze übernimmt
		// dessen Flag nach der Analyse aus katago.Remote.
		return katago.NewRemote(os.Getenv("KATAGO_REMOTE_URL"),
			os.Getenv("KATAGO_REMOTE_TOKEN")), false, nil
	}

	return startLocalAnalyzer()
}

// startLocalAnalyzer liefert Binary-Engine oder Mock — nie Remote. Der
// Engine-Passthrough nutzt ausschließlich diese Variante, damit sich
// zwei Instanzen nicht gegenseitig endlos weiterreichen können.
func startLocalAnalyzer() (katago.Analyzer, bool, error) {
	if !katagoConfigured() {
		return katago.Mock{}, true, nil
	}

	configPath := os.Getenv("KATAGO_CONFIG")

	if configPath == "" {
		configPath = "analysis.cfg"
	}

	// KATAGO_OVERRIDES ("k=v,k=v") erlaubt Hardware-Tuning ohne Rebuild,
	// z. B. mehr Suchthreads auf Maschinen mit vielen vCPUs.
	eng, err := katago.Start(
		os.Getenv("KATAGO_PATH"), os.Getenv("KATAGO_MODEL"), configPath,
		os.Getenv("KATAGO_OVERRIDES"))

	if err != nil {
		return nil, false, err
	}

	return eng, false, nil
}

// inputFromRequest extrahiert die Eingabe aus dem Request: SGF als
// Datei-Upload, Textfeld "sgf" (multipart/URL-kodiert) oder roher Body.
// Der zurückgegebene Getter liest Parameter aus Query und Formular
// (Query gewinnt); der Statuscode gilt nur im Fehlerfall.
func inputFromRequest(w http.ResponseWriter, r *http.Request) (
	[]byte, func(string) string, int, error) {
	queryOnly := func(key string) string { return r.URL.Query().Get(key) }

	mediaType := r.Header.Get("Content-Type")

	if mt, _, err := mime.ParseMediaType(mediaType); err == nil {
		mediaType = mt
	}

	switch mediaType {
	case "multipart/form-data":
		limit := int64(maxSGFBytes + (1 << 16))
		r.Body = http.MaxBytesReader(w, r.Body, limit)

		if err := r.ParseMultipartForm(limit); err != nil {
			return nil, queryOnly, bodyErrStatus(err),
				fmt.Errorf("Formular unlesbar: %w", err)
		}

		get := valuesGetter(r, r.PostForm)

		if sgf, err := uploaded(r, "sgf", maxSGFBytes); err != nil {
			return nil, get, http.StatusBadRequest, err
		} else if len(sgf) > 0 {
			return sgf, get, 0, nil
		}

		// Kein (nutzbarer) Datei-Upload: Textfeld "sgf". Leere Werte
		// überspringen — ein leeres File-Input landet als Leerstring
		// vor dem Textfeld gleichen Namens.
		return []byte(firstNonEmpty(r.PostForm["sgf"])), get, 0, nil

	case "application/x-www-form-urlencoded":
		// Achtung: curl setzt diesen Typ als Default (-d/--data-binary).
		// Ein roher SGF-Body beginnt immer mit "(" und wird direkt
		// akzeptiert; nur echte Formulardaten werden als Query geparst.
		// URL-Kodierung kann das SGF aufblähen, daher großzügigeres Limit —
		// das SGF-Limit selbst prüft der Aufrufer auf den dekodierten Bytes.
		const limit = 3*maxSGFBytes + (1 << 16)

		raw, err := io.ReadAll(io.LimitReader(r.Body, limit+1))

		if err != nil {
			return nil, queryOnly, http.StatusBadRequest,
				fmt.Errorf("Body unlesbar: %w", err)
		}

		if len(raw) > limit {
			return nil, queryOnly, http.StatusRequestEntityTooLarge,
				fmt.Errorf("Body größer als %d Bytes", limit)
		}

		if trimmed := bytes.TrimSpace(raw); bytes.HasPrefix(trimmed, []byte("(")) {
			return trimmed, queryOnly, 0, nil
		}

		values, err := url.ParseQuery(string(raw))

		if err != nil {
			return nil, queryOnly, http.StatusBadRequest,
				fmt.Errorf("Formular unlesbar: %w", err)
		}

		return []byte(firstNonEmpty(values["sgf"])), valuesGetter(r, values), 0, nil
	}

	// Roher Body (bisheriges API-Verhalten).
	data, err := io.ReadAll(io.LimitReader(r.Body, maxSGFBytes+1))

	if err != nil {
		return nil, queryOnly, http.StatusBadRequest,
			fmt.Errorf("Body unlesbar: %w", err)
	}

	return data, queryOnly, 0, nil
}

// uploaded liest ein Datei-Feld des Formulars; fehlt es oder ist es leer,
// kommt nil zurück (ein leeres File-Input ist der Normalfall, kein Fehler).
func uploaded(r *http.Request, field string, limit int) ([]byte, error) {
	file, header, err := r.FormFile(field)

	if err != nil {
		// Nur ein fehlendes Feld ist der Normalfall. Jeden anderen Fehler —
		// etwa kaputte Multipart-Daten — als "kein Upload" zu behandeln,
		// verschluckt die Ursache und lässt sie später an einer Stelle
		// auftauchen, die nichts mehr damit zu tun hat.
		if errors.Is(err, http.ErrMissingFile) {
			return nil, nil
		}

		return nil, fmt.Errorf("Upload %q unlesbar: %w", field, err)
	}

	defer file.Close()

	if header.Filename == "" || header.Size == 0 {
		return nil, nil
	}

	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))

	if err != nil {
		return nil, fmt.Errorf("Upload %q unlesbar: %w", field, err)
	}

	if len(data) > limit {
		return nil, fmt.Errorf("Upload %q größer als %d Bytes", field, limit)
	}

	return data, nil
}

// ogsBaseURL ist Variable statt Konstante, damit Tests einen Stub-Server
// einsetzen können. Abgefragt wird ausschließlich der SGF-Export der
// öffentlichen OGS-API: /api/v1/games/<id>/sgf.
var ogsBaseURL = "https://online-go.com"

var ogsClient = &http.Client{Timeout: 15 * time.Second}

// ogsRefPattern akzeptiert Partie-URLs von online-go.com; die ID wird als
// reine Ziffernfolge extrahiert — nie eine Nutzer-URL abgerufen (kein SSRF).
var ogsRefPattern = regexp.MustCompile(
	`^(?:https?://)?(?:www\.)?online-go\.com/game/(?:view/)?([0-9]+)(?:[/?#].*)?$`)

// parseOGSGameID macht aus "12345678" oder "https://online-go.com/game/12345678"
// eine validierte Partie-ID.
func parseOGSGameID(ref string) (string, error) {
	ref = strings.TrimSpace(ref)

	if ref != "" && strings.Trim(ref, "0123456789") == "" {
		return ref, nil
	}

	if m := ogsRefPattern.FindStringSubmatch(ref); m != nil {
		return m[1], nil
	}

	return "", fmt.Errorf(
		"OGS-Referenz unverständlich: %q (erwartet Partie-ID oder online-go.com/game/<id>)",
		ref)
}

// fetchOGSSGF lädt das SGF einer öffentlichen OGS-Partie. Der Context des
// eingehenden Requests bricht den Abruf mit ab, wenn der Client die
// Verbindung beendet.
func fetchOGSSGF(ctx context.Context, id string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		ogsBaseURL+"/api/v1/games/"+id+"/sgf", nil)

	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent",
		"goteach (+https://github.com/vmanke/goteach-prod)")

	resp, err := ogsClient.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d für Partie %s", resp.StatusCode, id)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSGFBytes+1))

	if err != nil {
		return nil, err
	}

	if len(data) > maxSGFBytes {
		return nil, fmt.Errorf("SGF größer als %d Bytes", maxSGFBytes)
	}

	return data, nil
}

// bodyErrStatus unterscheidet „Body zu groß" (413) von „Body kaputt" (400).
func bodyErrStatus(err error) int {
	var tooLarge *http.MaxBytesError

	if errors.As(err, &tooLarge) {
		return http.StatusRequestEntityTooLarge
	}

	return http.StatusBadRequest
}

// valuesGetter liest einen Parameter aus der Query, sonst aus den
// Formularwerten. Ein in der Query vorhandener Schlüssel gewinnt auch mit
// leerem Wert (?rules= leert also ein Formularfeld explizit); leere
// Formularwerte werden übersprungen.
func valuesGetter(r *http.Request, values url.Values) func(string) string {
	query := r.URL.Query()

	return func(key string) string {
		if vs, ok := query[key]; ok && len(vs) > 0 {
			return vs[0]
		}

		return firstNonEmpty(values[key])
	}
}

func firstNonEmpty(values []string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}

	return ""
}

// wantsDownload meldet, ob der Report als Datei ausgeliefert werden soll.
func wantsDownload(get func(string) string) bool {
	switch strings.ToLower(get("download")) {
	case "1", "true", "on", "yes":
		return true
	}

	return false
}

// optionsFrom baut teaching.Options aus den Parametern (Query/Formular);
// Grenzen wie im CLI, Visits zusätzlich gedeckelt.
func optionsFrom(get func(string) string) (teaching.Options, error) {
	opt := teaching.Options{
		Visits: defaultVisits,
		Tau:    3.0,
		Rules:  get("rules"),
	}

	var err error

	if v := get("visits"); v != "" {
		if opt.Visits, err = strconv.Atoi(v); err != nil || opt.Visits < 1 {
			return opt, fmt.Errorf("visits ungültig: %q", v)
		}
	}

	if opt.Visits > maxVisits {
		opt.Visits = maxVisits
	}

	if v := get("tau"); v != "" {
		if opt.Tau, err = strconv.ParseFloat(v, 64); err != nil || opt.Tau <= 0 {
			return opt, fmt.Errorf("tau ungültig: %q", v)
		}
	}

	if v := get("from"); v != "" {
		if opt.From, err = strconv.Atoi(v); err != nil || opt.From < 1 {
			return opt, fmt.Errorf("from ungültig: %q", v)
		}
	}

	if v := get("to"); v != "" {
		if opt.To, err = strconv.Atoi(v); err != nil || opt.To < 0 {
			return opt, fmt.Errorf("to ungültig: %q", v)
		}
	}

	if v := get("komi"); v != "" {
		komi, err := strconv.ParseFloat(v, 64)

		if err != nil || math.IsNaN(komi) || math.IsInf(komi, 0) {
			return opt, fmt.Errorf("komi ungültig: %q", v)
		}

		opt.Komi = &komi
	}

	return opt, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	if err := enc.Encode(v); err != nil {
		log.Printf("goteach-server: JSON-Antwort: %v", err)
	}
}

func httpError(w http.ResponseWriter, status int, format string, args ...any) {
	writeJSON(w, status, map[string]string{
		"error": fmt.Sprintf(format, args...),
	})
}
