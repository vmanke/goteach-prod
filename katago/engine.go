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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
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

// Request beschreibt eine zu analysierende Partie/Stellung. Die JSON-Tags
// dienen dem Remote-Passthrough (POST /engine/analyze, siehe Remote) —
// die Query an die lokale Engine baut AnalyzeGame weiterhin selbst.
type Request struct {
	InitialStones [][2]string `json:"initialStones,omitempty"` // z. B. Handicap: {{"B","D4"}, ...}
	Moves         [][2]string `json:"moves"`                   // Züge in GTP-Koordinaten: {{"B","Q16"}, ...}
	Rules         string      `json:"rules"`                   // "chinese", "japanese", "tromp-taylor", ...
	Komi          float64     `json:"komi"`
	Size          int         `json:"size"`
	MaxVisits     int         `json:"maxVisits,omitempty"`
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

// engineArgs baut die Argumentliste für `katago analysis`. overrides ist
// eine kommagetrennte Liste "schlüssel=wert,…" und wird als
// -override-config an KataGo durchgereicht (offiziell unterstützt) —
// so lässt sich z. B. die Thread-Zahl per Umgebung ohne Rebuild an die
// Hardware anpassen. Einträge ohne "=" sind Tippfehler und werden als
// Fehler gemeldet statt still verschluckt.
func engineArgs(modelPath, configPath, overrides string) ([]string, error) {
	args := []string{"analysis", "-model", modelPath, "-config", configPath}

	for _, entry := range strings.Split(overrides, ",") {
		entry = strings.TrimSpace(entry)

		if entry == "" {
			continue
		}

		key, value, ok := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		if !ok || key == "" || value == "" {
			return nil, fmt.Errorf(
				"katago: Override ohne \"schlüssel=wert\": %q", entry)
		}

		// Normalisiert weitergeben: Leerzeichen um "=" würden sonst Teil
		// des KataGo-Schlüssels bzw. -Werts.
		args = append(args, "-override-config", key+"="+value)
	}

	return args, nil
}

// Start startet `katago analysis -model <model> -config <config>`;
// overrides (optional, "k=v,k=v") ergänzt -override-config-Argumente.
func Start(katagoPath, modelPath, configPath, overrides string) (*Engine, error) {
	args, err := engineArgs(modelPath, configPath, overrides)

	if err != nil {
		return nil, err
	}

	cmd := exec.Command(katagoPath, args...)

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
	out := make([]*Result, 0, len(turns))

	err := e.AnalyzeGameStream(req, turns, func(res *Result) error {
		out = append(out, res)

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Die Engine antwortet in der Reihenfolge, in der sie fertig wird;
	// das Interface verspricht aufsteigende TurnNumber.
	sort.Slice(out, func(i, j int) bool {
		return out[i].TurnNumber < out[j].TurnNumber
	})

	return out, nil
}

// AnalyzeGameStream sendet dieselbe Query wie AnalyzeGame, reicht aber
// jede Antwort sofort an emit weiter, statt sie zu sammeln.
func (e *Engine) AnalyzeGameStream(req Request, turns []int,
	emit func(*Result) error) error {

	e.mu.Lock()

	if e.dead != nil {
		e.mu.Unlock()

		return e.dead
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
		return fmt.Errorf("katago: Senden fehlgeschlagen: %w", err)
	}

	for received := 0; received < len(turns); received++ {
		res, ok := <-ch

		if !ok {
			e.mu.Lock()
			err := e.dead
			e.mu.Unlock()

			return fmt.Errorf("katago: Verbindung verloren: %w", err)
		}

		if res.ErrorMsg != "" {
			return fmt.Errorf("katago: Engine-Fehler: %s", res.ErrorMsg)
		}

		if res.Warning != "" {
			fmt.Fprintf(os.Stderr, "katago: Warnung: %s\n", res.Warning)
		}

		if err := emit(res); err != nil {
			return err
		}
	}

	return nil
}

// Close beendet die Engine. Der Prozess wird bewusst per Kill beendet;
// das daraus resultierende "signal: killed" von Wait ist der Normalfall
// und kein Fehler — sonst loggt jede Anfrage eine Pseudo-Fehlerzeile.
func (e *Engine) Close() error {
	if e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
	}

	err := e.cmd.Wait()

	// ExitCode -1 = durch Signal beendet (robuster als der
	// plattformabhängige Fehlertext "signal: killed").
	var exit *exec.ExitError

	if errors.As(err, &exit) && exit.ExitCode() == -1 {
		return nil
	}

	return err
}
