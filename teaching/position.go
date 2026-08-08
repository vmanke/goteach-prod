package teaching

import (
	"fmt"
	"strings"

	"github.com/vmanke/goteach-prod/board"
	"github.com/vmanke/goteach-prod/groups"
	"github.com/vmanke/goteach-prod/katago"
	"github.com/vmanke/goteach-prod/strength"
)

// GroupState beschreibt eine Kette in einer Stellung ohne Zughistorie.
type GroupState struct {
	Color       string  `json:"color"`
	Rep         string  `json:"rep"`
	Stones      int     `json:"stones"`
	Liberties   int     `json:"liberties"`
	Strength    float64 `json:"strength"`
	InAtari     bool    `json:"inAtari,omitempty"`
	UncondAlive bool    `json:"uncondAlive,omitempty"`
}

// PositionReport ist die Auswertung einer einzelnen Stellung.
//
// Winrate und ScoreLead stehen in Schwarz-Sicht — anders als in MoveReport,
// wo auf die Sicht des Ziehenden normiert wird. Ohne Zug gibt es niemanden,
// auf den normiert werden könnte; die Schwarz-Sicht ist die Perspektive, in
// der KataGo liefert (reportAnalysisWinratesAs = BLACK).
type PositionReport struct {
	Size      int          `json:"size"`
	Komi      float64      `json:"komi"`
	Rules     string       `json:"rules"`
	Stones    int          `json:"stones"`
	Winrate   float64      `json:"winrate"`
	ScoreLead float64      `json:"scoreLead"`
	BestMove  string       `json:"bestMove,omitempty"`
	BestPV    []string     `json:"bestPV,omitempty"`
	Groups    []GroupState `json:"groups,omitempty"`
	Text      string       `json:"text"`
}

// AnalyzePosition wertet eine Stellung ohne Zughistorie aus — die Form, in
// der die Bilderkennung (Paket vision) ihr Ergebnis liefert.
//
// Analyze kann das nicht leisten: Es baut Lehreinheiten *pro Zug* und lehnt
// eine Partie ohne Züge folgerichtig ab. Ein erkanntes Foto hat aber keine
// Historie, nur gesetzte Steine.
func AnalyzePosition(g *board.Game, an katago.Analyzer, opt Options) (
	*PositionReport, error) {

	if opt.Tau <= 0 {
		opt.Tau = 3.0
	}

	if len(g.Moves) > 0 {
		return nil, fmt.Errorf(
			"teaching: AnalyzePosition erwartet eine Stellung ohne Züge (%d gefunden)",
			len(g.Moves))
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

	analyses, err := an.AnalyzeGame(req, []int{0})

	if err != nil {
		return nil, err
	}

	if len(analyses) != 1 {
		return nil, fmt.Errorf("teaching: 1 Analyse erwartet, %d erhalten",
			len(analyses))
	}

	result := analyses[0]
	nsq := g.Size * g.Size

	if len(result.Ownership) != nsq {
		return nil, fmt.Errorf(
			"teaching: Ownership-Länge %d ≠ %d — Config prüfen",
			len(result.Ownership), nsq)
	}

	positions, err := g.Positions()

	if err != nil {
		return nil, err
	}

	b := positions[0]

	rep := &PositionReport{
		Size:      g.Size,
		Komi:      req.Komi,
		Rules:     req.Rules,
		Stones:    len(g.Setup),
		Winrate:   result.RootInfo.Winrate,
		ScoreLead: result.RootInfo.ScoreLead,
		Groups:    groupStates(b, g.Size, result.Ownership, opt.Tau),
	}

	if best := result.Best(); best != nil {
		rep.BestMove = best.Move
		rep.BestPV = limit(best.PV, 6)
	}

	rep.Text = positionText(rep)

	return rep, nil
}

// groupStates beschreibt jede Kette der Stellung; Reihenfolge wie
// groups.FindChains, also deterministisch in Scanreihenfolge.
func groupStates(b *board.Board, size int, ownership []float64,
	tau float64) []GroupState {

	bensonB := groups.UnconditionallyAlive(b, board.Black)
	bensonW := groups.UnconditionallyAlive(b, board.White)

	chains := groups.FindChains(b)
	out := make([]GroupState, 0, len(chains))

	for _, ch := range chains {
		alive := bensonB

		if ch.Color == board.White {
			alive = bensonW
		}

		out = append(out, GroupState{
			Color:       playerName(ch.Color),
			Rep:         board.ToGTP(ch.Rep(size), size),
			Stones:      len(ch.Stones),
			Liberties:   len(ch.Liberties),
			Strength:    strength.Group(size, ownership, ch.Stones, ch.Color, tau),
			InAtari:     len(ch.Liberties) == 1,
			UncondAlive: alive[ch.Stones[0]],
		})
	}

	return out
}

// positionText baut den deutschen Lehrtext ausschließlich aus verifizierten
// Zahlen — dieselbe Halluzinationssperre wie beim Teaching pro Zug.
func positionText(rep *PositionReport) string {
	var sb strings.Builder

	leader, lead := "Schwarz", rep.ScoreLead

	if lead < 0 {
		leader, lead = "Weiß", -lead
	}

	fmt.Fprintf(&sb, "Stellung auf %d×%d mit %d Steinen, Komi %.1f (%s). ",
		rep.Size, rep.Size, rep.Stones, rep.Komi, rep.Rules)
	fmt.Fprintf(&sb, "%s führt mit %.1f Punkten; Gewinnchance Schwarz %.0f%%. ",
		leader, lead, 100*rep.Winrate)

	if rep.BestMove != "" {
		fmt.Fprintf(&sb, "Engine-Erstwahl: %s", rep.BestMove)

		if len(rep.BestPV) > 1 {
			fmt.Fprintf(&sb, " (Variante %s)", strings.Join(rep.BestPV, " "))
		}

		sb.WriteString(". ")
	}

	var atari, alive int

	for _, g := range rep.Groups {
		if g.InAtari {
			atari++
		}

		if g.UncondAlive {
			alive++
		}
	}

	fmt.Fprintf(&sb, "%d Ketten", len(rep.Groups))

	if alive > 0 {
		fmt.Fprintf(&sb, ", davon %d unbedingt lebendig (Benson)", alive)
	}

	if atari > 0 {
		fmt.Fprintf(&sb, ", %d im Atari", atari)
	}

	sb.WriteString(".")

	return sb.String()
}
