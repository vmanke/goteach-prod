package teaching

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
)

// loadPartieFixture liest die Zug-Reports einer echten, mit KataGo
// analysierten Partie (292 Züge) — auf das reduziert, was die
// Baustellen-Suche braucht.
func loadPartieFixture(t *testing.T) []MoveReport {
	t.Helper()

	data, err := os.ReadFile("testdata/baustellen-partie.json")

	if err != nil {
		t.Fatalf("Fixture: %v", err)
	}

	var fixture struct {
		Size  int          `json:"size"`
		Moves []MoveReport `json:"moves"`
	}

	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("Fixture unlesbar: %v", err)
	}

	if fixture.Size != 19 || len(fixture.Moves) != 292 {
		t.Fatalf("Fixture: Brett %d, %d Züge", fixture.Size, len(fixture.Moves))
	}

	return fixture.Moves
}

func TestBaustellenDerBeispielpartie(t *testing.T) {
	reports := loadPartieFixture(t)
	found := findBaustellen(19, reports)

	// Die erwarteten Werte stammen aus einer unabhängigen Auswertung
	// derselben Partie (Python über das JSON) und wurden von Hand geprüft.
	want := []struct {
		player, point   string
		from, to        int
		hits, near, own int
		resolution      int
		resolvedBy      string
		counterpart     int
	}{
		{"Schwarz", "G2", 61, 83, 11, 0, 12, 101, "selbst", 5},
		{"Schwarz", "C2", 105, 153, 20, 0, 25, 205, "selbst", 0},
		{"Schwarz", "S19", 181, 233, 25, 0, 27, 237, "selbst", 6},
		{"Schwarz", "A8", 253, 273, 7, 0, 11, 277, "selbst", 7},
		{"Weiß", "F4", 54, 88, 15, 0, 18, 102, "selbst", 1},
		{"Weiß", "R19", 198, 236, 19, 4, 20, 237, "Gegner", 3},
		{"Weiß", "A8", 244, 276, 14, 0, 17, 277, "Gegner", 4},
	}

	if len(found) != len(want) {
		for _, b := range found {
			t.Logf("gefunden: %s %s %d..%d (%d/%d)", b.Player, b.Point,
				b.FromMove, b.ToMove, b.Hits, b.OwnMoves)
		}

		t.Fatalf("%d Baustellen, erwartet %d", len(found), len(want))
	}

	for i, w := range want {
		b := found[i]

		if b.ID != i+1 {
			t.Errorf("Baustelle %d: ID %d", i+1, b.ID)
		}

		if b.Player != w.player || b.Point != w.point || b.FromMove != w.from ||
			b.ToMove != w.to {
			t.Errorf("Baustelle %d: %s %s %d..%d, erwartet %s %s %d..%d",
				b.ID, b.Player, b.Point, b.FromMove, b.ToMove,
				w.player, w.point, w.from, w.to)
		}

		if b.Hits != w.hits || b.NearHits != w.near || b.OwnMoves != w.own {
			t.Errorf("Baustelle %d: %d Treffer (%d daneben) von %d, erwartet %d (%d) von %d",
				b.ID, b.Hits, b.NearHits, b.OwnMoves, w.hits, w.near, w.own)
		}

		if b.Resolution == nil || b.Resolution.Number != w.resolution ||
			b.ResolvedBy != w.resolvedBy {
			t.Errorf("Baustelle %d: Auflösung %+v (%q), erwartet Zug %d (%q)",
				b.ID, b.Resolution, b.ResolvedBy, w.resolution, w.resolvedBy)
		}

		if b.Counterpart != w.counterpart {
			t.Errorf("Baustelle %d: Gegenstück %d, erwartet %d",
				b.ID, b.Counterpart, w.counterpart)
		}
	}
}

