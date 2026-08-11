// Die deutschen Sätze der Lehreinheiten.
//
// Drei Regeln, aus denen alles Weitere folgt:
//
//  1. Kein Satz ohne Beleg. Jede Behauptung nennt die Zahl, die sie trägt —
//     Freiheiten, Stärke, Punkte, Koordinaten. Was sich nicht belegen lässt,
//     wird nicht gesagt. Deshalb steht hier nirgends, das Brett sei "noch
//     offen" oder ein Zug "solide": Das wäre geraten, und geraten klingt
//     genau wie erfunden.
//  2. Keine Sprichwörter. Eine Go-Weisheit, die an jede Karte passt, sagt
//     über DIESEN Zug nichts. Statt einer Merkregel steht da, was der Zug
//     gekostet hat und was die Engine stattdessen gerechnet hat.
//  3. Keine Etiketten im Fließtext. Die ROSE-Stufe steht als Feld in den
//     Daten; sie muss nicht in jedem zweiten Satz als "R wie Respond:"
//     angekündigt werden.
package teaching

import (
	"fmt"
	"strings"
)

// adjColor macht aus "Schwarz"/"Weiß" das kleingeschriebene Adjektiv im
// Nominativ ("die schwarze Kette").
func adjColor(name string) string {
	if name == "Schwarz" {
		return "schwarze"
	}

	return "weiße"
}

// adjColorDative ist dasselbe Adjektiv im Dativ ("der schwarzen Kette").
// Eigene Funktion, weil ein falscher Kasus einen sonst sauberen Satz
// sofort nach Maschine klingen lässt.
func adjColorDative(name string) string {
	return adjColor(name) + "n"
}

// demandSentence beschreibt, was die Stellung VOR dem Zug verlangte —
// immer mit den Zahlen, die den Befund tragen.
func demandSentence(f *roseFinding, prevCoord string) string {
	switch f.bucket {
	case roseR:
		if f.tactic != nil {
			// Der Lehrsatz des Motivs stammt aus exakter Lesearbeit
			// (shapes/reading.go), nicht aus einer Faustregel.
			return fmt.Sprintf("Die %s Kette um %s stand in einem %s: %s",
				adjColor(f.color), f.rep, f.tactic.Name,
				lowerFirst(f.tactic.Teaching))
		}

		if f.atari {
			if prevCoord != "" {
				return fmt.Sprintf(
					"%s setzte die %s Kette um %s ins Atari — eine Freiheit blieb.",
					prevCoord, adjColor(f.color), f.rep)
			}

			return fmt.Sprintf(
				"Die %s Kette um %s stand im Atari — eine Freiheit blieb.",
				adjColor(f.color), f.rep)
		}

		return fmt.Sprintf(
			"%s nahm der %s Kette um %s Luft: %d Freiheiten, Stärke %.2f.",
			prevCoord, adjColorDative(f.color), f.rep, f.libs,
			noNegZero(f.strength))

	case roseO:
		if f.tactic != nil {
			return fmt.Sprintf("Die %s Kette um %s stand in einem %s: %s",
				adjColor(f.color), f.rep, f.tactic.Name,
				lowerFirst(f.tactic.Teaching))
		}

		return fmt.Sprintf(
			"Die %s Kette um %s hatte %d Freiheiten und Stärke %.2f.",
			adjColor(f.color), f.rep, f.libs, noNegZero(f.strength))

	default: // roseS
		return fmt.Sprintf(
			"Die eigene Kette um %s hatte %d Freiheiten und Stärke %.2f.",
			f.rep, f.libs, noNegZero(f.strength))
	}
}

// variationSentence spricht die gerechnete Fortsetzung aus. Sie ist der
// eigentliche Lehrstoff bei einem Fehler: nicht DASS ein Zug besser war,
// sondern wie es weitergegangen wäre.
func variationSentence(lead string, pv []string) string {
	if len(pv) < 2 {
		return ""
	}

	return fmt.Sprintf("%s rechnet die Engine weiter mit %s.",
		lead, strings.Join(pv[1:], " "))
}

// costSentence nennt den Preis des Zuges und die Erstwahl. cost ist der
// Abstand zur Erstwahl in Punkten, aus Sicht des Ziehenden.
func costSentence(coord, best string, cost float64) string {
	return fmt.Sprintf("%s kostet %.1f Punkte gegenüber %s.",
		coord, cost, best)
}

// unconsideredSentence gilt, wenn die Engine den gespielten Zug gar nicht
// erst gerechnet hat — bei einer breiten Suche eine Aussage für sich.
func unconsideredSentence(coord string, visits int) string {
	return fmt.Sprintf(
		"%s stand nicht unter den %d Zügen, die die Engine geprüft hat.",
		coord, visits)
}

// lowerFirst senkt den ersten Buchstaben eines Satzes für die Satzmitte.
func lowerFirst(s string) string {
	r := []rune(s)

	if len(r) == 0 {
		return s
	}

	first := strings.ToLower(string(r[0]))

	return first + string(r[1:])
}

// upperFirst hebt den ersten Buchstaben für den Satzanfang.
func upperFirst(s string) string {
	r := []rune(s)

	if len(r) == 0 {
		return s
	}

	return strings.ToUpper(string(r[0])) + string(r[1:])
}
