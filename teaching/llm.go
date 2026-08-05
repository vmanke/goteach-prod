package teaching

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
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
func NewAnthropicPolisher(apiKey, model string) func(*MoveReport) (string, error) {
	client := &http.Client{Timeout: 60 * time.Second}

	const system = "Sie sind ein erfahrener Go-Lehrer (Baduk/Weiqi). " +
		"Formulieren Sie die gegebene, faktisch verifizierte Zuganalyse als " +
		"kurzen, klaren Lehrtext auf Deutsch (Sie-Form, max. 120 Wörter). " +
		"STRIKTE REGELN: Verwenden Sie AUSSCHLIESSLICH die gelieferten " +
		"Zahlen, Koordinaten und Züge. Erfinden Sie keine neuen Züge, " +
		"Varianten, Zahlen oder taktischen Behauptungen. Bei Unsicherheit " +
		"lassen Sie Details weg, statt zu spekulieren."

	return func(r *MoveReport) (string, error) {
		facts, err := json.Marshal(r)

		if err != nil {
			return "", err
		}

		body := map[string]any{
			"model":      model,
			"max_tokens": 400,
			"system":     system,
			"messages": []map[string]any{
				{
					"role":    "user",
					"content": "Verifizierte Fakten (JSON):\n" + string(facts),
				},
			},
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

		resp, err := client.Do(req)

		if err != nil {
			return "", err
		}

		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("llm: HTTP %d", resp.StatusCode)
		}

		var out struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return "", err
		}

		for _, c := range out.Content {
			if c.Type == "text" && c.Text != "" {
				return c.Text, nil
			}
		}

		return "", fmt.Errorf("llm: leere Antwort")
	}
}
