package shapes

import (
	"sort"

	"github.com/vmanke/goteach-prod/board"
	"github.com/vmanke/goteach-prod/groups"
)

// DefaultDepth begrenzt die Leitersuche. Eine Leiter läuft diagonal über das
// Brett und ist nach spätestens rund 2·Brettgröße Zügen entschieden; der
// Puffer fängt Umwege durch geschlagene Steine ab.
const DefaultDepth = 64

// getaRadius begrenzt, wie weit vom Stein entfernt ein Netzzug gesucht wird.
// Ein Netz wirkt aus der Nähe; alles Weitere wäre Rauschen und teuer.
const getaRadius = 2

// Ladder meldet, ob der Gegner die Kette am Punkt p durch fortgesetztes Atari
// fangen kann — die Leiter (Shicho).
//
// Damit löst das Paket ein Versprechen ein, das der Lehrtext bisher an den
// Spieler weiterreichte ("lesen Sie die Leiter"), ohne es selbst zu können.
//
// Die Suche ist exakt, nicht heuristisch: Sie spielt die Variante auf Kopien
// des Bretts durch und nutzt dieselbe Regellogik wie eine echte Partie
// (board.Board.Play, inklusive Schlagen und Selbstmordverbot).
func Ladder(b *board.Board, p board.Point, depth int) bool {
	if depth <= 0 {
		depth = DefaultDepth
	}

	return caught(b, p, depth)
}

func caught(b *board.Board, p board.Point, depth int) bool {
	defender := b.Get(p)

	if defender == board.Empty || depth <= 0 {
		return false
	}

	chain := groups.ChainAt(b, p)

	if chain == nil {
		return false
	}

	switch len(chain.Liberties) {
	case 1:
		// Bereits im Atari: der nächste Zug schlägt.
		return true

	case 2:
		// Genau der Fall, in dem eine Leiter überhaupt möglich ist.

	default:
		return false
	}

	attacker := defender.Opponent()

	for _, liberty := range chain.Liberties {
		after := b.Clone()

		if err := after.Play(board.Move{Color: attacker, Point: liberty}); err != nil {
			continue
		}

		// Der Angriffszug kann die Kette bereits geschlagen haben.
		if after.Get(p) == board.Empty {
			return true
		}

		escape := groups.ChainAt(after, p)

		if escape == nil || len(escape.Liberties) == 0 {
			return true
		}

		if len(escape.Liberties) > 1 {
			// Kein Atari — dieser Angriffszug taugt nicht, nächster.
			continue
		}

		// Der Verteidiger muss auf seine letzte Freiheit ausdehnen.
		run := after.Clone()
		err := run.Play(board.Move{Color: defender, Point: escape.Liberties[0]})

		if err != nil {
			// Kein legaler Ausweg: gefangen.
			return true
		}

		if caught(run, p, depth-1) {
			return true
		}
	}

	return false
}

// Geta meldet, ob die Kette am Punkt p in einem Netz gefangen werden kann:
// ein Zug, der die Kette *nicht* ins Atari setzt und ihr trotzdem jeden
// Ausweg nimmt.
//
// Gesucht wird nur in unmittelbarer Nähe der Kette — ein Netz wirkt aus der
// Distanz eines oder zweier Punkte, alles Weitere wäre Rauschen.
func Geta(b *board.Board, p board.Point, depth int) (board.Point, bool) {
	defender := b.Get(p)

	if defender == board.Empty {
		return board.Point{}, false
	}

	chain := groups.ChainAt(b, p)

	// Bei einer Freiheit ist es Atari, bei vielen ist nichts zu netzen.
	if chain == nil || len(chain.Liberties) != 2 {
		return board.Point{}, false
	}

	attacker := defender.Opponent()
	liberties := map[int]bool{}

	for _, l := range chain.Liberties {
		liberties[b.Idx(l)] = true
	}

	for _, q := range nearbyEmpty(b, chain.Stones, getaRadius) {
		// Ein Zug auf eine Freiheit wäre Atari, kein Netz.
		if liberties[b.Idx(q)] {
			continue
		}

		net := b.Clone()

		if err := net.Play(board.Move{Color: attacker, Point: q}); err != nil {
			continue
		}

		if escapes(net, p, attacker, depth) {
			continue
		}

		return q, true
	}

	return board.Point{}, false
}

