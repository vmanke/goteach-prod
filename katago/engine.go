// Package katago implementiert einen Client für die KataGo Analysis Engine
// (JSON über stdin/stdout). Feldnamen und Semantik sind gegen
// docs/Analysis_Engine.md des KataGo-Repositories verifiziert:
//   - Query: id, moves, initialStones, rules, komi, boardXSize/boardYSize,
//     analyzeTurns, maxVisits, includeOwnership.
//   - Response: id, turnNumber, moveInfos{move,visits,winrate,scoreLead,pv,
//     order}, rootInfo{winrate,scoreLead,visits}, ownership.
//   - Ownership: Länge boardYSize*boardXSize, row-major, beginnend oben
//     links (A19) bis unten rechts (T1) — identisch zur board.Idx-Ordnung.
//   - Perspektive ALLER Werte: reportAnalysisWinratesAs aus der Analysis-
//     Config. Dieses Projekt setzt verbindlich BLACK voraus (siehe
//     mitgelieferte analysis.cfg; entspricht dem offiziellen
//     analysis_example.cfg).
package katago

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"sync"
	"sync/atomic"
)

// MoveInfo ist ein von KataGo betrachteter Kandidatenzug.
type MoveInfo struct {
	Move      string   `json:"move"`
	Visits    int      `json:"visits"`
	Winrate   float64  `json:"winrate"`
	ScoreLead float64  `json:"scoreLead"`
	Order     int      `json:"order"`
	PV        []string `json:"pv"`
}

// RootInfo sind die Wurzelstatistiken der analysierten Stellung.
type RootInfo struct {
	Winrate   float64 `json:"winrate"`
	ScoreLead float64 `json:"scoreLead"`
	Visits    int     `json:"visits"`
}

// Result ist die Antwort für genau eine analysierte Stellung (turnNumber).
type Result struct {
	ID         string     `json:"id"`
	TurnNumber int        `json:"turnNumber"`
	MoveInfos  []MoveInfo `json:"moveInfos"`
	RootInfo   RootInfo   `json:"rootInfo"`
	Ownership  []float64  `json:"ownership"`
	ErrorMsg   string     `json:"error"`
	Warning    string     `json:"warning"`
}

// Best liefert den Engine-Erstvorschlag (kleinste order, Fallback: Visits).
func (r *Result) Best() *MoveInfo {
	if len(r.MoveInfos) == 0 {
		return nil
	}

	best := 0

	for i := 1; i < len(r.MoveInfos); i++ {
		a, b := r.MoveInfos[i], r.MoveInfos[best]

		if a.Order < b.Order || (a.Order == b.Order && a.Visits > b.Visits) {
			best = i
		}
	}

	return &r.MoveInfos[best]
}

// Request beschreibt eine zu analysierende Partie/Stellung.
type Request struct {
	InitialStones [][2]string // z. B. Handicap: {{"B","D4"}, ...}
	Moves         [][2]string // Züge in GTP-Koordinaten: {{"B","Q16"}, ...}
	Rules         string      // "chinese", "japanese", "tromp-taylor", ...
	Komi          float64
	Size          int
	MaxVisits     int
}

// Analyzer abstrahiert die Engine, damit Tests und Offline-Demos ohne
// KataGo-Binary laufen können (siehe Mock).
type Analyzer interface {
	// AnalyzeGame analysiert die Stellungen zu den angegebenen turnNumbers
	// (0 = Ausgangsstellung, i = Stellung nach Moves[i-1]) und liefert die
	// Ergebnisse aufsteigend nach TurnNumber sortiert.
	AnalyzeGame(req Request, turns []int) ([]*Result, error)

	Close() error
}

// Engine ist der Prozess-Client zur echten KataGo Analysis Engine.
type Engine struct {
	cmd   *exec.Cmd
	stdin *json.Encoder
	wmu   sync.Mutex

	mu      sync.Mutex
	pending map[string]chan *Result
	dead    error

	nextID atomic.Uint64
}

