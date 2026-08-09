// Package teaching erzeugt für JEDEN Zug einer Partie eine Lehreinheit
// ("Teaching pro Zug", Stufe 6 der Architektur): Bewertungskategorie,
// Punktverlust, Gewinnchancen-Verlauf, Engine-Erstwahl mit Variante,
// Auswirkungen auf betroffene Gruppen (situative Stärke, Atari, Schlagen,
// Benson-Status) sowie einen deutschen Lehrtext.
//
// Halluzinationsschutz: Der Lehrtext wird ausschließlich aus verifizierten
// Zahlen (KataGo/Benson/Brettlogik) per Template gebaut. Ein optionaler
// LLM-Feinschliff darf nur umformulieren, nie neue Zahlen oder Züge erfinden
// (siehe llm.go); der verifizierte Basistext bleibt stets erhalten.
package teaching

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/vmanke/goteach-prod/board"
	"github.com/vmanke/goteach-prod/groups"
	"github.com/vmanke/goteach-prod/katago"
	"github.com/vmanke/goteach-prod/strength"
)

// Options steuert die Analyse.
type Options struct {
	Visits   int
	Tau      float64
	Rules    string   // leer = aus SGF ableiten, Fallback "chinese"
	Komi     *float64 // nil = aus SGF
	From     int      // erster Zug (1-basiert), 0 = 1
	To       int      // letzter Zug (inklusive), 0 = Partieende
	Progress bool

	// Polish ist ein optionaler LLM-Feinschliff pro Zug (nur Umformulierung).
	Polish func(*MoveReport) (string, error)

	// PolishStrand ist derselbe Feinschliff für Erzählstränge. Er kostet
	// einen Aufruf je Strang statt je Zug — bei einer typischen Partie eine
	// Handvoll statt einiger hundert.
	PolishStrand func(*Strand) (string, error)

	// RefineVisits rechnet die stärksten Erzählstränge in einer zweiten
	// Runde mit dieser Visit-Zahl nach. Kleiner oder gleich Visits schaltet
	// die Rückkopplung ab.
	RefineVisits int

	// RefineTop begrenzt, wie viele Stränge nachgerechnet werden (0 = 3).
	RefineTop int
}

// GroupEffect beschreibt die Auswirkung eines Zuges auf eine Kette.
type GroupEffect struct {
	Color          string  `json:"color"`
	Rep            string  `json:"rep"`
	Stones         int     `json:"stones"`
	Liberties      int     `json:"liberties"`
	StrengthBefore float64 `json:"strengthBefore"`
	StrengthAfter  float64 `json:"strengthAfter"`
	New            bool    `json:"new,omitempty"`
	Captured       bool    `json:"captured,omitempty"`
	InAtari        bool    `json:"inAtari,omitempty"`
	UncondAlive    bool    `json:"uncondAlive,omitempty"`
}

// MoveReport ist die Lehreinheit zu genau einem Zug.
type MoveReport struct {
	Number        int           `json:"number"`
	Player        string        `json:"player"`
	Coord         string        `json:"coord"`
	Pass          bool          `json:"pass,omitempty"`
	WinrateBefore float64       `json:"winrateBefore"`
	WinrateAfter  float64       `json:"winrateAfter"`
	PointsLost    float64       `json:"pointsLost"`
	Category      string        `json:"category"`
	BestMove      string        `json:"bestMove,omitempty"`
	BestPV        []string      `json:"bestPV,omitempty"`
	Effects       []GroupEffect `json:"effects,omitempty"`
	Text          string        `json:"text"`
	TextLLM       string        `json:"textLLM,omitempty"`
}

// Analyze erstellt Teaching-Reports für die Züge [From..To] der Partie.
// Perspektiven-Annahme (verbindlich): Die Analysis-Config setzt
// reportAnalysisWinratesAs = BLACK; Winrate/ScoreLead/Ownership sind daher
// aus Schwarz-Sicht und werden hier auf die Sicht des Ziehenden normiert.
func Analyze(g *board.Game, an katago.Analyzer, opt Options) ([]MoveReport, error) {
	report, err := analyzeCore(g, an, opt, false)

	if err != nil {
		return nil, err
	}

	return report.Moves, nil
}

