// goteach-server stellt die Teaching-Analyse als HTTP-Dienst bereit
// (Vercel-kompatibler Entrypoint unter cmd/server/main.go).
//
// Endpunkte:
//
//	GET  /         Dienstinfo (JSON)
//	GET  /healthz  Liveness-Check
//	POST /analyze  SGF im Request-Body → Teaching-Reports als JSON
//
// Engine-Wahl über Umgebung: Sind KATAGO_PATH und KATAGO_MODEL gesetzt,
// wird pro Anfrage eine KataGo-Engine gestartet. Andernfalls läuft der
// Mock-Analyzer; die Antwort trägt dann "synthetic": true, denn ohne
// Engine sind alle Werte SYNTHETISCH und taugen nicht als Teaching.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
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

func main() {
	// Secrets ausschließlich aus Umgebung/.env (nie als Flag).
	if err := dotenv.Load(".env"); err != nil {
		log.Fatalf("goteach-server: .env: %v", err)
	}

	port := os.Getenv("PORT")

	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleInfo)
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/analyze", handleAnalyze)

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("goteach-server: lausche auf :%s (KataGo konfiguriert: %v)",
		port, katagoConfigured())

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("goteach-server: %v", err)
	}
}

// katagoConfigured meldet, ob eine echte Engine per Umgebung verfügbar ist.
func katagoConfigured() bool {
	return os.Getenv("KATAGO_PATH") != "" && os.Getenv("KATAGO_MODEL") != ""
}

func handleInfo(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"service": "goteach",
		"katago":  katagoConfigured(),
		"analyze": "POST /analyze mit SGF im Body; Parameter: visits, tau, from, to, rules, komi",
		"warnung": "ohne konfigurierte KataGo-Engine sind alle Werte synthetisch (Mock)",
		"healthz": "GET /healthz",
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}

// analyzeResponse ist die JSON-Antwort von POST /analyze.
type analyzeResponse struct {
	Size      int                   `json:"size"`
	Komi      float64               `json:"komi"`
	Rules     string                `json:"rules,omitempty"`
	Moves     int                   `json:"moves"`
	Synthetic bool                  `json:"synthetic"`
	Reports   []teaching.MoveReport `json:"reports"`
}

func handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		httpError(w, http.StatusMethodNotAllowed, "POST erwartet")

		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, maxSGFBytes+1))

	if err != nil {
		httpError(w, http.StatusBadRequest, "Body unlesbar: %v", err)

		return
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

	opt, err := optionsFromQuery(r)

	if err != nil {
		httpError(w, http.StatusBadRequest, "%v", err)

		return
	}

	var an katago.Analyzer

	synthetic := !katagoConfigured()

	if synthetic {
		an = katago.Mock{}
	} else {
		configPath := os.Getenv("KATAGO_CONFIG")

		if configPath == "" {
			configPath = "analysis.cfg"
		}

		eng, err := katago.Start(
			os.Getenv("KATAGO_PATH"), os.Getenv("KATAGO_MODEL"), configPath)

		if err != nil {
			httpError(w, http.StatusBadGateway, "KataGo-Start: %v", err)

			return
		}

		an = eng
	}

	defer an.Close()

	reports, err := teaching.Analyze(game, an, opt)

	if err != nil {
		httpError(w, http.StatusInternalServerError, "Analyse: %v", err)

		return
	}

	writeJSON(w, http.StatusOK, analyzeResponse{
		Size:      game.Size,
		Komi:      game.Komi,
		Rules:     game.Rules,
		Moves:     len(game.Moves),
		Synthetic: synthetic,
		Reports:   reports,
	})
}

// optionsFromQuery baut teaching.Options aus den Query-Parametern;
// Grenzen wie im CLI, Visits zusätzlich gedeckelt.
func optionsFromQuery(r *http.Request) (teaching.Options, error) {
	q := r.URL.Query()

	opt := teaching.Options{
		Visits: 50,
		Tau:    3.0,
		Rules:  q.Get("rules"),
	}

	var err error

	if v := q.Get("visits"); v != "" {
		if opt.Visits, err = strconv.Atoi(v); err != nil || opt.Visits < 1 {
			return opt, fmt.Errorf("visits ungültig: %q", v)
		}
	}

	if opt.Visits > maxVisits {
		opt.Visits = maxVisits
	}

	if v := q.Get("tau"); v != "" {
		if opt.Tau, err = strconv.ParseFloat(v, 64); err != nil || opt.Tau <= 0 {
			return opt, fmt.Errorf("tau ungültig: %q", v)
		}
	}

	if v := q.Get("from"); v != "" {
		if opt.From, err = strconv.Atoi(v); err != nil || opt.From < 1 {
			return opt, fmt.Errorf("from ungültig: %q", v)
		}
	}

	if v := q.Get("to"); v != "" {
		if opt.To, err = strconv.Atoi(v); err != nil || opt.To < 0 {
			return opt, fmt.Errorf("to ungültig: %q", v)
		}
	}

	if v := q.Get("komi"); v != "" {
		komi, err := strconv.ParseFloat(v, 64)

		if err != nil || math.IsNaN(komi) {
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
