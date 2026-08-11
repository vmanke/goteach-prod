package katago

// Ergebnisse einzeln abholen, statt auf die ganze Partie zu warten.
//
// Die Analysis Engine antwortet ohnehin je Stellung, sobald sie fertig
// ist — AnalyzeGame sammelt diese Antworten nur ein. Für einen Dienst,
// der die Lehreinheiten fortlaufend ausliefert, ist genau das Einsammeln
// das Problem: Bei einer langen Partie liegt die erste fertige Stellung
// Minuten vor der letzten, und niemand sieht sie.

// StreamAnalyzer ist die optionale Erweiterung für Analyzer, die ihre
// Ergebnisse liefern können, sobald sie entstehen.
//
// Bewusst NICHT Teil von Analyzer: Der Remote-Client holt seine
// Ergebnisse in einem HTTP-Roundtrip und kann nichts früher liefern; er
// soll deshalb auch nicht so tun.
type StreamAnalyzer interface {
	// AnalyzeGameStream ruft emit für jedes Ergebnis auf, sobald es
	// vorliegt. Die Reihenfolge ist NICHT die der Turns — KataGo
	// antwortet, wie es fertig wird. Ein Fehler aus emit bricht ab.
	AnalyzeGameStream(req Request, turns []int, emit func(*Result) error) error
}

// Stream liefert die Ergebnisse einzeln an emit: über den fortlaufenden
// Weg, wenn der Analyzer ihn beherrscht, sonst als Nachlauf einer
// Sammelanalyse. So bleibt der Aufrufer von der Frage unbehelligt,
// welche Engine gerade rechnet — er bekommt in beiden Fällen dieselben
// Ergebnisse, nur früher oder später.
func Stream(an Analyzer, req Request, turns []int, emit func(*Result) error) error {
	if s, ok := an.(StreamAnalyzer); ok {
		return s.AnalyzeGameStream(req, turns, emit)
	}

	results, err := an.AnalyzeGame(req, turns)

	if err != nil {
		return err
	}

	for _, res := range results {
		if err := emit(res); err != nil {
			return err
		}
	}

	return nil
}
