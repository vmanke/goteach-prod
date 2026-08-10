package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// DefaultIterations ist die PBKDF2-Iterationszahl für neue Hashes
// (OWASP-Empfehlung für PBKDF2-HMAC-SHA256).
const DefaultIterations = 600000

// hashPrefix kennzeichnet das einzige unterstützte Hash-Format.
const hashPrefix = "pbkdf2-sha256"

// saltLen und keyLen sind bewusst fix: 16-Byte-Salt, 32-Byte-Schlüssel.
const (
	saltLen = 16
	keyLen  = 32
)

// b64 ist RawURLEncoding: ohne "="-Padding, damit die Hashes unbeschadet
// durch internal/dotenv kommen (das schneidet am ersten "=" und trimmt
// Anführungszeichen).
var b64 = base64.RawURLEncoding

// HashPassword erzeugt einen Hash im Format
// pbkdf2-sha256$<iterationen>$<salt-b64url>$<hash-b64url>.
func HashPassword(password string, iter int) (string, error) {
	if password == "" {
		return "", fmt.Errorf("auth: leeres Passwort")
	}

	if iter < 1 {
		return "", fmt.Errorf("auth: Iterationen < 1")
	}

	salt := make([]byte, saltLen)

	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: Salt erzeugen: %w", err)
	}

	key := Key([]byte(password), salt, iter, keyLen)

	return fmt.Sprintf("%s$%d$%s$%s",
		hashPrefix, iter, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword prüft ein Passwort gegen einen kodierten Hash; Vergleich
// in konstanter Zeit. Unlesbare Hashes gelten als „stimmt nicht".
func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")

	if len(parts) != 4 || parts[0] != hashPrefix {
		return false
	}

	iter, err := strconv.Atoi(parts[1])

	if err != nil || iter < 1 {
		return false
	}

	salt, err := b64.DecodeString(parts[2])

	if err != nil {
		return false
	}

	want, err := b64.DecodeString(parts[3])

	if err != nil || len(want) == 0 {
		return false
	}

	got := Key([]byte(password), salt, iter, len(want))

	return hmac.Equal(got, want)
}

// ParseUsers zerlegt den Wert von AUTH_USERS: kommagetrennte Einträge
// "name:hash". Namen dürfen weder ":" noch "," enthalten; jeder Hash muss
// das bekannte Format tragen, damit Tippfehler beim Deployment sofort
// auffallen statt still jeden Login abzulehnen.
func ParseUsers(env string) (map[string]string, error) {
	users := map[string]string{}

	for _, entry := range strings.Split(env, ",") {
		entry = strings.TrimSpace(entry)

		if entry == "" {
			continue
		}

		name, hash, ok := strings.Cut(entry, ":")
		name = strings.TrimSpace(name)
		hash = strings.TrimSpace(hash)

		if !ok || name == "" || hash == "" {
			return nil, fmt.Errorf("auth: Eintrag ohne \"name:hash\": %q", entry)
		}

		if !strings.HasPrefix(hash, hashPrefix+"$") {
			return nil, fmt.Errorf(
				"auth: Hash für %q hat nicht das Format %s$…", name, hashPrefix)
		}

		if _, dup := users[name]; dup {
			return nil, fmt.Errorf("auth: Benutzer %q doppelt", name)
		}

		users[name] = hash
	}

	if len(users) == 0 {
		return nil, fmt.Errorf("auth: keine Benutzer in AUTH_USERS")
	}

	return users, nil
}
