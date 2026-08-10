// Package auth implementiert Passwort-Hashing (PBKDF2-HMAC-SHA256) und
// JWT HS256 ausschließlich mit der Standardbibliothek — das Projekt bleibt
// bewusst dependency-frei.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
)

// Key leitet nach RFC 2898 (PBKDF2) einen Schlüssel aus Passwort und Salt
// ab; PRF ist HMAC-SHA256. Entspricht golang.org/x/crypto/pbkdf2.Key mit
// sha256.New, hier nachimplementiert, um ohne externe Module auszukommen.
func Key(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen

	var buf [4]byte

	dk := make([]byte, 0, numBlocks*hashLen)
	u := make([]byte, hashLen)

	for block := 1; block <= numBlocks; block++ {
		// U_1 = PRF(password, salt || INT_BE(block))
		prf.Reset()
		prf.Write(salt)
		buf[0] = byte(block >> 24)
		buf[1] = byte(block >> 16)
		buf[2] = byte(block >> 8)
		buf[3] = byte(block)
		prf.Write(buf[:4])
		dk = prf.Sum(dk)

		t := dk[len(dk)-hashLen:]
		copy(u, t)

		// T = U_1 XOR U_2 XOR ... XOR U_iter
		for n := 2; n <= iter; n++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])

			for i := range u {
				t[i] ^= u[i]
			}
		}
	}

	return dk[:keyLen]
}
