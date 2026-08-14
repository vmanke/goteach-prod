package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// clubJWK ist der wirklich ausgelieferte öffentliche Schlüssel der
// Vereinsseite (frontend/src/keys_generated.rs, PUBLIC_JWK). Er steht hier,
// damit ein Formatwechsel drüben hier auffällt und nicht erst in Produktion.
const clubJWK = `{"kty":"EC","crv":"P-256",` +
	`"x":"7JZhsuvgEhe-PKipOJolP_MLvsSUmNhMTC9J3xiVVZw",` +
	`"y":"0zfb0vppJiiCMcRd94FkkjedlCR_usRF4mh_Fv9E4zg"}`

// issueES256 stellt ein Token aus wie die Vereinsseite: rohes r‖s, kein DER.
func issueES256(t *testing.T, key *ecdsa.PrivateKey, payload string) string {
	t.Helper()

	header := b64.EncodeToString([]byte(`{"alg":"ES256","typ":"JWT"}`))
	signing := header + "." + b64.EncodeToString([]byte(payload))
	sum := sha256.Sum256([]byte(signing))

	r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])

	if err != nil {
		t.Fatalf("signieren: %v", err)
	}

	sig := make([]byte, 2*coordLen)
	r.FillBytes(sig[:coordLen])
	s.FillBytes(sig[coordLen:])

	return signing + "." + b64.EncodeToString(sig)
}

// memberPayload baut den Claims-Satz, den admin.rs der Vereinsseite schreibt
// — inklusive des Content-Keys "k", den dieser Dienst ignorieren muss.
func memberPayload(sub string, exp int64) string {
	body, _ := json.Marshal(map[string]any{
		"iss": MemberIssuer,
		"sub": sub,
		"exp": exp,
		"k":   b64.EncodeToString(make([]byte, 32)),
	})

	return string(body)
}

func TestPublicKeyFromClubJWK(t *testing.T) {
	pub, err := PublicKeyFromJWK(clubJWK)

	if err != nil {
		t.Fatalf("PublicKeyFromJWK: %v", err)
	}

	if pub.Curve != elliptic.P256() {
		t.Error("Kurve ist nicht P-256")
	}
}

func TestPublicKeyFromJWKRejectsRubbish(t *testing.T) {
	for name, bad := range map[string]string{
		"leer":            "",
		"kein JSON":       "{",
		"falsche Kurve":   `{"kty":"EC","crv":"P-384","x":"AA","y":"AA"}`,
		"falscher Typ":    `{"kty":"RSA","crv":"P-256","x":"AA","y":"AA"}`,
		"x zu kurz":       `{"kty":"EC","crv":"P-256","x":"AA","y":"AA"}`,
		"nicht auf Kurve": `{"kty":"EC","crv":"P-256","x":"` + strings.Repeat("A", 43) + `","y":"` + strings.Repeat("A", 43) + `"}`,
	} {
		if _, err := PublicKeyFromJWK(bad); err == nil {
			t.Errorf("PublicKeyFromJWK(%s): Fehler erwartet", name)
		}
	}
}

func TestES256Roundtrip(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	if err != nil {
		t.Fatalf("Schlüssel: %v", err)
	}

	now := time.Unix(1700000000, 0)
	token := issueES256(t, key, memberPayload("uwe", now.Add(time.Hour).Unix()))

	c, err := VerifyES256(&key.PublicKey, token, now)

	if err != nil {
		t.Fatalf("VerifyES256: %v", err)
	}

	if c.Sub != "uwe" || c.Iss != MemberIssuer {
		t.Errorf("Claims falsch: %+v", c)
	}
}

func TestES256RejectsForeignKeyAndTampering(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	now := time.Unix(1700000000, 0)
	token := issueES256(t, key, memberPayload("uwe", now.Add(time.Hour).Unix()))

	if _, err := VerifyES256(&other.PublicKey, token, now); err == nil {
		t.Error("Token mit fremdem Schlüssel akzeptiert")
	}

	// Payload gegen den eines anderen Inhabers tauschen: die Signatur deckt
	// ihn mit ab, also muss die Prüfung scheitern.
	parts := strings.Split(token, ".")
	forged := strings.Split(
		issueES256(t, other, memberPayload("mallory", now.Add(time.Hour).Unix())), ".")

	if _, err := VerifyES256(&key.PublicKey,
		parts[0]+"."+forged[1]+"."+parts[2], now); err == nil {
		t.Error("manipuliertes Token akzeptiert")
	}
}

func TestES256RejectsAlgNoneAndHS256(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := time.Unix(1700000000, 0)
	payload := b64.EncodeToString([]byte(memberPayload("uwe", now.Add(time.Hour).Unix())))

	for name, header := range map[string]string{
		"none":  `{"alg":"none","typ":"JWT"}`,
		"HS256": `{"alg":"HS256","typ":"JWT"}`,
	} {
		token := b64.EncodeToString([]byte(header)) + "." + payload + "."

		if _, err := VerifyES256(&key.PublicKey, token, now); err == nil {
			t.Errorf("alg %s akzeptiert", name)
		}
	}
}

func TestES256RejectsForeignIssuerAndExpiry(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := time.Unix(1700000000, 0)

	foreign, _ := json.Marshal(map[string]any{
		"iss": "angreifer.example", "sub": "mallory",
		"exp": now.Add(time.Hour).Unix(),
	})

	if _, err := VerifyES256(&key.PublicKey,
		issueES256(t, key, string(foreign)), now); err == nil {
		t.Error("fremder Aussteller akzeptiert")
	}

	expired := issueES256(t, key, memberPayload("uwe", now.Add(-time.Hour).Unix()))

	if _, err := VerifyES256(&key.PublicKey, expired, now); err == nil {
		t.Error("abgelaufenes Token akzeptiert")
	}

	// Innerhalb der Leeway-Toleranz bleibt es gültig — wie bei HS256.
	edge := issueES256(t, key, memberPayload("uwe", now.Add(-ExpLeeway/2).Unix()))

	if _, err := VerifyES256(&key.PublicKey, edge, now); err != nil {
		t.Errorf("Token innerhalb der Leeway abgelehnt: %v", err)
	}
}

func TestES256Garbage(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := time.Unix(1700000000, 0)

	for _, bad := range []string{"", "abc", "a.b", "a.b.c.d", "ä.ö.ü"} {
		if _, err := VerifyES256(&key.PublicKey, bad, now); err == nil {
			t.Errorf("VerifyES256(%q): Fehler erwartet", bad)
		}
	}

	if _, err := VerifyES256(nil, "a.b.c", now); err == nil {
		t.Error("Prüfung ohne Schlüssel akzeptiert")
	}
}
