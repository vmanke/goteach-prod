// Package server stellt die Teaching-Analyse als HTTP-Dienst bereit.
// Die Vercel-kompatiblen Entrypoints (main.go in der Repo-Wurzel und
// cmd/server/main.go) sind dünne Wrapper um Run.
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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vmanke/goteach-prod/board"
	"github.com/vmanke/goteach-prod/internal/dotenv"
	"github.com/vmanke/goteach-prod/katago"
	"github.com/vmanke/goteach-prod/teaching"
	"github.com/vmanke/goteach-prod/vision"
)

// maxSGFBytes begrenzt den Request-Body; reale SGF-Partien liegen weit
// unter 1 MiB.
const maxSGFBytes = 1 << 20

// maxImageBytes begrenzt einen Bild-Upload. Fotos eines Bretts liegen
// deutlich darunter; das Limit hält den Speicherbedarf pro Anfrage im Zaum.
const maxImageBytes = 8 << 20

// maxVisits deckelt den teuersten Parameter (Visits × Stellungen).
const maxVisits = 1000

// defaultVisits gilt, wenn der Parameter visits fehlt.
const defaultVisits = 50

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

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Printf("goteach-server: lausche auf :%s (KataGo konfiguriert: %v)",
		port, katagoConfigured())

	return srv.ListenAndServe()
}

