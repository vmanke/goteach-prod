package qr

// Reed-Solomon über GF(256) mit dem für QR vorgeschriebenen Primpolynom
// x⁸+x⁴+x³+x²+1 (0x11D) und dem erzeugenden Element 2.

const gfPrimitive = 0x11D

var (
	gfExp [512]byte // gfExp[i] = 2^i, doppelt geführt, damit Indizes nicht überlaufen
	gfLog [256]byte // Umkehrung; gfLog[0] bleibt ungenutzt
)

func init() {
	x := 1

	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[x] = byte(i)
		x <<= 1

		if x&0x100 != 0 {
			x ^= gfPrimitive
		}
	}

	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

// gfMul multipliziert im Körper; die Null ist Sonderfall, weil sie keinen
// Logarithmus hat.
func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}

	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// generatorPoly ist das Generatorpolynom vom Grad n, also das Produkt der
// (x − 2^i) für i < n. Koeffizienten in absteigender Ordnung.
func generatorPoly(n int) []byte {
	poly := []byte{1}

	for i := 0; i < n; i++ {
		next := make([]byte, len(poly)+1)

		for j, c := range poly {
			next[j] ^= c
			next[j+1] ^= gfMul(c, gfExp[i])
		}

		poly = next
	}

	return poly
}

// reedSolomon liefert die n Fehlerkorrekturcodewörter zu data, also den
// Rest der Division des um n Stellen verschobenen Datenpolynoms durch das
// Generatorpolynom.
func reedSolomon(data []byte, n int) []byte {
	gen := generatorPoly(n)
	rest := make([]byte, len(data)+n)
	copy(rest, data)

	for i := 0; i < len(data); i++ {
		lead := rest[i]

		if lead == 0 {
			continue
		}

		for j, c := range gen {
			rest[i+j] ^= gfMul(c, lead)
		}
	}

	return rest[len(data):]
}
