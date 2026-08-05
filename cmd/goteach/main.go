// goteach analysiert eine SGF-Partie mit KataGo und erzeugt für jeden Zug
// eine deutsche Lehreinheit ("Teaching pro Zug").
//
// Beispiele:
//
//	goteach -sgf partie.sgf -katago /usr/local/bin/katago \
//	        -model kata.bin.gz -config analysis.cfg -visits 200
//	goteach -sgf demo/demo.sgf -mock            # Offline-Demo ohne Engine
//	goteach -sgf partie.sgf ... -json out.json  # zusätzlich JSON-Export
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/vmanke/goteach/board"
	"github.com/vmanke/goteach/internal/dotenv"
	"github.com/vmanke/goteach/katago"
	"github.com/vmanke/goteach/teaching"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "goteach: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		sgfPath    = flag.String("sgf", "", "Pfad zur SGF-Datei (erforderlich)")
		katagoPath = flag.String("katago", "katago", "Pfad zur KataGo-Binary")
		modelPath  = flag.String("model", "", "Pfad zum KataGo-Netz (.bin.gz)")
		configPath = flag.String("config", "analysis.cfg",
			"Analysis-Config (MUSS reportAnalysisWinratesAs = BLACK setzen)")
		visits = flag.Int("visits", 200, "maxVisits pro Stellung (Kostenfaktor!)")
		tau    = flag.Float64("tau", 3.0, "Abklinglänge der Gruppenstärke")
		rules  = flag.String("rules", "", "Regelwerk-Override (chinese, japanese, ...)")
		komi   = flag.Float64("komi", math.NaN(), "Komi-Override")
		from   = flag.Int("from", 1, "erster zu analysierender Zug (1-basiert)")
		to     = flag.Int("to", 0, "letzter Zug (0 = Partieende)")
		mock   = flag.Bool("mock", false,
			"Mock-Analyzer statt KataGo (SYNTHETISCHE Werte, nur Demo/Tests)")
		jsonOut  = flag.String("json", "", "Reports zusätzlich als JSON schreiben")
		useLLM   = flag.Bool("llm", false, "LLM-Feinschliff (ANTHROPIC_API_KEY aus .env)")
		llmModel = flag.String("llm-model", "claude-sonnet-4-6", "Modell für -llm")
	)

	flag.Parse()

	if *sgfPath == "" {
		flag.Usage()

		return fmt.Errorf("-sgf fehlt")
	}

	// Secrets ausschließlich aus Umgebung/.env (nie als Flag).
	if err := dotenv.Load(".env"); err != nil {
		return fmt.Errorf(".env: %w", err)
	}

	data, err := os.ReadFile(*sgfPath)

	if err != nil {
		return err
	}

	game, err := board.ParseSGF(string(data))

	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr,
		"goteach: %d Züge, Brett %d×%d, Komi %.1f, Regeln %q\n",
		len(game.Moves), game.Size, game.Size, game.Komi, game.Rules)

	var an katago.Analyzer

	if *mock {
		fmt.Fprintln(os.Stderr,
			"╔══════════════════════════════════════════════════════════╗\n"+
				"║  MOCK-MODUS: SYNTHETISCHE WERTE — KEINE ECHTE ANALYSE.   ║\n"+
				"║  Nur für Pipeline-Demo/Tests. Für Teaching KataGo nutzen.║\n"+
				"╚══════════════════════════════════════════════════════════╝")

		an = katago.Mock{}
	} else {
		if *modelPath == "" {
			return fmt.Errorf("-model fehlt (oder -mock für die Offline-Demo nutzen)")
		}

		eng, err := katago.Start(*katagoPath, *modelPath, *configPath)

		if err != nil {
			return err
		}

		an = eng
		est := float64(*visits) * float64(len(game.Moves)+1)
		fmt.Fprintf(os.Stderr,
			"goteach: Kostenhinweis: ≈ %.0f Visits gesamt (Visits × Stellungen); "+
				"-visits/-from/-to zum Begrenzen.\n", est)
	}

	defer an.Close()

	opt := teaching.Options{
		Visits:   *visits,
		Tau:      *tau,
		Rules:    *rules,
		From:     *from,
		To:       *to,
		Progress: true,
	}

	if !math.IsNaN(*komi) {
		opt.Komi = komi
	}

	if *useLLM {
		key := os.Getenv("ANTHROPIC_API_KEY")

		if key == "" {
			return fmt.Errorf("-llm gesetzt, aber ANTHROPIC_API_KEY fehlt (.env)")
		}

		fmt.Fprintf(os.Stderr,
			"goteach: LLM-Feinschliff aktiv (%s) — Kostenhinweis: 1 API-Call pro Zug.\n",
			*llmModel)
		opt.Polish = teaching.NewAnthropicPolisher(key, *llmModel)
	}

	reports, err := teaching.Analyze(game, an, opt)

	if err != nil {
		return err
	}

	for i := range reports {
		r := &reports[i]
		fmt.Println(r.Text)

		if r.TextLLM != "" {
			fmt.Printf("Lehrtext (LLM): %s\n", r.TextLLM)
		}

		fmt.Println()
	}

	if *jsonOut != "" {
		buf, err := json.MarshalIndent(reports, "", "  ")

		if err != nil {
			return err
		}

		if err := os.WriteFile(*jsonOut, buf, 0o644); err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "goteach: JSON-Report → %s\n", *jsonOut)
	}

	printSummary(reports)

	return nil
}

// printSummary druckt eine kompakte Partie-Bilanz je Spieler.
func printSummary(reports []teaching.MoveReport) {
	if len(reports) == 0 {
		return
	}

	categories := []string{
		"ausgezeichnet", "gut", "Ungenauigkeit", "Fehler", "grober Fehler",
	}

	type agg struct {
		counts map[string]int
		lost   float64
		n      int
	}

	stats := map[string]*agg{}

	for i := range reports {
		r := &reports[i]
		a := stats[r.Player]

		if a == nil {
			a = &agg{counts: map[string]int{}}
			stats[r.Player] = a
		}

		a.counts[r.Category]++
		a.lost += r.PointsLost
		a.n++
	}

	fmt.Println("Zusammenfassung")
	fmt.Println("---------------")

	for _, player := range []string{"Schwarz", "Weiß"} {
		a := stats[player]

		if a == nil {
			continue
		}

		parts := make([]string, 0, len(categories))

		for _, c := range categories {
			if a.counts[c] > 0 {
				parts = append(parts, fmt.Sprintf("%s ×%d", c, a.counts[c]))
			}
		}

		fmt.Printf("%s: %s | Ø Punktverlust %.2f\n",
			player, strings.Join(parts, ", "), a.lost/float64(a.n))
	}
}