func TestBaustellenPunkteSindNachrechenbar(t *testing.T) {
	reports := loadPartieFixture(t)
	byNumber := map[int]*MoveReport{}

	for i := range reports {
		byNumber[reports[i].Number] = &reports[i]
	}

	for _, b := range findBaustellen(19, reports) {
		var sum float64
		hits := 0

		point, _, err := boardPoint(b.Point)

		if err != nil {
			t.Fatal(err)
		}

		for n := b.FromMove; n <= b.ToMove; n++ {
			r := byNumber[n]

			if r == nil || r.Player != b.Player || r.BestMove == "" {
				continue
			}

			best, _, err := boardPoint(r.BestMove)

			if err != nil || !near(best, point, baustelleReach) {
				continue
			}

			hits++
			sum += r.PointsLost
		}

		if hits != b.Hits {
			t.Errorf("Baustelle %d: %d Treffer nachgerechnet, %d gemeldet",
				b.ID, hits, b.Hits)
		}

		if math.Abs(sum-b.PointsLost) > 1e-9 {
			t.Errorf("Baustelle %d: Summe %.3f nachgerechnet, %.3f gemeldet",
				b.ID, sum, b.PointsLost)
		}

		// Der Spieler hat den Punkt im Fenster nie besetzt.
		for n := b.FromMove; n <= b.ToMove; n++ {
			if r := byNumber[n]; r != nil && r.Coord == b.Point {
				t.Errorf("Baustelle %d: Zug %d liegt auf dem Punkt", b.ID, n)
			}
		}
	}
}

func TestBaustellenTextNenntNurGezaehltes(t *testing.T) {
	reports := loadPartieFixture(t)
	found := findBaustellen(19, reports)

	if len(found) < 6 {
		t.Fatalf("nur %d Baustellen", len(found))
	}

	first := found[0].Text

	for _, want := range []string{
		"Von Zug 61 bis 83 nannte die Engine für Schwarz den Punkt G2 (unten in der Mitte) in 11 von 12 eigenen Zügen als Erstwahl.",
		"Schwarz spielte in dieser Zeit nie dort.",
		"kosteten zusammen 67.3 Punkte",
		"Beendet hat es Schwarz selbst mit Zug 101 auf G2.",
		"Gegenstück: Baustelle 5 der anderen Seite am selben Ort.",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("Text der ersten Baustelle ohne %q:\n%s", want, first)
		}
	}

	sixth := found[5].Text

	for _, want := range []string{
		"den Punkt R19 (oben rechts) in 19 von 20 eigenen Zügen",
		"Davon lag die Erstwahl 4-mal auf einem Nachbarpunkt.",
		"Zugegriffen hat am Ende Schwarz mit Zug 237 auf S19.",
	} {
		if !strings.Contains(sixth, want) {
			t.Errorf("Text der sechsten Baustelle ohne %q:\n%s", want, sixth)
		}
	}
}

func TestBaustelleOhneAufloesung(t *testing.T) {
	// Acht eigene Züge, immer dieselbe Erstwahl, nie dort gespielt, kein
	// Zug in der Nähe — die Baustelle bleibt bis zum Ende offen.
	var reports []MoveReport

	for n := 1; n <= 16; n++ {
		r := MoveReport{Number: n, Player: "Weiß", Coord: "A1", PointsLost: 1}

		if n%2 == 1 {
			r.Player = "Schwarz"
			r.Coord = "T19"
			r.BestMove = "K10"
		} else {
			r.BestMove = "A1"
		}

		reports = append(reports, r)
	}

	found := findBaustellen(19, reports)

	if len(found) != 1 {
		t.Fatalf("%d Baustellen, erwartet 1", len(found))
	}

	b := found[0]

	if b.Player != "Schwarz" || b.Point != "K10" || b.Resolution != nil ||
		b.ResolvedBy != "" {
		t.Fatalf("unerwartete Baustelle: %+v", b)
	}

	if !strings.Contains(b.Text, "blieb der Punkt frei") {
		t.Fatalf("Text ohne Hinweis auf die offene Baustelle: %s", b.Text)
	}
}

func TestZuWenigeTrefferSindKeineBaustelle(t *testing.T) {
	var reports []MoveReport

	for n := 1; n <= 2*(minBaustelleHits-1); n++ {
		r := MoveReport{Number: n, Player: "Weiß", Coord: "A1", BestMove: "A1"}

		if n%2 == 1 {
			r = MoveReport{Number: n, Player: "Schwarz", Coord: "T19", BestMove: "K10"}
		}

		reports = append(reports, r)
	}

	if found := findBaustellen(19, reports); len(found) != 0 {
		t.Fatalf("%d Baustellen bei %d Treffern", len(found), minBaustelleHits-1)
	}
}
