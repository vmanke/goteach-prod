// Package shapes benennt bekannte Formen auf dem Brett.
//
// Die Erkennung ist vollständig deterministisch — kein Modell, keine
// Schätzung. Das ist kein Verzicht, sondern der Punkt: "hier steht ein leeres
// Dreieck" ist eine nachprüfbare Tatsache über die Stellung, und nur solche
// Tatsachen dürfen laut Architekturbericht in einen Lehrtext eingehen.
//
// Zwei Mechanismen, weil Go-Formen zwei verschiedene Dinge sind:
//
//   - Schablonen (catalog.go, match.go): lokale Steinmuster, die man
//     hinschreiben kann — leeres Dreieck, Bambusverbindung, Keima.
//   - Lesearbeit (reading.go): Motive, die eine Variantensuche brauchen —
//     Leiter, Netz, Schnapp. Als Muster sind sie nicht darstellbar.
//
// Bewusst NICHT enthalten sind Josekis. Das sind Zugfolgen, keine lokalen
// Muster; sie bräuchten eine Sequenzdatenbank und ein Partienkorpus.
package shapes

// Zeichen einer Schablonenzelle.
const (
	// CellAny passt auf alles — auch auf Punkte außerhalb des Bretts.
	CellAny = '?'

	// CellEmpty verlangt einen leeren Punkt auf dem Brett.
	CellEmpty = '.'

	// CellOwn verlangt einen Stein der Farbe, die gerade die Rolle "eigen"
	// spielt; CellOpponent den der Gegenfarbe.
	CellOwn      = 'X'
	CellOpponent = 'O'
)

// Template ist ein benanntes lokales Steinmuster.
//
// Rows[0] ist die oberste Zeile. Alle Zeilen müssen gleich lang sein.
type Template struct {
	// Name ist die deutsche Bezeichnung, Japanese der eingebürgerte
	// japanische Begriff (leer, wo es keinen gibt).
	Name     string
	Japanese string

	// Teaching beschreibt in einem Satz, warum die Form erwähnenswert ist.
	// Rein beschreibend — die Bewertung liefern die Zahlen aus KataGo.
	Teaching string

	Rows []string
}

// Catalog sind die Formen, die als Schablone darstellbar sind.
//
// Die Muster stehen hier in *einer* Orientierung; match.go erzeugt daraus
// beim Start alle acht Symmetrien des Quadrats und beide Farbrollen.
var Catalog = []Template{
	{
		Name:     "leeres Dreieck",
		Japanese: "empty triangle",
		Teaching: "Drei Steine im rechten Winkel mit leerem vierten Punkt — " +
			"dieselbe Steinzahl deckt hier weniger Freiheiten ab als in " +
			"gestreckter Form.",
		Rows: []string{
			"XX",
			"X.",
		},
	},
	{
		Name:     "Bambusverbindung",
		Japanese: "take no fushi",
		Teaching: "Zwei Steinpaare mit Lücke: nicht trennbar, weil ein Schnitt " +
			"jeweils mit der anderen Seite beantwortet wird.",
		Rows: []string{
			"XX",
			"..",
			"XX",
		},
	},
	{
		Name:     "Tigermaul",
		Japanese: "tora no ko",
		Teaching: "Drei Steine um einen leeren Punkt — wer dort hineinspielt, " +
			"steht sofort im Atari.",
		Rows: []string{
			"X.X",
			".X.",
		},
	},
	{
		Name:     "Kosumi",
		Japanese: "kosumi",
		Teaching: "Diagonalzug: langsam, aber nicht zu trennen.",
		Rows: []string{
			"X.",
			".X",
		},
	},
	{
		Name:     "Ein-Punkt-Sprung",
		Japanese: "ikken tobi",
		Teaching: "Der Sprung über einen Punkt gilt als nie schlecht — er ist " +
			"schnell und bleibt verbunden.",
		Rows: []string{
			"X.X",
		},
	},
	{
		Name:     "Zwei-Punkte-Sprung",
		Japanese: "niken tobi",
		Teaching: "Schneller als der Ein-Punkt-Sprung, dafür angreifbarer.",
		Rows: []string{
			"X..X",
		},
	},
	{
		Name:     "Kleiner Springerzug",
		Japanese: "keima",
		Teaching: "Springerzug: schnell, aber am Kreuzungspunkt durchschneidbar.",
		Rows: []string{
			".X",
			"..",
			"X.",
		},
	},
	{
		Name:     "Großer Springerzug",
		Japanese: "ogeima",
		Teaching: "Weiter Springerzug — schnell und entsprechend dünn.",
		Rows: []string{
			".X",
			"..",
			"..",
			"X.",
		},
	},
	{
		Name:     "Kreuzschnitt",
		Japanese: "crosscut",
		Teaching: "Vier Steine über Kreuz: eine Kampfform, in der meistens " +
			"ausgedehnt statt gleich geschlagen wird.",
		Rows: []string{
			"XO",
			"OX",
		},
	},
}
