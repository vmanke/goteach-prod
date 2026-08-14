// Prüfung der Mitglieder-Tokens der Vereinsseite (ES256).
//
// Die Vereinsseite (flascheleer-berlin.de) stellt ihren Mitgliedern offline
// signierte Tokens aus — der Mannschaftsführer erzeugt sie mit
// `FLB_ISSUE=1 cargo run` und gibt sie persönlich weiter. Dieser Dienst
// kennt nur die öffentliche Hälfte des Schlüssels und kann damit prüfen,
// aber nichts ausstellen.
//
// Format (identisch zu dem, was die Seite selbst im Browser prüft):
//
//	b64url({"alg":"ES256","typ":"JWT"}) . b64url(Claims) . b64url(r‖s)
//
// Die Signatur ist rohes r‖s (64 Byte), nicht DER — genau das, was die
// Rust-Seite schreibt und was WebCrypto erwartet. Es gibt deshalb hier
// keine ASN.1-Umwandlung.
//
// Bewusst NICHT gelesen wird der Claim "k": das ist der Schlüssel, mit dem
// der Browser den verschlüsselten Mitglieder-Blob der Seite entschlüsselt.
// Auf diesem Server hat er nichts zu suchen, und was nicht gelesen wird,
// kann auch nicht versehentlich geloggt werden.
package auth

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// MemberIssuer ist der einzige Aussteller, dem dieser Dienst glaubt.
// Er steht wörtlich in frontend/src/admin.rs (payload_json) der Vereinsseite.
const MemberIssuer = "flascheleer-berlin.de"

// coordLen ist die Länge einer P-256-Koordinate in Byte.
const coordLen = 32

// MemberClaims sind die Felder, die dieser Dienst aus einem Mitglieder-Token
// nutzt. "k" fehlt hier absichtlich (siehe Paketkommentar).
type MemberClaims struct {
	Sub string `json:"sub"`
	Iss string `json:"iss"`
	Exp int64  `json:"exp"`
}

// jwk ist die öffentliche Schlüsselhälfte, wie die Vereinsseite sie
// committet (frontend/src/keys_generated.rs, PUBLIC_JWK).
type jwk struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// PublicKeyFromJWK liest den öffentlichen P-256-Schlüssel aus einem JWK.
//
// Streng statt nachsichtig: Kurve und Typ müssen stimmen, die Koordinaten
// müssen genau 32 Byte lang sein, und der Punkt muss auf der Kurve liegen
// (das prüft crypto/ecdh beim Anlegen). Ein halb akzeptierter Schlüssel
// wäre eine Fehlkonfiguration, die erst beim ersten Login auffiele.
func PublicKeyFromJWK(s string) (*ecdsa.PublicKey, error) {
	var k jwk

	if err := json.Unmarshal([]byte(s), &k); err != nil {
		return nil, fmt.Errorf("auth: JWK unlesbar: %w", err)
	}

	if k.Kty != "EC" || k.Crv != "P-256" {
		return nil, fmt.Errorf("auth: JWK ist kein EC/P-256 (kty=%q crv=%q)",
			k.Kty, k.Crv)
	}

	x, err := b64.DecodeString(k.X)

	if err != nil || len(x) != coordLen {
		return nil, fmt.Errorf("auth: JWK-Koordinate x unbrauchbar")
	}

	y, err := b64.DecodeString(k.Y)

	if err != nil || len(y) != coordLen {
		return nil, fmt.Errorf("auth: JWK-Koordinate y unbrauchbar")
	}

	// Unkomprimierter Punkt (0x04 ‖ x ‖ y). crypto/ecdh prüft dabei, dass
	// er wirklich auf der Kurve liegt — ecdsa.PublicKey täte das nicht.
	point := make([]byte, 0, 1+2*coordLen)
	point = append(point, 4)
	point = append(point, x...)
	point = append(point, y...)

	if _, err := ecdh.P256().NewPublicKey(point); err != nil {
		return nil, fmt.Errorf("auth: JWK-Punkt liegt nicht auf P-256: %w", err)
	}

	return &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(x),
		Y:     new(big.Int).SetBytes(y),
	}, nil
}

// VerifyES256 prüft Signatur, Aussteller und Ablauf eines Mitglieder-Tokens.
// Der Algorithmus ist auf ES256 gepinnt ("none" & Co. fliegen raus), der
// Ablauf toleriert ExpLeeway Uhrenversatz — dieselben Regeln wie VerifyHS256.
func VerifyES256(pub *ecdsa.PublicKey, token string, now time.Time) (MemberClaims, error) {
	var c MemberClaims

	if pub == nil {
		return c, fmt.Errorf("auth: kein öffentlicher Schlüssel konfiguriert")
	}

	parts := strings.Split(token, ".")

	if len(parts) != 3 {
		return c, fmt.Errorf("auth: Token hat keine drei Segmente")
	}

	headerJSON, err := b64.DecodeString(parts[0])

	if err != nil {
		return c, fmt.Errorf("auth: Header unlesbar: %w", err)
	}

	var header jwtHeader

	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return c, fmt.Errorf("auth: Header unlesbar: %w", err)
	}

	if header.Alg != "ES256" {
		return c, fmt.Errorf("auth: Algorithmus %q nicht erlaubt", header.Alg)
	}

	sig, err := b64.DecodeString(parts[2])

	if err != nil {
		return c, fmt.Errorf("auth: Signatur unlesbar: %w", err)
	}

	if len(sig) != 2*coordLen {
		return c, fmt.Errorf("auth: Signatur ist nicht 64 Byte (r‖s)")
	}

	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(sig[:coordLen])
	s := new(big.Int).SetBytes(sig[coordLen:])

	if !ecdsa.Verify(pub, sum[:], r, s) {
		return c, fmt.Errorf("auth: Signatur falsch")
	}

	payload, err := b64.DecodeString(parts[1])

	if err != nil {
		return c, fmt.Errorf("auth: Claims unlesbar: %w", err)
	}

	if err := json.Unmarshal(payload, &c); err != nil {
		return c, fmt.Errorf("auth: Claims unlesbar: %w", err)
	}

	if c.Iss != MemberIssuer {
		return c, fmt.Errorf("auth: fremder Aussteller %q", c.Iss)
	}

	if c.Sub == "" {
		return c, fmt.Errorf("auth: Token ohne Inhaber (sub)")
	}

	if c.Exp <= 0 {
		return c, fmt.Errorf("auth: Token ohne Ablauf (exp)")
	}

	if now.After(time.Unix(c.Exp, 0).Add(ExpLeeway)) {
		return c, fmt.Errorf("auth: Token abgelaufen")
	}

	return c, nil
}