// analyzeCore trägt die gemeinsame Arbeit von Analyze und AnalyzeGame.
//
// Der Unterschied ist allein withStrands: Nur dafür werden die
// Ownership-Felder aufbewahrt, statt nach dem Ableiten der Skalare verworfen
// zu werden. Sie kosten rund 0,7 MB je Partie — ohne sie gäbe es keine
// Analyse über Region und Zeit.
func analyzeCore(g *board.Game, an katago.Analyzer, opt Options,
	withStrands bool) (*GameReport, error) {

	if opt.Tau <= 0 {
		opt.Tau = 3.0
	}

	positions, err := g.Positions()

	if err != nil {
		return nil, err
	}

	n := len(g.Moves)
	from := opt.From

	if from < 1 {
		from = 1
	}

	to := opt.To

	if to < from || to > n {
		to = n
	}

	if n == 0 {
		return nil, fmt.Errorf("teaching: Partie enthält keine Züge")
	}

	req := katago.Request{
		Rules:     rulesString(g.Rules, opt.Rules),
		Komi:      g.Komi,
		Size:      g.Size,
		MaxVisits: opt.Visits,
	}

	if opt.Komi != nil {
		req.Komi = *opt.Komi
	}

	for _, s := range g.Setup {
		req.InitialStones = append(req.InitialStones,
			[2]string{s.Color.String(), board.ToGTP(s.Point, g.Size)})
	}

	for _, m := range g.Moves {
		coord := "pass"

		if !m.Pass {
			coord = board.ToGTP(m.Point, g.Size)
		}

		req.Moves = append(req.Moves, [2]string{m.Color.String(), coord})
	}

	// Eine Query, Stellungen from-1 .. to (jeweils "vor Zug i" und "nach
	// letztem Zug"): Anzahl Analysen = (to-from)+2.
	turns := make([]int, 0, to-from+2)

	for t := from - 1; t <= to; t++ {
		turns = append(turns, t)
	}

	if opt.Progress {
		fmt.Fprintf(os.Stderr,
			"teaching: analysiere %d Stellungen (Visits=%d) ...\n",
			len(turns), opt.Visits)
	}

	analyses, err := an.AnalyzeGame(req, turns)

	if err != nil {
		return nil, err
	}

	if len(analyses) != len(turns) {
		return nil, fmt.Errorf("teaching: %d Analysen erwartet, %d erhalten",
			len(turns), len(analyses))
	}

	nsq := g.Size * g.Size

	for _, a := range analyses {
		if len(a.Ownership) != nsq {
			return nil, fmt.Errorf(
				"teaching: Ownership-Länge %d ≠ %d (Turn %d) — Config prüfen",
				len(a.Ownership), nsq, a.TurnNumber)
		}
	}

	byTurn := map[int]*katago.Result{}

	for _, a := range analyses {
		byTurn[a.TurnNumber] = a
	}

	reports := make([]MoveReport, 0, to-from+1)

	for i := from - 1; i < to; i++ {
		mv := g.Moves[i]
		before := byTurn[i]
		after := byTurn[i+1]
		rep := buildReport(i, mv, g.Size, positions[i], positions[i+1],
			before, after, opt.Tau)

		if opt.Polish != nil {
			if txt, perr := opt.Polish(&rep); perr == nil {
				rep.TextLLM = txt
			} else {
				fmt.Fprintf(os.Stderr,
					"teaching: LLM-Feinschliff Zug %d übersprungen: %v\n",
					rep.Number, perr)
			}
		}

		reports = append(reports, rep)

		if opt.Progress && (i+1)%10 == 0 {
			fmt.Fprintf(os.Stderr, "teaching: Zug %d/%d fertig\n", i+1, to)
		}
	}

	report := &GameReport{
		Size:  g.Size,
		Komi:  req.Komi,
		Rules: req.Rules,
		Moves: reports,
	}

	if withStrands {
		lo := from - 1
		ownership := make([][]float64, 0, len(turns))

		for t := lo; t <= to; t++ {
			a := byTurn[t]

			if a == nil {
				break
			}

			ownership = append(ownership, a.Ownership)
		}

		if opt.Progress {
			fmt.Fprintf(os.Stderr,
				"teaching: suche Erzählstränge über %d Stellungen ...\n",
				len(ownership))
		}

		report.Strands = buildStrands(g, positions, ownership, lo, reports, opt.Tau)

		// Rückkopplung: Die erkannten Stränge bestimmen, wo genauer
		// gerechnet wird. Muss vor dem Feinschliff laufen, damit das LLM die
		// verfeinerten Zahlen sieht.
		if err := refine(g, an, opt, req, positions, report); err != nil {
			return nil, err
		}

		if opt.PolishStrand != nil {
			for i := range report.Strands {
				s := &report.Strands[i]

				if txt, perr := opt.PolishStrand(s); perr == nil {
					s.TextLLM = txt
				} else {
					fmt.Fprintf(os.Stderr,
						"teaching: LLM-Feinschliff Strang %d übersprungen: %v\n",
						s.ID, perr)
				}
			}
		}

		if opt.Progress {
			fmt.Fprintf(os.Stderr, "teaching: %d Stränge gefunden\n",
				len(report.Strands))
		}
	}

	return report, nil
}