// Start startet `katago analysis -model <model> -config <config>`.
func Start(katagoPath, modelPath, configPath string) (*Engine, error) {
	cmd := exec.Command(katagoPath, "analysis",
		"-model", modelPath, "-config", configPath)

	stdin, err := cmd.StdinPipe()

	if err != nil {
		return nil, fmt.Errorf("katago: stdin: %w", err)
	}

	stdout, err := cmd.StdoutPipe()

	if err != nil {
		return nil, fmt.Errorf("katago: stdout: %w", err)
	}

	// KataGo loggt Diagnose auf stderr; durchreichen.
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("katago: Start fehlgeschlagen: %w", err)
	}

	e := &Engine{
		cmd:     cmd,
		stdin:   json.NewEncoder(stdin),
		pending: map[string]chan *Result{},
	}

	go e.readLoop(stdout)

	return e, nil
}

func (e *Engine) readLoop(r interface{ Read([]byte) (int, error) }) {
	sc := bufio.NewScanner(r)
	// Ownership-Zeilen sind lang; Puffer großzügig dimensionieren.
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)

	for sc.Scan() {
		line := sc.Bytes()

		if len(line) == 0 {
			continue
		}

		var res Result

		if err := json.Unmarshal(line, &res); err != nil {
			fmt.Fprintf(os.Stderr, "katago: unlesbare Zeile ignoriert: %v\n", err)

			continue
		}

		e.mu.Lock()
		ch := e.pending[res.ID]
		e.mu.Unlock()

		if ch != nil {
			ch <- &res
		}
	}

	err := sc.Err()

	if err == nil {
		err = fmt.Errorf("katago: Engine-Prozess beendet")
	}

	e.mu.Lock()
	e.dead = err

	for id, ch := range e.pending {
		close(ch)
		delete(e.pending, id)
	}

	e.mu.Unlock()
}

// AnalyzeGame sendet EINE Query mit analyzeTurns und sammelt alle Antworten
// (eine pro Turn) ein — effizienter als N Einzel-Queries, da KataGo intern
// Cache und Batching nutzt.
func (e *Engine) AnalyzeGame(req Request, turns []int) ([]*Result, error) {
	e.mu.Lock()

	if e.dead != nil {
		e.mu.Unlock()

		return nil, e.dead
	}

	id := fmt.Sprintf("q%d", e.nextID.Add(1))
	ch := make(chan *Result, len(turns))
	e.pending[id] = ch
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		delete(e.pending, id)
		e.mu.Unlock()
	}()

	q := map[string]any{
		"id":               id,
		"moves":            req.Moves,
		"rules":            req.Rules,
		"komi":             req.Komi,
		"boardXSize":       req.Size,
		"boardYSize":       req.Size,
		"analyzeTurns":     turns,
		"includeOwnership": true,
	}

	if req.Moves == nil {
		q["moves"] = [][2]string{}
	}

	if len(req.InitialStones) > 0 {
		q["initialStones"] = req.InitialStones
	}

	if req.MaxVisits > 0 {
		q["maxVisits"] = req.MaxVisits
	}

	e.wmu.Lock()
	err := e.stdin.Encode(q)
	e.wmu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("katago: Senden fehlgeschlagen: %w", err)
	}

	out := make([]*Result, 0, len(turns))

	for len(out) < len(turns) {
		res, ok := <-ch

		if !ok {
			e.mu.Lock()
			err := e.dead
			e.mu.Unlock()

			return nil, fmt.Errorf("katago: Verbindung verloren: %w", err)
		}

		if res.ErrorMsg != "" {
			return nil, fmt.Errorf("katago: Engine-Fehler: %s", res.ErrorMsg)
		}

		if res.Warning != "" {
			fmt.Fprintf(os.Stderr, "katago: Warnung: %s\n", res.Warning)
		}

		out = append(out, res)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].TurnNumber < out[j].TurnNumber
	})

	return out, nil
}

// Close beendet die Engine.
func (e *Engine) Close() error {
	if e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
	}

	return e.cmd.Wait()
}
