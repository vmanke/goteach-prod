package teaching

import (
	"strings"
	"testing"

	"github.com/vmanke/goteach-prod/board"
	"github.com/vmanke/goteach-prod/katago"
)

// mkReport baut einen MoveReport mit ROSE-Detail für Compose-Tests. Die
// Details sind synthetisch — composeTexts liest nur Fakten, keine Bretter.
func mkReport(number int, coord, category string, facts RoseFacts,
	d roseDetail) MoveReport {

	player := "Schwarz"

	if number%2 == 0 {
		player = "Weiß"
	}

	detail := d

	return MoveReport{
		Number:   number,
		Player:   player,
		Coord:    coord,
		Category: category,
		Rose:     &facts,
		rose:     &detail,
	}
}

// countLines zählt Zeilen aller Texte, die substr enthalten.
func countLines(reports []MoveReport, substr string) int {
	n := 0

	for _, r := range reports {
		for _, line := range strings.Split(r.Text, "\n") {
			if strings.Contains(line, substr) {
				n++
			}
		}
	}

	return n
}

// Derselbe O-Befund schweigt innerhalb des 5-Züge-Fensters: bei zwölf
// Wiederholungen bleiben genau drei Nennungen (Zug 1, 6, 11).
func TestComposeDedupWindow(t *testing.T) {
	coords := []string{"A1", "B2", "C3", "D4", "E5", "F6",
		"G7", "H8", "J9", "A2", "B3", "C4"}
	reports := make([]MoveReport, 0, len(coords))

	for i, coord := range coords {
		reports = append(reports, mkReport(i+1, coord, "gut",
			RoseFacts{Played: "O"},
			roseDetail{
				bucketBest: -1,
				findings: []roseFinding{{
					bucket: roseO, rep: "G7", color: "Weiß",
					stones: 2, libs: 3, strength: -0.2,
				}},
			}))
	}

	composeTexts(reports)

	if got := countLines(reports, "O wie Offense"); got != 3 {
		t.Fatalf("O-Befund %d-mal genannt, erwartet 3 (Fenster %d)",
			got, dedupWindow)
	}

	for _, r := range reports {
		if strings.TrimSpace(r.Text) == "" {
			t.Fatalf("leerer Text in Zug %d", r.Number)
		}
	}
}

// R-Atari wird nie unterdrückt — auch bei direkter Wiederholung.
func TestComposeAtariNeverSuppressed(t *testing.T) {
	reports := make([]MoveReport, 0, 4)

	for i := 0; i < 4; i++ {
		reports = append(reports, mkReport(i+1, "E6", "Fehler",
			RoseFacts{Played: "E", Urgent: true},
			roseDetail{
				bucketBest: -1,
				findings: []roseFinding{{
					bucket: roseR, rep: "E5", color: "Schwarz",
					stones: 2, libs: 1, atari: true,
				}},
			}))
	}

	composeTexts(reports)

	if got := countLines(reports, "R wie Respond"); got != 4 {
		t.Fatalf("R-Atari-Befund %d-mal genannt, erwartet 4", got)
	}
}

// Die goldene Regel hält bei Ungenauigkeiten mindestens goldenGap Züge
// Abstand; die übrigen Züge bekommen den nüchternen Vergleichssatz.
func TestComposeGoldenRuleSpacing(t *testing.T) {
	reports := make([]MoveReport, 0, 20)

	for i := 0; i < 20; i++ {
		rep := mkReport(i+1, "K10", "Ungenauigkeit",
			RoseFacts{Played: "E", Best: "R", Urgent: true},
			roseDetail{
				bucketBest: roseR,
				prevCoord:  "D5",
				findings: []roseFinding{{
					bucket: roseR, rep: "D5", color: "Schwarz",
					stones: 2, libs: 2, strength: 0.1,
				}},
			})
		rep.BestMove = "E6"
		reports = append(reports, rep)
	}

	composeTexts(reports)

	var goldenAt []int

	for _, r := range reports {
		if strings.Contains(r.Text, "Merkregel: Dringlichkeit geht vor Größe") {
			goldenAt = append(goldenAt, r.Number)
		}
	}

	if len(goldenAt) != 3 {
		t.Fatalf("goldene Regel bei %v, erwartet 3 Nennungen (1, 9, 17)",
			goldenAt)
	}

	for i := 1; i < len(goldenAt); i++ {
		if goldenAt[i]-goldenAt[i-1] < goldenGap {
			t.Fatalf("goldene Regel zu dicht: %v", goldenAt)
		}
	}

	// Ohne Regel bleibt der Vergleich trotzdem in jedem Text.
	for _, r := range reports {
		if !strings.Contains(r.Text, "Erstwahl") &&
			!strings.Contains(r.Text, "Merkregel") {
			t.Fatalf("Zug %d ohne Urteil:\n%s", r.Number, r.Text)
		}
	}
}