func buildReport(i int, mv board.Move, size int, bb, ab *board.Board,
	before, after *katago.Result, tau float64) MoveReport {

	moverBlack := mv.Color == board.Black
	persp := func(wBlack float64) float64 {
		if moverBlack {
			return wBlack
		}

		return 1.0 - wBlack
	}

	sign := 1.0

	if !moverBlack {
		sign = -1.0
	}

	rep := MoveReport{
		Number:        i + 1,
		Player:        playerName(mv.Color),
		Pass:          mv.Pass,
		Coord:         "Pass",
		WinrateBefore: persp(before.RootInfo.Winrate),
		WinrateAfter:  persp(after.RootInfo.Winrate),
		PointsLost:    sign * (before.RootInfo.ScoreLead - after.RootInfo.ScoreLead),
	}

	if !mv.Pass {
		rep.Coord = board.ToGTP(mv.Point, size)
	}

	if best := before.Best(); best != nil {
		rep.BestMove = best.Move
		rep.BestPV = limit(best.PV, 6)
	}

	matchesBest := !mv.Pass && rep.BestMove != "" && rep.BestMove == rep.Coord
	rep.Category = category(rep.PointsLost, matchesBest)

	if !mv.Pass {
		rep.Effects = collectEffects(mv, size, bb, ab,
			before.Ownership, after.Ownership, tau)
	}

	rep.Text = renderText(&rep, matchesBest)

	return rep
}

