// Engine-Passthrough: POST /engine/analyze reicht eine katago.Request
// samt Turns an die LOKALE Engine dieser Instanz durch (oder den Mock).
// Damit kann eine Instanz ohne Engine-Binary — etwa auf Vercel — die
// Analyse an den Docker-Host delegieren (katago.Remote als Client).
//
// Geschützt per Shared-Secret KATAGO_ENGINE_TOKEN (Bearer, Vergleich in
// konstanter Zeit) — bewusst NICHT per User-JWT: der Aufrufer ist ein
// Server, kein Mensch. Ohne gesetztes Token antwortet die Route 404,
// der Passthrough existiert dann nach außen nicht.
package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/vmanke/goteach-prod/katago"
)

// maxEngineBytes begrenzt den Passthrough-Body; eine Partie mit Turns
// liegt weit darunter.
const maxEngineBytes = 2 << 20

// maxEngineTurns deckelt die Zahl der Stellungen pro Anfrage; reale
// Partien haben unter 1000 Züge.
const maxEngineTurns = 2048

// tokenEqual vergleicht Tokens über ihre SHA-256-Digests: hmac.Equal
// bricht bei ungleicher Länge sofort ab, die Digests sind immer gleich
// lang — so verrät auch die Token-Länge keinen Timing-Unterschied.
func tokenEqual(a, b string) bool {
	da := sha256.Sum256([]byte(a))
	db := sha256.Sum256([]byte(b))

	return hmac.Equal(da[:], db[:])
}

// engineQuery ist der Request-Body von POST /engine/analyze
// (Gegenstück zu katago.Remote).
type engineQuery struct {
	Request katago.Request `json:"request"`
	Turns   []int          `json:"turns"`
}

// engineReply ist die 200-Antwort von POST /engine/analyze.
type engineReply struct {
	Synthetic bool             `json:"synthetic"`
	Results   []*katago.Result `json:"results"`
}

// handleEngineAnalyze bedient POST /engine/analyze.
func handleEngineAnalyze(w http.ResponseWriter, r *http.Request) {
	token := os.Getenv("KATAGO_ENGINE_TOKEN")

	if token == "" {
		httpError(w, http.StatusNotFound,
			"Engine-Passthrough nicht konfiguriert (KATAGO_ENGINE_TOKEN fehlt)")

		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		httpError(w, http.StatusMethodNotAllowed, "POST erwartet")

		return
	}

	got, ok := bearerToken(r)

	if !ok || !tokenEqual(got, token) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		httpError(w, http.StatusUnauthorized, "Engine-Token falsch oder fehlt")

		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxEngineBytes+1))

	if err != nil || len(body) > maxEngineBytes {
		httpError(w, http.StatusRequestEntityTooLarge,
			"Body unlesbar oder größer als %d Bytes", maxEngineBytes)

		return
	}

	var q engineQuery

	if err := json.Unmarshal(body, &q); err != nil {
		httpError(w, http.StatusBadRequest, "JSON unlesbar: %v", err)

		return
	}

	if q.Request.Size < 2 || q.Request.Size > 25 {
		httpError(w, http.StatusBadRequest,
			"size %d außerhalb 2–25", q.Request.Size)

		return
	}

	if len(q.Turns) == 0 || len(q.Turns) > maxEngineTurns {
		httpError(w, http.StatusBadRequest,
			"turns leer oder mehr als %d", maxEngineTurns)

		return
	}

	// Visits erneut deckeln: dem entfernten Aufrufer wird nicht vertraut.
	if q.Request.MaxVisits <= 0 || q.Request.MaxVisits > maxVisits {
		q.Request.MaxVisits = maxVisits
	}

	// Immer die LOKALE Engine (oder den Mock) — nie katago.Remote, sonst
	// könnten sich zwei Instanzen gegenseitig endlos weiterreichen.
	an, synthetic, err := startLocalAnalyzer()

	if err != nil {
		httpError(w, http.StatusBadGateway, "KataGo-Start: %v", err)

		return
	}

	defer func() {
		if err := an.Close(); err != nil {
			log.Printf("goteach-server: Analyzer schließen: %v", err)
		}
	}()

	results, err := an.AnalyzeGame(q.Request, q.Turns)

	if err != nil {
		httpError(w, http.StatusInternalServerError, "Analyse: %v", err)

		return
	}

	writeJSON(w, http.StatusOK, engineReply{
		Synthetic: synthetic,
		Results:   results,
	})
}