// escapes prüft, ob der Verteidiger die Kette bei p noch herausbekommt.
func escapes(b *board.Board, p board.Point, attacker board.Color, depth int) bool {
	chain := groups.ChainAt(b, p)

	if chain == nil {
		// Kette ist weg — sie ist nicht entkommen.
		return false
	}

	if len(chain.Liberties) > 2 {
		return true
	}

	defender := b.Get(p)

	for _, liberty := range chain.Liberties {
		run := b.Clone()

		if err := run.Play(board.Move{Color: defender, Point: liberty}); err != nil {
			continue
		}

		grown := groups.ChainAt(run, p)

		if grown == nil {
			continue
		}

		// Genügend Freiheiten gewonnen: entkommen, sofern keine Leiter greift.
		if len(grown.Liberties) > 2 {
			return true
		}

		if !caught(run, p, depth) {
			return true
		}
	}

	return false
}

// Snapback meldet, ob am Punkt p ein Schnapp vorliegt: Der Verteidiger kann
// eine gegnerische Kette schlagen, steht danach aber selbst sofort im Atari
// und verliert mehr, als er gewonnen hat.
func Snapback(b *board.Board, p board.Point) bool {
	victim := b.Get(p)

	if victim == board.Empty {
		return false
	}

	chain := groups.ChainAt(b, p)

	// Der Köder ist eine Kette im Atari.
	if chain == nil || len(chain.Liberties) != 1 {
		return false
	}

	capturer := victim.Opponent()
	after := b.Clone()

	if err := after.Play(board.Move{Color: capturer, Point: chain.Liberties[0]}); err != nil {
		return false
	}

	// Nach dem Schlagen: Steht der schlagende Stein selbst im Atari?
	taken := groups.ChainAt(after, chain.Liberties[0])

	return taken != nil && len(taken.Liberties) == 1
}

// nearbyEmpty liefert leere Punkte im gegebenen Gitterabstand zu den Steinen,
// deterministisch sortiert.
func nearbyEmpty(b *board.Board, stones []board.Point, radius int) []board.Point {
	seen := map[int]bool{}
	var out []board.Point

	for _, s := range stones {
		for dy := -radius; dy <= radius; dy++ {
			for dx := -radius; dx <= radius; dx++ {
				if abs(dx)+abs(dy) > radius {
					continue
				}

				q := board.Point{X: s.X + dx, Y: s.Y + dy}

				if !b.InBounds(q) || b.Get(q) != board.Empty {
					continue
				}

				if idx := b.Idx(q); !seen[idx] {
					seen[idx] = true
					out = append(out, q)
				}
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Y != out[j].Y {
			return out[i].Y < out[j].Y
		}

		return out[i].X < out[j].X
	})

	return out
}

func abs(v int) int {
	if v < 0 {
		return -v
	}

	return v
}

// FindTactics liefert die gelesenen Motive der Stellung als Instanzen, damit
// sie neben den Schablonenformen in denselben Strang einfließen können.
func FindTactics(b *board.Board) []Instance {
	var out []Instance

	for _, chain := range groups.FindChains(b) {
		rep := chain.Rep(b.Size)

		switch {
		case len(chain.Liberties) == 1 && Snapback(b, rep):
			out = append(out, tactic(b, "Schnapp", "snapback", chain,
				"Diese Kette zu schlagen kostet mehr, als sie einbringt — "+
					"der schlagende Stein steht danach selbst im Atari."))

		case len(chain.Liberties) == 2:
			if Ladder(b, rep, DefaultDepth) {
				out = append(out, tactic(b, "Leiter", "shicho", chain,
					"Diese Kette lässt sich durch fortgesetztes Atari fangen; "+
						"Ausdehnen hilft nicht."))

				continue
			}

			if _, ok := Geta(b, rep, DefaultDepth); ok {
				out = append(out, tactic(b, "Netz", "geta", chain,
					"Diese Kette lässt sich einnetzen: ein Zug aus der "+
						"Distanz nimmt ihr den Ausweg, ohne sie ins Atari zu setzen."))
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}

		return out[i].Points[0] < out[j].Points[0]
	})

	return out
}

func tactic(b *board.Board, name, japanese string, chain *groups.Chain,
	teaching string) Instance {

	points := make([]string, len(chain.Stones))
	stones := make([]board.Point, len(chain.Stones))

	copy(stones, chain.Stones)
	sort.Slice(stones, func(i, j int) bool {
		if stones[i].Y != stones[j].Y {
			return stones[i].Y < stones[j].Y
		}

		return stones[i].X < stones[j].X
	})

	for i, s := range stones {
		points[i] = board.ToGTP(s, b.Size)
	}

	return Instance{
		Name:     name,
		Japanese: japanese,
		Color:    colourName(chain.Color),
		Points:   points,
		Teaching: teaching,
		Stones:   stones,
	}
}