func collectEffects(mv board.Move, size int, bb, ab *board.Board,
	ownBefore, ownAfter []float64, tau float64) []GroupEffect {

	var out []GroupEffect
	opp := mv.Color.Opponent()

	// 1) Geschlagene gegnerische Ketten (vorher vorhanden, nachher leer).
	for _, ch := range groups.FindChains(bb) {
		if ch.Color != opp {
			continue
		}

		gone := true

		for _, s := range ch.Stones {
			if ab.Get(s) != board.Empty {
				gone = false

				break
			}
		}

		if gone {
			out = append(out, GroupEffect{
				Color:          playerName(ch.Color),
				Rep:            board.ToGTP(ch.Rep(size), size),
				Stones:         len(ch.Stones),
				StrengthBefore: strength.Group(size, ownBefore, ch.Stones, ch.Color, tau),
				StrengthAfter:  -1.0,
				Captured:       true,
			})
		}
	}

	bensonB := groups.UnconditionallyAlive(ab, board.Black)
	bensonW := groups.UnconditionallyAlive(ab, board.White)
	alive := func(ch *groups.Chain) bool {
		set := bensonB

		if ch.Color == board.White {
			set = bensonW
		}

		return set[ch.Stones[0]]
	}

	seen := map[board.Point]bool{}

	addChain := func(ch *groups.Chain) {
		if ch == nil || len(ch.Stones) == 0 {
			return
		}

		rp := ch.Rep(size)

		if seen[rp] {
			return
		}

		seen[rp] = true

		eff := GroupEffect{
			Color:         playerName(ch.Color),
			Rep:           board.ToGTP(rp, size),
			Stones:        len(ch.Stones),
			Liberties:     len(ch.Liberties),
			StrengthAfter: strength.Group(size, ownAfter, ch.Stones, ch.Color, tau),
			InAtari:       len(ch.Liberties) == 1,
			UncondAlive:   alive(ch),
		}

		// Stärke "vorher": über dieselben Steine, soweit sie existierten.
		var existed []board.Point

		for _, s := range ch.Stones {
			if bb.Get(s) == ch.Color {
				existed = append(existed, s)
			}
		}

		if len(existed) == 0 {
			eff.New = true
			eff.StrengthBefore = strength.Group(size, ownBefore,
				[]board.Point{mv.Point}, ch.Color, tau)
		} else {
			eff.StrengthBefore = strength.Group(size, ownBefore,
				existed, ch.Color, tau)
		}

		out = append(out, eff)
	}

	// 2) Eigene Kette des gesetzten Steins.
	if own := groups.ChainAt(ab, mv.Point); own != nil {
		addChain(own)
	}

	// 3) Angrenzende gegnerische Ketten.
	for _, q := range ab.Neighbors(mv.Point) {
		if ab.Get(q) == opp {
			if ch := groups.ChainAt(ab, q); ch != nil {
				addChain(ch)
			}
		}
	}

	// Sortierung: Schlagen zuerst, dann größte Stärkeänderung; max. 4 Einträge.
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Captured != out[b].Captured {
			return out[a].Captured
		}

		da := math.Abs(out[a].StrengthAfter - out[a].StrengthBefore)
		db := math.Abs(out[b].StrengthAfter - out[b].StrengthBefore)

		return da > db
	})

	if len(out) > 4 {
		out = out[:4]
	}

	return out
}

func renderText(r *MoveReport, matchesBest bool) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "Zug %d — %s %s [%s, %+.1f Pkt]. ",
		r.Number, r.Player, r.Coord, r.Category, -r.PointsLost)
	fmt.Fprintf(&sb, "Gewinnchance %s: %.1f %% → %.1f %%.",
		r.Player, 100*r.WinrateBefore, 100*r.WinrateAfter)

	if r.BestMove != "" && !matchesBest {
		fmt.Fprintf(&sb, "\nEngine-Erstwahl: %s", r.BestMove)

		if len(r.BestPV) > 1 {
			fmt.Fprintf(&sb, " (Variante: %s)", strings.Join(r.BestPV, " "))
		}

		sb.WriteString(".")
	}

	for _, e := range r.Effects {
		sb.WriteString("\n")

		switch {
		case e.Captured:
			fmt.Fprintf(&sb,
				"Geschlagen: %se Kette um %s (%d Stein(e), Stärke vorher %.2f).",
				e.Color, e.Rep, e.Stones, e.StrengthBefore)

		default:
			fmt.Fprintf(&sb,
				"%se Kette um %s (%d Stein(e), %d Freiheit(en)): Stärke %.2f → %.2f",
				e.Color, e.Rep, e.Stones, e.Liberties,
				e.StrengthBefore, e.StrengthAfter)

			if e.InAtari {
				sb.WriteString(" — steht im Atari!")
			} else if e.UncondAlive {
				sb.WriteString(" — unbedingt lebendig (Benson).")
			} else {
				sb.WriteString(".")
			}
		}
	}

	fmt.Fprintf(&sb, "\nMerksatz: %s", lesson(r, matchesBest))

	return sb.String()
}