// Ab Kategorie Fehler bricht die goldene Regel den Abstand.
func TestComposeGoldenRuleSeverityOverridesGap(t *testing.T) {
	reports := make([]MoveReport, 0, 2)

	for i := 0; i < 2; i++ {
		rep := mkReport(i+1, "K10", "grober Fehler",
			RoseFacts{Played: "E", Best: "R", Urgent: true},
			roseDetail{
				bucketBest: roseR,
				findings: []roseFinding{{
					bucket: roseR, rep: "D5", color: "Schwarz",
					stones: 2, libs: 2, strength: 0.1,
				}},
			})
		rep.BestMove = "E6"
		reports = append(reports, rep)
	}

	composeTexts(reports)

	if got := countLines(reports, "Merkregel:"); got != 2 {
		t.Fatalf("goldene Regel %d-mal, erwartet 2 (Kategorie bricht Abstand)",
			got)
	}
}

// Abschluss-Formulierungen: jede höchstens zweimal, danach Stille.
func TestComposeCloserPools(t *testing.T) {
	reports := make([]MoveReport, 0, 12)

	for i := 0; i < 12; i++ {
		reports = append(reports, mkReport(i+1, "K10", "Ungenauigkeit",
			RoseFacts{Played: "E"},
			roseDetail{bucketBest: -1}))
	}

	composeTexts(reports)

	for _, pool := range closers {
		for _, closer := range pool {
			if got := countLines(reports, closer); got > closerMaxUse {
				t.Fatalf("Abschluss %q %d-mal, erlaubt %d",
					closer, got, closerMaxUse)
			}
		}
	}
}

// Pass-Texte: Alarm unter Not, sonst einmal Einordnung, danach knapp.
func TestComposePassTexts(t *testing.T) {
	urgent := mkReport(1, "Pass", "grober Fehler",
		RoseFacts{Played: "E", Urgent: true},
		roseDetail{
			bucketBest: -1,
			findings: []roseFinding{{
				bucket: roseR, rep: "E5", color: "Schwarz",
				stones: 2, libs: 1, atari: true,
			}},
		})
	urgent.Pass = true

	quiet1 := mkReport(2, "Pass", "ausgezeichnet",
		RoseFacts{Played: "E"}, roseDetail{bucketBest: -1})
	quiet1.Pass = true

	quiet2 := mkReport(3, "Pass", "ausgezeichnet",
		RoseFacts{Played: "E"}, roseDetail{bucketBest: -1})
	quiet2.Pass = true

	reports := []MoveReport{urgent, quiet1, quiet2}
	composeTexts(reports)

	if !strings.Contains(reports[0].Text, "Passen, während") {
		t.Fatalf("Pass unter Not ohne Alarm:\n%s", reports[0].Text)
	}

	if !strings.Contains(reports[1].Text, "Pass — vertretbar") {
		t.Fatalf("erster ruhiger Pass ohne Einordnung:\n%s", reports[1].Text)
	}

	if reports[2].Text == reports[1].Text {
		t.Fatal("zweiter ruhiger Pass wiederholt die Einordnung")
	}
}

