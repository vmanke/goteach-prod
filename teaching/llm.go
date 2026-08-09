package teaching

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// NewAnthropicPolisher liefert eine Polish-Funktion, die den verifizierten
// Basistext eines MoveReports sprachlich glättet. Sicherheitsprinzip aus dem
// Architekturbericht: Das LLM darf NICHT rechnen — es erhält ausschließlich
// die bereits verifizierten Zahlen/Züge und die harte Anweisung, nichts
// hinzuzuerfinden. Der Basistext (Report.Text) bleibt immer erhalten.
//
// Der API-Key kommt aus der Umgebung (.env: ANTHROPIC_API_KEY); er wird nie
// geloggt oder persistiert. Kostenhinweis: pro Zug ein API-Call — bei langen
// Partien entsprechend budgetieren oder -llm nur für ausgewählte Züge nutzen.
//
// Bewusst Raw-HTTP statt offiziellem SDK: das Projekt bleibt dependency-frei.
// Modellhinweise (Stand claude-fable-5 als Default):
//   - Thinking ist auf Fable 5 immer aktiv und zählt gegen max_tokens;
//     deshalb großzügiges max_tokens. Kein "thinking"-Feld senden — jede
//     explizite Konfiguration außer adaptive wird mit 400 abgelehnt.
//   - Sicherheits-Klassifizierer können Anfragen ablehnen (HTTP 200 mit
//     stop_reason "refusal"); dafür wird der serverseitige Fallback
//     (fallbacks: "default", Beta-Header) aktiviert, der die Anfrage von
//     einem Ersatzmodell beantworten lässt.
func NewAnthropicPolisher(apiKey, model string) func(*MoveReport) (string, error) {
	const system = moveSystemPrompt

	call := newPolishCall(apiKey, model, system)

	return func(r *MoveReport) (string, error) {
		return call(r)
	}
}

// NewAnthropicStrandPolisher glättet den Text eines Erzählstrangs.
//
// Der eigentliche Gewinn ist die Zahl der Aufrufe: Ein Strang fasst viele
// Züge zusammen, eine Partie kommt damit auf eine Handvoll Anfragen statt
// auf eine je Zug.
//
// Die zusätzliche Regel gegenüber dem Zug-Prompt ist die wichtigste hier:
// Gekoppelte Formen dürfen nicht kausal verknüpft werden. Die
// Kreuzkorrelation zeigt einen zeitlichen Zusammenhang; "A wurde durch B
// entschieden" wäre eine Behauptung, die die Daten nicht hergeben.
func NewAnthropicStrandPolisher(apiKey, model string) func(*Strand) (string, error) {
	call := newPolishCall(apiKey, model, strandSystemPrompt)

	return func(s *Strand) (string, error) {
		return call(s)
	}
}

const moveSystemPrompt = "Sie sind ein erfahrener Go-Lehrer (Baduk/Weiqi). " +
	"Formulieren Sie die gegebene, faktisch verifizierte Zuganalyse als " +
	"kurzen, klaren Lehrtext auf Deutsch (Sie-Form, max. 120 Wörter). " +
	"STRIKTE REGELN: Verwenden Sie AUSSCHLIESSLICH die gelieferten " +
	"Zahlen, Koordinaten und Züge. Erfinden Sie keine neuen Züge, " +
	"Varianten, Zahlen oder taktischen Behauptungen. Bei Unsicherheit " +
	"lassen Sie Details weg, statt zu spekulieren."

const strandSystemPrompt = "Sie sind ein erfahrener Go-Lehrer (Baduk/Weiqi). " +
	"Sie erhalten einen Abschnitt einer Partie — eine Brettgegend über einen " +
	"Zugbereich — mit verifizierten Zahlen und benannten Formen. " +
	"Erzählen Sie diesen Abschnitt als zusammenhängenden Lehrtext auf " +
	"Deutsch (Sie-Form, max. 150 Wörter). " +
	"STRIKTE REGELN: Verwenden Sie AUSSCHLIESSLICH die gelieferten Zahlen, " +
	"Koordinaten, Zugnummern und Formnamen. Erfinden Sie keine Züge, " +
	"Varianten oder Bewertungen. Die Angabe gekoppelter Formen bedeutet " +
	"einen ZEITLICHEN Zusammenhang, KEINE Ursache: Schreiben Sie " +
	"\"hängt zeitlich damit zusammen\" und niemals \"wurde dadurch " +
	"entschieden\" oder \"führte zu\". Bei Unsicherheit lassen Sie " +
	"Details weg, statt zu spekulieren."

// newPolishCall baut den gemeinsamen HTTP-Aufruf für beide Polisher.
func newPolishCall(apiKey, model, system string) func(any) (string, error) {
	client := &http.Client{Timeout: 120 * time.Second}

	// Server-Fallback nur auf Modellen anfordern, die ihn unterstützen.
	useFallback := strings.HasPrefix(model, "claude-fable-5") ||
		strings.HasPrefix(model, "claude-mythos-5") ||
		strings.HasPrefix(model, "claude-opus-5")

	return func(payload any) (string, error) {
		facts, err := json.Marshal(payload)

		if err != nil {
			return "", err
		}

		body := map[string]any{
			"model": model,
			// Thinking zählt auf Fable 5 mit gegen max_tokens.
			"max_tokens": 2048,
			"system":     system,
			"messages": []map[string]any{
				{
					"role":    "user",
					"content": "Verifizierte Fakten (JSON):\n" + string(facts),
				},
			},
		}

		if useFallback {
			body["fallbacks"] = "default"
		}

		buf, err := json.Marshal(body)

		if err != nil {
			return "", err
		}

		req, err := http.NewRequest(http.MethodPost,
			"https://api.anthropic.com/v1/messages", bytes.NewReader(buf))

		if err != nil {
			return "", err
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")

		if useFallback {
			req.Header.Set("anthropic-beta", "server-side-fallback-2026-07-01")
		}

		resp, err := client.Do(req)

		if err != nil {
			return "", err
		}

		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("llm: HTTP %d", resp.StatusCode)
		}

		var out struct {
			StopReason string `json:"stop_reason"`
			Content    []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return "", err
		}

		// Klassifizierer-Ablehnung: kein Feinschliff, Basistext bleibt.
		if out.StopReason == "refusal" {
			return "", fmt.Errorf("llm: Anfrage abgelehnt (stop_reason=refusal)")
		}

		for _, c := range out.Content {
			if c.Type == "text" && c.Text != "" {
				return c.Text, nil
			}
		}

		return "", fmt.Errorf("llm: leere Antwort")
	}
}