// lesson wählt einen didaktischen Merksatz — ausschließlich auf Basis
// berechneter Fakten, nie spekulativ.
func lesson(r *MoveReport, matchesBest bool) string {
	if r.Pass {
		return "Passen Sie nur, wenn kein Zug mehr Punkte bringt — " +
			"prüfen Sie offene Randgebiete und Ko-Drohungen."
	}

	var ownAtari, oppAtari, captured bool
	var weakenedOwn *GroupEffect

	for i := range r.Effects {
		e := &r.Effects[i]

		if e.Captured {
			captured = true
		}

		if e.InAtari {
			if e.Color == r.Player {
				ownAtari = true
			} else {
				oppAtari = true
			}
		}

		if e.Color == r.Player && !e.New &&
			e.StrengthBefore-e.StrengthAfter >= 0.15 {
			weakenedOwn = e
		}
	}

	switch {
	case ownAtari:
		return "Ihre Kette steht nach diesem Zug im Atari — zählen Sie " +
			"die Freiheiten VOR dem Setzen, nicht danach."

	case captured:
		return "Geschlagene Steine sichern Freiheiten und Augenraum — " +
			"prüfen Sie, ob der Gewinn den Tempoverlust wert war."

	case oppAtari:
		return "Atari allein tötet nicht — lesen Sie Leiter und " +
			"Verbindung bis zum Ende, bevor Sie jagen."

	case weakenedOwn != nil:
		return fmt.Sprintf("Der Zug schwächt Ihre Kette um %s (Δ %.2f) — "+
			"Stabilität der eigenen Gruppen geht vor Territorium.",
			weakenedOwn.Rep, weakenedOwn.StrengthAfter-weakenedOwn.StrengthBefore)

	case matchesBest:
		return "Deckungsgleich mit der Engine-Erstwahl — merken Sie sich " +
			"die Form dieser Stellung."

	case r.PointsLost > 6:
		return fmt.Sprintf("Große Verluste entstehen meist, weil der größte "+
			"Punkt übersehen wird — vergleichen Sie Ihren Zug mit %s.",
			r.BestMove)

	case r.PointsLost > 1.5:
		return "Fragen Sie vor jedem Zug: Was ist die dringendste Gruppe, " +
			"was der größte Punkt? Dringend schlägt groß."

	default:
		return "Solide. Beobachten Sie, wie sich die Ownership in der " +
			"Umgebung des Zuges verschiebt."
	}
}

func category(pointsLost float64, matchesBest bool) string {
	switch {
	case matchesBest || pointsLost <= 0.5:
		return "ausgezeichnet"

	case pointsLost <= 1.5:
		return "gut"

	case pointsLost <= 3.0:
		return "Ungenauigkeit"

	case pointsLost <= 6.0:
		return "Fehler"

	default:
		return "grober Fehler"
	}
}

func playerName(c board.Color) string {
	if c == board.Black {
		return "Schwarz"
	}

	return "Weiß"
}

func rulesString(fromSGF, override string) string {
	if override != "" {
		return override
	}

	s := strings.ToLower(fromSGF)

	switch {
	case strings.Contains(s, "jap"):
		return "japanese"

	case strings.Contains(s, "kor"):
		return "korean"

	case strings.Contains(s, "aga"):
		return "aga"

	case strings.Contains(s, "tromp"):
		return "tromp-taylor"

	case strings.Contains(s, "new"):
		return "new-zealand"

	default:
		return "chinese"
	}
}

func limit(s []string, n int) []string {
	if len(s) <= n {
		return s
	}

	return s[:n]
}
