package katago

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrRemote kennzeichnet Fehler der Remote-Engine (Netz, HTTP-Status,
// unlesbare Antwort). Der Server mappt sie auf 502 statt 500.
var ErrRemote = errors.New("katago: Remote-Engine")

// remoteTimeout deckelt einen kompletten Analyse-Roundtrip; Analysen
// skalieren mit Visits × Stellungen und dürfen Minuten dauern.
const remoteTimeout = 5 * time.Minute

// Remote ist ein Analyzer, der die Analyse an einen entfernten
// goteach-Server mit echter Engine delegiert (POST <url>/engine/analyze,
// Bearer-Token). So bekommt eine Instanz ohne KataGo-Binary — etwa auf
// Vercel — echte Analysen vom Docker-Host.
type Remote struct {
	baseURL   string
	token     string
	client    *http.Client
	synthetic bool
}

// NewRemote baut einen Remote-Analyzer für die Basis-URL des
// Engine-Hosts (z. B. "https://goteach.fly.dev").
func NewRemote(baseURL, token string) *Remote {
	return &Remote{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		client:  &http.Client{Timeout: remoteTimeout},
	}
}

// remoteQuery ist der Request-Body von POST /engine/analyze.
type remoteQuery struct {
	Request Request `json:"request"`
	Turns   []int   `json:"turns"`
}

// remoteReply ist die 200-Antwort von POST /engine/analyze.
type remoteReply struct {
	Synthetic bool      `json:"synthetic"`
	Results   []*Result `json:"results"`
}

// AnalyzeGame schickt die Anfrage an den Engine-Host und reicht dessen
// Ergebnisse durch. Antwortet der Host mit dem Mock (kein KATAGO_* dort),
// merkt sich Remote das synthetic-Flag — abfragbar über Synthetic().
func (rm *Remote) AnalyzeGame(req Request, turns []int) ([]*Result, error) {
	body, err := json.Marshal(remoteQuery{Request: req, Turns: turns})

	if err != nil {
		return nil, fmt.Errorf("%w: Anfrage kodieren: %v", ErrRemote, err)
	}

	httpReq, err := http.NewRequest(http.MethodPost,
		rm.baseURL+"/engine/analyze", bytes.NewReader(body))

	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRemote, err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+rm.token)

	resp, err := rm.client.Do(httpReq)

	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRemote, err)
	}

	defer resp.Body.Close()

	// Fehlerantworten sind klein ({"error": …}); Limit gegen Müll-Antworten.
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))

		var e struct {
			Error string `json:"error"`
		}

		if json.Unmarshal(msg, &e) == nil && e.Error != "" {
			return nil, fmt.Errorf("%w: HTTP %d: %s", ErrRemote, resp.StatusCode, e.Error)
		}

		return nil, fmt.Errorf("%w: HTTP %d", ErrRemote, resp.StatusCode)
	}

	var reply remoteReply

	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return nil, fmt.Errorf("%w: Antwort unlesbar: %v", ErrRemote, err)
	}

	if len(reply.Results) != len(turns) {
		return nil, fmt.Errorf("%w: %d Ergebnisse für %d Turns",
			ErrRemote, len(reply.Results), len(turns))
	}

	rm.synthetic = reply.Synthetic

	return reply.Results, nil
}

// Synthetic meldet, ob die letzte Antwort vom Mock des Engine-Hosts kam.
func (rm *Remote) Synthetic() bool { return rm.synthetic }

// Close hält das Analyzer-Interface ein; es gibt keinen Prozess zu beenden.
func (*Remote) Close() error { return nil }