// Handler baut den Router des Dienstes; Run und die Tests teilen ihn.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleHome)
	mux.HandleFunc("/api", handleInfo)
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/analyze", handleAnalyze)
	mux.HandleFunc("/robots.txt", handleRobots)
	mux.HandleFunc("/sitemap.xml", handleSitemap)
	mux.HandleFunc("/app.js", handleAsset("app.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("/style.css", handleAsset("style.css", "text/css; charset=utf-8"))
	mux.HandleFunc("/favicon.svg", handleAsset("favicon.svg", "image/svg+xml"))

	return withRecover(mux)
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
		"katago":   katagoConfigured(),
		"frontend": "GET / (HTML im Browser)",
		"analyze": "POST /analyze mit SGF (roh, Formularfeld oder Datei-Upload \"sgf\") " +
			"oder ogs=<URL|ID> (online-go.com); Parameter: visits, tau, from, to, " +
			"rules, komi; download=1 für Datei-Download",
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

// analyzeResponse ist die JSON-Antwort von POST /analyze.
type analyzeResponse struct {
	Size      int     `json:"size"`
	Komi      float64 `json:"komi"`
	Rules     string  `json:"rules,omitempty"`
	Moves     int     `json:"moves"`
	Synthetic bool    `json:"synthetic"`
	// Strands ist die Hauptsicht auf eine Partie: zusammenhängende
	// Abschnitte statt einer Wand aus Einzelzügen. Reports bleibt als
	// Detailebene darunter erhalten.
	Strands []teaching.Strand `json:"strands,omitempty"`

	Reports []teaching.MoveReport `json:"reports,omitempty"`

	// Position steht statt Reports, wenn die Anfrage ein Brettfoto war:
	// Eine erkannte Stellung hat keine Zughistorie und damit keine
	// Lehreinheit pro Zug.
	Position *teaching.PositionReport `json:"position,omitempty"`
}

func handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		httpError(w, http.StatusMethodNotAllowed, "POST erwartet")

		return
	}

	data, image, get, status, err := inputFromRequest(w, r)

	if err != nil {
		httpError(w, status, "%v", err)

		return
	}

	if len(image) > 0 {
		analyzeImage(w, r, image, get)

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

	an, synthetic, err := startAnalyzer()

	if err != nil {
		httpError(w, http.StatusBadGateway, "KataGo-Start: %v", err)

		return
	}

	defer func() {
		if err := an.Close(); err != nil {
			log.Printf("goteach-server: Analyzer schließen: %v", err)
		}
	}()

	report, err := teaching.AnalyzeGame(game, an, opt)

	if err != nil {
		httpError(w, http.StatusInternalServerError, "Analyse: %v", err)

		return
	}

	// Effektive Werte melden: Query-Parameter (opt) überschreiben die
	// SGF-Werte – genau wie in teaching.Analyze für die Analyse verwendet.
	effKomi := game.Komi

	if opt.Komi != nil {
		effKomi = *opt.Komi
	}

	effRules := game.Rules

	if opt.Rules != "" {
		effRules = opt.Rules
	}

	if wantsDownload(get) {
		w.Header().Set("Content-Disposition",
			`attachment; filename="goteach-report.json"`)
	}

	writeJSON(w, http.StatusOK, analyzeResponse{
		Size:      game.Size,
		Komi:      effKomi,
		Rules:     effRules,
		Moves:     len(game.Moves),
		Synthetic: synthetic,
		Strands:   report.Strands,
		Reports:   report.Moves,
	})
}

// startAnalyzer liefert die Engine dieser Instanz. Ohne konfigurierte
// KataGo-Binary läuft der Mock; die Antwort trägt dann "synthetic": true.
func startAnalyzer() (katago.Analyzer, bool, error) {
	if !katagoConfigured() {
		return katago.Mock{}, true, nil
	}

	configPath := os.Getenv("KATAGO_CONFIG")

	if configPath == "" {
		configPath = "analysis.cfg"
	}

	eng, err := katago.Start(
		os.Getenv("KATAGO_PATH"), os.Getenv("KATAGO_MODEL"), configPath)

	if err != nil {
		return nil, false, err
	}

	return eng, false, nil
}

// analyzeImage beantwortet eine Anfrage mit Brettfoto: erst die Erkennung
// über die Vision-Brücke (Stufen 1–2), dann der Stellungsbericht.
//
// Kein Teaching pro Zug: Ein Foto zeigt eine Stellung, keine Partie.
func analyzeImage(w http.ResponseWriter, r *http.Request, image []byte,
	get func(string) string) {

	if !vision.Configured() {
		httpError(w, http.StatusNotImplemented,
			"Bilderkennung ist auf dieser Instanz nicht eingerichtet "+
				"(%s nicht gesetzt)", vision.EnvCommand)

		return
	}

	opt, err := optionsFrom(get)

	if err != nil {
		httpError(w, http.StatusBadRequest, "%v", err)

		return
	}

	detect := vision.Options{Komi: opt.Komi}

	if raw := strings.TrimSpace(get("size")); raw != "" {
		size, err := strconv.Atoi(raw)

		if err != nil || (size != 9 && size != 13 && size != 19) {
			httpError(w, http.StatusBadRequest, "size muss 9, 13 oder 19 sein")

			return
		}

		detect.Size = size
	}

	pos, err := vision.Detect(r.Context(), image, detect)

	if err != nil {
		httpError(w, http.StatusBadRequest, "Bilderkennung: %v", err)

		return
	}

	game, err := pos.Game()

	if err != nil {
		httpError(w, http.StatusBadRequest, "Bilderkennung: %v", err)

		return
	}

	an, synthetic, err := startAnalyzer()

	if err != nil {
		httpError(w, http.StatusBadGateway, "KataGo-Start: %v", err)

		return
	}

	defer func() {
		if err := an.Close(); err != nil {
			log.Printf("goteach-server: Analyzer schließen: %v", err)
		}
	}()

	report, err := teaching.AnalyzePosition(game, an, opt)

	if err != nil {
		httpError(w, http.StatusInternalServerError, "Analyse: %v", err)

		return
	}

	if wantsDownload(get) {
		w.Header().Set("Content-Disposition",
			`attachment; filename="goteach-stellung.json"`)
	}

	writeJSON(w, http.StatusOK, analyzeResponse{
		Size:      report.Size,
		Komi:      report.Komi,
		Rules:     report.Rules,
		Synthetic: synthetic,
		Position:  report,
	})
}

// inputFromRequest extrahiert die Eingabe aus dem Request: SGF als
// Datei-Upload, Textfeld "sgf" (multipart/URL-kodiert) oder roher Body — und
// zusätzlich ein PNG aus dem Datei-Feld "image".
// Der zurückgegebene Getter liest Parameter aus Query und Formular
// (Query gewinnt); der Statuscode gilt nur im Fehlerfall.
func inputFromRequest(w http.ResponseWriter, r *http.Request) (
	[]byte, []byte, func(string) string, int, error) {
	queryOnly := func(key string) string { return r.URL.Query().Get(key) }

	mediaType := r.Header.Get("Content-Type")

	if mt, _, err := mime.ParseMediaType(mediaType); err == nil {
		mediaType = mt
	}

	switch mediaType {
	case "multipart/form-data":
		// Das Limit richtet sich nach dem Bild-Upload, denn nur der kann
		// groß werden; das engere SGF-Limit prüft der Aufrufer danach.
		limit := int64(maxImageBytes + (1 << 16))
		r.Body = http.MaxBytesReader(w, r.Body, limit)

		if err := r.ParseMultipartForm(limit); err != nil {
			return nil, nil, queryOnly, bodyErrStatus(err),
				fmt.Errorf("Formular unlesbar: %w", err)
		}

		get := valuesGetter(r, r.PostForm)

		if image, err := uploaded(r, "image", maxImageBytes); err != nil {
			return nil, nil, get, http.StatusBadRequest, err
		} else if len(image) > 0 {
			return nil, image, get, 0, nil
		}

		if sgf, err := uploaded(r, "sgf", maxSGFBytes); err != nil {
			return nil, nil, get, http.StatusBadRequest, err
		} else if len(sgf) > 0 {
			return sgf, nil, get, 0, nil
		}

		// Kein (nutzbarer) Datei-Upload: Textfeld "sgf". Leere Werte
		// überspringen — ein leeres File-Input landet als Leerstring
		// vor dem Textfeld gleichen Namens.
		return []byte(firstNonEmpty(r.PostForm["sgf"])), nil, get, 0, nil

	case "application/x-www-form-urlencoded":
		// Achtung: curl setzt diesen Typ als Default (-d/--data-binary).
		// Ein roher SGF-Body beginnt immer mit "(" und wird direkt
		// akzeptiert; nur echte Formulardaten werden als Query geparst.
		// URL-Kodierung kann das SGF aufblähen, daher großzügigeres Limit —
		// das SGF-Limit selbst prüft der Aufrufer auf den dekodierten Bytes.
		const limit = 3*maxSGFBytes + (1 << 16)

		raw, err := io.ReadAll(io.LimitReader(r.Body, limit+1))

		if err != nil {
			return nil, nil, queryOnly, http.StatusBadRequest,
				fmt.Errorf("Body unlesbar: %w", err)
		}

		if len(raw) > limit {
			return nil, nil, queryOnly, http.StatusRequestEntityTooLarge,
				fmt.Errorf("Body größer als %d Bytes", limit)
		}

		if trimmed := bytes.TrimSpace(raw); bytes.HasPrefix(trimmed, []byte("(")) {
			return trimmed, nil, queryOnly, 0, nil
		}

		values, err := url.ParseQuery(string(raw))

		if err != nil {
			return nil, nil, queryOnly, http.StatusBadRequest,
				fmt.Errorf("Formular unlesbar: %w", err)
		}

		return []byte(firstNonEmpty(values["sgf"])), nil,
			valuesGetter(r, values), 0, nil

	case "image/png":
		// Rohes PNG im Body: der kürzeste Weg für API-Nutzer.
		image, err := io.ReadAll(io.LimitReader(r.Body, maxImageBytes+1))

		if err != nil {
			return nil, nil, queryOnly, http.StatusBadRequest,
				fmt.Errorf("Body unlesbar: %w", err)
		}

		if len(image) > maxImageBytes {
			return nil, nil, queryOnly, http.StatusRequestEntityTooLarge,
				fmt.Errorf("Bild größer als %d Bytes", maxImageBytes)
		}

		return nil, image, queryOnly, 0, nil
	}

	// Roher Body (bisheriges API-Verhalten).
	data, err := io.ReadAll(io.LimitReader(r.Body, maxSGFBytes+1))

	if err != nil {
		return nil, nil, queryOnly, http.StatusBadRequest,
			fmt.Errorf("Body unlesbar: %w", err)
	}

	return data, nil, queryOnly, 0, nil
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