// Ketten-Prosa nur bei Ereignis: Schlagen, Atari, Benson-Übergang,
// Stärkesprung und Freiheitsnot ja — kleine Drift nein; höchstens zwei
// Sätze je Zug.
func TestChainProseGates(t *testing.T) {
	r := &MoveReport{Effects: []GroupEffect{
		{Color: "Weiß", Rep: "Q10", Stones: 3, Captured: true},
		{Color: "Schwarz", Rep: "D4", Stones: 2, InAtari: true},
		{Color: "Schwarz", Rep: "C3", Stones: 5,
			UncondAlive: true, UncondAliveBefore: false},
	}}

	if got := chainProse(r); len(got) != maxChainProse {
		t.Fatalf("%d Ketten-Sätze, erwartet Kappung bei %d: %v",
			len(got), maxChainProse, got)
	}

	drift := &MoveReport{Effects: []GroupEffect{
		{Color: "Weiß", Rep: "Q10", Stones: 3, Liberties: 4,
			StrengthBefore: 0.30, StrengthAfter: 0.40},
	}}

	if got := chainProse(drift); len(got) != 0 {
		t.Fatalf("Stärke-Drift 0.10 erzeugt Prosa: %v", got)
	}

	jump := &MoveReport{Effects: []GroupEffect{
		{Color: "Weiß", Rep: "Q10", Stones: 3, Liberties: 4,
			StrengthBefore: 0.10, StrengthAfter: 0.40},
	}}

	if got := chainProse(jump); len(got) != 1 {
		t.Fatalf("Stärkesprung 0.30 ohne Prosa: %v", got)
	}

	lowLibs := &MoveReport{Effects: []GroupEffect{
		{Color: "Schwarz", Rep: "D4", Stones: 2, Liberties: 2,
			StrengthBefore: 0.2, StrengthAfter: 0.2},
	}}

	if got := chainProse(lowLibs); len(got) != 1 {
		t.Fatalf("2 Freiheiten ohne Prosa: %v", got)
	}

	alreadyAlive := &MoveReport{Effects: []GroupEffect{
		{Color: "Schwarz", Rep: "C3", Stones: 5, Liberties: 8,
			UncondAlive: true, UncondAliveBefore: true,
			StrengthBefore: 0.9, StrengthAfter: 0.9},
	}}

	if got := chainProse(alreadyAlive); len(got) != 0 {
		t.Fatalf("Benson ohne Übergang erzeugt Prosa: %v", got)
	}
}

// Regression auf der Demo-Partie: kein Satz wiederholt sich wörtlich mehr
// als dreimal, und kein Text ist leer.
func TestComposeDemoGameRepetition(t *testing.T) {
	g, err := board.ParseSGF(demoSGF)

	if err != nil {
		t.Fatal(err)
	}

	reports, err := Analyze(g, katago.Mock{}, Options{Visits: 1, Tau: 3.0})

	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]int{}

	for _, r := range reports {
		if strings.TrimSpace(r.Text) == "" {
			t.Fatalf("leerer Text in Zug %d", r.Number)
		}

		for _, line := range strings.Split(r.Text, "\n") {
			seen[line]++

			if seen[line] > 3 {
				t.Fatalf("Satz mehr als dreimal wiederholt: %q", line)
			}
		}
	}
}

// "Stärke -0.00" ist keine Aussage über die Stellung, sondern eine
// Eigenheit der Fließkommadarstellung — sie darf nicht im Text landen.
func TestChainProseHasNoNegativeZero(t *testing.T) {
	r := &MoveReport{Effects: []GroupEffect{
		{Color: "Weiß", Rep: "D4", Stones: 1, Liberties: 4,
			StrengthBefore: -0.0004, StrengthAfter: 0.38},
	}}

	got := chainProse(r)

	if len(got) != 1 {
		t.Fatalf("%d Sätze, erwartet 1: %v", len(got), got)
	}

	if strings.Contains(got[0], "-0.00") {
		t.Fatalf("negative Null im Text: %q", got[0])
	}
}
