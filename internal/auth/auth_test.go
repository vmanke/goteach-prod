package auth

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

// TestKeyVectors prüft die PBKDF2-Implementierung gegen publizierte
// PBKDF2-HMAC-SHA256-Testvektoren (u. a. RFC 7914, Abschnitt 11).
func TestKeyVectors(t *testing.T) {
	cases := []struct {
		password, salt string
		iter, keyLen   int
		want           string
	}{
		{"password", "salt", 1, 32,
			"120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b"},
		{"password", "salt", 2, 32,
			"ae4d0c95af6b46d32d0adff928f06dd02a303f8ef3c251dfd6e2d85a95474c43"},
		{"password", "salt", 4096, 32,
			"c5e478d59288c841aa530db6845c4c8d962893a001ce4e11a4963873aa98134a"},
		{"passwordPASSWORDpassword", "saltSALTsaltSALTsaltSALTsaltSALTsalt",
			4096, 40,
			"348c89dbcbd32b2f32d814b8116e84cf2b17347ebc1800181c4e2a1fb8dd53e1c635518c7dac47e9"},
	}

	for _, c := range cases {
		got := hex.EncodeToString(
			Key([]byte(c.password), []byte(c.salt), c.iter, c.keyLen))

		if got != c.want {
			t.Errorf("Key(%q,%q,%d,%d) = %s, erwartet %s",
				c.password, c.salt, c.iter, c.keyLen, got, c.want)
		}
	}
}

func TestHashVerifyRoundtrip(t *testing.T) {
	hash, err := HashPassword("geheim", 1000)

	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	if !strings.HasPrefix(hash, "pbkdf2-sha256$1000$") {
		t.Fatalf("unerwartetes Hash-Format: %s", hash)
	}

	if strings.Contains(hash, "=") {
		t.Fatalf("Hash enthält '=' (bricht dotenv): %s", hash)
	}

	if !VerifyPassword("geheim", hash) {
		t.Error("richtiges Passwort abgelehnt")
	}

	if VerifyPassword("falsch", hash) {
		t.Error("falsches Passwort akzeptiert")
	}

	if VerifyPassword("geheim", "kaputt$format") {
		t.Error("kaputter Hash akzeptiert")
	}
}

func TestHashPasswordErrors(t *testing.T) {
	if _, err := HashPassword("", 1000); err == nil {
		t.Error("leeres Passwort akzeptiert")
	}

	if _, err := HashPassword("pw", 0); err == nil {
		t.Error("0 Iterationen akzeptiert")
	}
}

func TestParseUsers(t *testing.T) {
	hash, err := HashPassword("pw", 1000)

	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	users, err := ParseUsers("alice:" + hash + " , bob:" + hash)

	if err != nil {
		t.Fatalf("ParseUsers: %v", err)
	}

	if len(users) != 2 || users["alice"] != hash || users["bob"] != hash {
		t.Errorf("ParseUsers: unerwartetes Ergebnis %v", users)
	}

	for _, bad := range []string{
		"",                         // keine Benutzer
		"alice",                    // kein Doppelpunkt
		"alice:",                   // leerer Hash
		":" + hash,                 // leerer Name
		"alice:klartext",           // falsches Hash-Format
		"a:" + hash + ",a:" + hash, // doppelt
	} {
		if _, err := ParseUsers(bad); err == nil {
			t.Errorf("ParseUsers(%q): Fehler erwartet", bad)
		}
	}
}

func TestJWTRoundtrip(t *testing.T) {
	secret := []byte("test-secret")
	now := time.Unix(1700000000, 0)

	token := SignHS256(secret, Claims{
		Sub: "alice", Iss: "goteach",
		Iat: now.Unix(), Exp: now.Add(time.Hour).Unix(),
	})

	c, err := VerifyHS256(secret, token, now)

	if err != nil {
		t.Fatalf("VerifyHS256: %v", err)
	}

	if c.Sub != "alice" || c.Iss != "goteach" {
		t.Errorf("Claims falsch: %+v", c)
	}
}

func TestJWTExpired(t *testing.T) {
	secret := []byte("test-secret")
	now := time.Unix(1700000000, 0)

	token := SignHS256(secret, Claims{
		Sub: "alice", Iat: now.Add(-2 * time.Hour).Unix(),
		Exp: now.Add(-time.Hour).Unix(),
	})

	if _, err := VerifyHS256(secret, token, now); err == nil {
		t.Error("abgelaufenes Token akzeptiert")
	}

	// Innerhalb der Leeway-Toleranz bleibt das Token gültig.
	edge := SignHS256(secret, Claims{
		Sub: "alice", Iat: now.Unix(), Exp: now.Add(-ExpLeeway / 2).Unix(),
	})

	if _, err := VerifyHS256(secret, edge, now); err != nil {
		t.Errorf("Token innerhalb der Leeway abgelehnt: %v", err)
	}
}

func TestJWTTampered(t *testing.T) {
	secret := []byte("test-secret")
	now := time.Unix(1700000000, 0)

	token := SignHS256(secret, Claims{
		Sub: "alice", Iat: now.Unix(), Exp: now.Add(time.Hour).Unix(),
	})

	// Payload manipulieren: Signatur muss die Änderung erkennen.
	parts := strings.Split(token, ".")
	forged := SignHS256(secret, Claims{
		Sub: "mallory", Iat: now.Unix(), Exp: now.Add(time.Hour).Unix(),
	})
	forgedPayload := strings.Split(forged, ".")[1]

	if _, err := VerifyHS256(secret,
		parts[0]+"."+forgedPayload+"."+parts[2], now); err == nil {
		t.Error("manipuliertes Token akzeptiert")
	}

	if _, err := VerifyHS256([]byte("anderes-secret"), token, now); err == nil {
		t.Error("Token mit falschem Secret akzeptiert")
	}
}

func TestJWTAlgNone(t *testing.T) {
	secret := []byte("test-secret")
	now := time.Unix(1700000000, 0)

	// Token mit alg "none" und leerer Signatur — muss abgelehnt werden.
	header := b64.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := b64.EncodeToString([]byte(`{"sub":"alice","exp":1700003600}`))

	if _, err := VerifyHS256(secret, header+"."+payload+".", now); err == nil {
		t.Error("alg:none akzeptiert")
	}
}

func TestJWTGarbage(t *testing.T) {
	secret := []byte("test-secret")
	now := time.Unix(1700000000, 0)

	for _, bad := range []string{"", "abc", "a.b", "a.b.c.d", "ä.ö.ü"} {
		if _, err := VerifyHS256(secret, bad, now); err == nil {
			t.Errorf("VerifyHS256(%q): Fehler erwartet", bad)
		}
	}
}
