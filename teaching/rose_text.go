// Die deutschen Bausteine der ROSE-Texte. Alles Prosa hier ist Template —
// jede Zahl, jede Koordinate, jeder Ketten-Bezug kommt aus verifizierten
// Befunden (rose.go). Stil: nüchtern, Sie-Form, keine Floskeln.
package teaching

import (
	"fmt"
	"strings"
)

// adjColor macht aus "Schwarz"/"Weiß" das kleingeschriebene Adjektiv für
// die Satzmitte.
func adjColor(name string) string {
	if name == "Schwarz" {
		return "schwarze"
	}

	return "weiße"
}

// befundSentence formuliert den dringlichsten Befund der Stellung.
func befundSentence(f *roseFinding, prevCoord string) string {
	switch f.bucket {
	case roseR:
		if f.tactic != nil {
			return fmt.Sprintf(
				"R wie Respond: %s gegen die %s Kette um %s — %s",
				f.tactic.Name, adjColor(f.color), f.rep,
				lowerFirst(f.tactic.Teaching))
		}

		if f.atari {
			if prevCoord != "" {
				return fmt.Sprintf(
					"R wie Respond: Der letzte Zug (%s) setzt die %s Kette um "+
						"%s ins Atari — eine lokale Antwort ist zwingend.",
					prevCoord, adjColor(f.color), f.rep)
			}

			return fmt.Sprintf(
				"R wie Respond: Die %s Kette um %s steht im Atari — eine "+
					"lokale Antwort ist zwingend.", adjColor(f.color), f.rep)
		}

		return fmt.Sprintf(
			"R wie Respond: Der letzte Zug (%s) bedroht die %s Kette um %s "+
				"(%d Freiheiten, Stärke %.2f) — erst die Not klären.",
			prevCoord, adjColor(f.color), f.rep, f.libs, f.strength)

	case roseO:
		if f.tactic != nil {
			return fmt.Sprintf(
				"O wie Offense: %s gegen die %s Kette um %s — %s",
				f.tactic.Name, adjColor(f.color), f.rep,
				lowerFirst(f.tactic.Teaching))
		}

		return fmt.Sprintf(
			"O wie Offense: Die %s Kette um %s ist schwach (%d Freiheiten, "+
				"Stärke %.2f) — Angriff oder Trennung ist der dringlichste Zug.",
			adjColor(f.color), f.rep, f.libs, f.strength)

	default: // roseS
		return fmt.Sprintf(
			"S wie Status: Die eigene Kette um %s steht schwach (%d "+
				"Freiheiten, Stärke %.2f) — erst verstärken, dann expandieren.",
			f.rep, f.libs, f.strength)
	}
}

// eSentence formuliert den E-Befund einer Stellung ohne dringlichere Fragen.
func eSentence(d *roseDetail, coord string) string {
	switch {
	case d.phase == "Endspiel":
		return "E wie Endgame: Keine Gruppe in Not — jetzt zählt die exakte " +
			"Punktgröße jedes Zuges."

	case d.openArea && coord != "":
		return fmt.Sprintf(
			"E wie Expansion: Um %s ist das Brett noch offen — solange nichts "+
				"drängt, ist der größte freie Raum der Maßstab.", coord)

	default:
		return "E wie Expansion: Keine dringende Gruppe auf dem Brett — es " +
			"entscheidet die Größe des Punktes."
	}
}

const tenukiSentence = "R geprüft: keine eigene Gruppe in Not — das Tenuki " +
	"ist erlaubt."

// bucketClaim benennt, was ein Zug auf einer Stufe tut — für die
// Urteilssätze.
func bucketClaim(bucket int, f *roseFinding, prevCoord string) string {
	switch bucket {
	case roseR:
		if prevCoord != "" {
			return "R (Antwort auf " + prevCoord + ")"
		}

		return "R (lokale Antwort)"

	case roseO:
		if f != nil {
			return "O (Angriff um " + f.rep + ")"
		}

		return "O (Angriff)"

	case roseS:
		if f != nil {
			return "S (Verstärkung um " + f.rep + ")"
		}

		return "S (Verstärkung)"

	default:
		return "E (großer Punkt)"
	}
}

// findingOf liefert den ersten Befund einer Stufe.
func findingOf(findings []roseFinding, bucket int) *roseFinding {
	for i := range findings {
		if findings[i].bucket == bucket {
			return &findings[i]
		}
	}

	return nil
}

// variantSuffix hängt die Engine-Variante an, wenn sie mehr als den ersten
// Zug enthält.
func variantSuffix(pv []string) string {
	if len(pv) <= 1 {
		return ""
	}

	return " (Variante: " + strings.Join(pv, " ") + ")"
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

// Abschluss-Sätze je Stufe: zwei Formulierungen, jede höchstens zweimal je
// Partie — danach bewusst Stille statt Floskel.
var closers = map[int][]string{
	roseR: {
		"Erst die Not klären, dann der große Punkt — eine Gruppe im Atari " +
			"kostet mehr als jedes Gebiet.",
		"Freiheiten vor dem Setzen zählen, nicht danach.",
	},
	roseO: {
		"Schwache Gruppen greift man mit Abstand an — der Profit entsteht " +
			"beim Jagen, nicht beim Berühren.",
		"Trennen ist oft stärker als Umzingeln: getrennte Gruppen müssen " +
			"zweimal leben.",
	},
	roseS: {
		"Eine Basis mit Augenraum ist der billigste Zug der Partie — " +
			"solange sie noch freiwillig ist.",
		"Stabilität der eigenen Gruppen geht vor Territorium.",
	},
	roseE: {
		"Der größte Punkt liegt dort, wo beide Seiten expandieren können — " +
			"wer zuerst kommt, nimmt beide Richtungen.",
		"Im Endspiel zählt Sente: erst die Züge, die der Gegner beantworten " +
			"muss.",
	},
}
