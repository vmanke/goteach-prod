package strength

import (
	"math"
)

// Field liefert den Stärketensor: für JEDEN Brettpunkt die distanzgewichtete
// Aggregation des Ownership-Felds um diesen Punkt herum, in Schwarz-Sicht.
//
//	K(p) = Σ_q exp(-d(p, q)/tau) · ownership_black(q)
//	       ──────────────────────────────────────────
//	       Σ_q exp(-d(p, q)/tau)
//
// Wertebereich [-1, +1]: +1 = Umgebung vollständig schwarz.
//
// Verhältnis zu Group: Group misst dieselbe Größe für eine ganze Kette und
// benutzt dafür die Multi-Source-Distanz zur nächstgelegenen Steinposition.
// Field ist der Spezialfall einer einzelnen Quelle je Punkt — und genau
// dadurch billiger: Auf dem freien Gitter ist d die Manhattan-Distanz, also
// ist exp(-d/tau) = exp(-|dx|/tau) · exp(-|dy|/tau) separierbar. Statt O(N²)
// genügen zwei eindimensionale Durchläufe.
//
// Der Tensor ist die Grundlage dafür, Formen über die Zeit vergleichbar zu
// machen: Jede Forminstanz bekommt daraus ihre eigene Stärkespur.
func Field(size int, ownershipBlack []float64, tau float64) []float64 {
	n := size * size

	if len(ownershipBlack) != n {
		return nil
	}

	if tau <= 0 {
		tau = 3.0
	}

	kernel := make([]float64, size)

	for d := range kernel {
		kernel[d] = math.Exp(-float64(d) / tau)
	}

	// Zähler und Nenner gemeinsam falten: Der Nenner ist dieselbe Faltung
	// über ein Einsfeld und fängt damit den Brettrand korrekt ab, wo einem
	// Punkt schlicht weniger Nachbarschaft zur Verfügung steht.
	ones := make([]float64, n)

	for i := range ones {
		ones[i] = 1.0
	}

	numerator := separable(size, ownershipBlack, kernel)
	denominator := separable(size, ones, kernel)

	out := make([]float64, n)

	for i := range out {
		if denominator[i] > 0 {
			out[i] = numerator[i] / denominator[i]
		}
	}

	return out
}

// separable faltet ein Feld erst zeilen-, dann spaltenweise mit dem
// gegebenen symmetrischen Kern über den Abstandsindex.
func separable(size int, src, kernel []float64) []float64 {
	tmp := make([]float64, size*size)

	for y := 0; y < size; y++ {
		row := y * size

		for x := 0; x < size; x++ {
			var sum float64

			for k := 0; k < size; k++ {
				d := x - k

				if d < 0 {
					d = -d
				}

				sum += kernel[d] * src[row+k]
			}

			tmp[row+x] = sum
		}
	}

	out := make([]float64, size*size)

	for x := 0; x < size; x++ {
		for y := 0; y < size; y++ {
			var sum float64

			for k := 0; k < size; k++ {
				d := y - k

				if d < 0 {
					d = -d
				}

				sum += kernel[d] * tmp[k*size+x]
			}

			out[y*size+x] = sum
		}
	}

	return out
}
