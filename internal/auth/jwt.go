package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ExpLeeway toleriert Uhrenversatz zwischen Aussteller und Prüfer.
const ExpLeeway = 30 * time.Second

// Claims sind die einzigen Felder, die dieser Dienst nutzt. Zeiten als
// Unix-Sekunden (RFC 7519).
type Claims struct {
	Sub string `json:"sub"`
	Iss string `json:"iss"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

// jwtHeader ist fix: Es wird ausschließlich HS256 ausgestellt und geprüft.
type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// SignHS256 stellt ein JWT (HS256) über den Claims aus.
func SignHS256(secret []byte, c Claims) string {
	header, _ := json.Marshal(jwtHeader{Alg: "HS256", Typ: "JWT"})
	payload, _ := json.Marshal(c)

	signing := b64.EncodeToString(header) + "." + b64.EncodeToString(payload)
	sig := signHS256(secret, signing)

	return signing + "." + b64.EncodeToString(sig)
}

func signHS256(secret []byte, signing string) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signing))

	return mac.Sum(nil)
}

// VerifyHS256 prüft Signatur und Ablauf eines Tokens. Der Algorithmus ist
// auf HS256 gepinnt ("none" & Co. werden abgelehnt), Signaturvergleich in
// konstanter Zeit, Ablauf mit ExpLeeway gegen Uhrenversatz.
func VerifyHS256(secret []byte, token string, now time.Time) (Claims, error) {
	var c Claims

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

	if header.Alg != "HS256" {
		return c, fmt.Errorf("auth: Algorithmus %q nicht erlaubt", header.Alg)
	}

	sig, err := b64.DecodeString(parts[2])

	if err != nil {
		return c, fmt.Errorf("auth: Signatur unlesbar: %w", err)
	}

	want := signHS256(secret, parts[0]+"."+parts[1])

	if !hmac.Equal(sig, want) {
		return c, fmt.Errorf("auth: Signatur falsch")
	}

	payload, err := b64.DecodeString(parts[1])

	if err != nil {
		return c, fmt.Errorf("auth: Claims unlesbar: %w", err)
	}

	if err := json.Unmarshal(payload, &c); err != nil {
		return c, fmt.Errorf("auth: Claims unlesbar: %w", err)
	}

	if c.Exp <= 0 {
		return c, fmt.Errorf("auth: Token ohne Ablauf (exp)")
	}

	if now.After(time.Unix(c.Exp, 0).Add(ExpLeeway)) {
		return c, fmt.Errorf("auth: Token abgelaufen")
	}

	return c, nil
}
