// JWT-Authentifizierung des Dienstes. Benutzer kommen aus AUTH_USERS
// ("name:pbkdf2-sha256$…,…"), das Token-Secret aus AUTH_JWT_SECRET.
// Ohne AUTH_USERS bleibt der Dienst offen (Log-Warnung) — lokale
// Entwicklung, Tests und bestehende Deployments laufen unverändert.
// AUTH_USERS ohne AUTH_JWT_SECRET ist Fehlkonfiguration: fail closed.
//
// Daneben steht requireMember: der Zugang der Galerie, der zusätzlich die
// offline ausgestellten Mitglieder-Tokens der Vereinsseite annimmt
// (ES256, öffentlicher Schlüssel aus FLB_JWT_PUBLIC_JWK) — und der im
// Unterschied zu requireAuth niemals offen läuft.
package server

import (
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/vmanke/goteach-prod/internal/auth"
)

// maxLoginBytes begrenzt den Login-Body; Name+Passwort sind klein.
const maxLoginBytes = 4 << 10

// defaultTokenTTL gilt, wenn AUTH_TOKEN_TTL fehlt.
const defaultTokenTTL = 24 * time.Hour

// tokenIssuer identifiziert diesen Dienst in den Claims.
const tokenIssuer = "goteach"

// authEnabled meldet, ob Logins konfiguriert sind (Env pro Request,
// wie katagoConfigured — Tests setzen die Variablen per t.Setenv).
func authEnabled() bool {
	return os.Getenv("AUTH_USERS") != ""
}

// authConfig liest und validiert die Auth-Umgebung. Fehler bedeuten
// Fehlkonfiguration, nicht „Auth aus" — die Aufrufer antworten dann 500.
func authConfig() (users map[string]string, secret []byte, err error) {
	users, err = auth.ParseUsers(os.Getenv("AUTH_USERS"))

	if err != nil {
		return nil, nil, err
	}

	s := os.Getenv("AUTH_JWT_SECRET")

	if s == "" {
		return nil, nil, errors.New("AUTH_JWT_SECRET fehlt (AUTH_USERS ist gesetzt)")
	}

	return users, []byte(s), nil
}

// tokenTTL liest AUTH_TOKEN_TTL (time.ParseDuration, z. B. "24h", "30m").
func tokenTTL() (time.Duration, error) {
	v := os.Getenv("AUTH_TOKEN_TTL")

	if v == "" {
		return defaultTokenTTL, nil
	}

	ttl, err := time.ParseDuration(v)

	if err != nil || ttl <= 0 {
		return 0, errors.New("AUTH_TOKEN_TTL unverständlich (erwartet z. B. \"24h\")")
	}

	return ttl, nil
}

// authMandatory meldet, ob offener Betrieb verboten ist
// (GOTEACH_REQUIRE_AUTH gesetzt und nicht "0"/"false").
//
// Bewusst ein ausdrücklicher Schalter statt einer Erkennung der Umgebung:
// Würde der Dienst selbst raten, ob er „in Produktion" läuft, bräche eine
// falsche Vermutung das nächste Deployment beim Start — und zwar genau
// dann, wenn niemand damit rechnet. So entscheidet der Betreiber.
func authMandatory() bool {
	v := strings.TrimSpace(os.Getenv("GOTEACH_REQUIRE_AUTH"))

	return v != "" && v != "0" && !strings.EqualFold(v, "false")
}

// validateAuthEnv prüft die Auth-Umgebung beim Serverstart, damit
// Fehlkonfiguration sofort auffällt statt erst beim ersten Login.
func validateAuthEnv() error {
	if !authEnabled() {
		// Ohne AUTH_USERS ist /analyze öffentlich. Lokal ist das gewollt;
		// auf einer öffentlich erreichbaren Maschine, die pro Anfrage
		// Minuten CPU verbrennt, ist es eine teure Überraschung. Wer
		// GOTEACH_REQUIRE_AUTH setzt, will lieber gar nicht starten.
		if authMandatory() {
			return errors.New(
				"GOTEACH_REQUIRE_AUTH ist gesetzt, aber AUTH_USERS fehlt — " +
					"der Dienst würde offen laufen")
		}

		return nil
	}

	if _, _, err := authConfig(); err != nil {
		return err
	}

	if _, err := tokenTTL(); err != nil {
		return err
	}

	// Dummy-Hash vorwärmen: sonst wäre der allererste Login-Versuch mit
	// unbekanntem Namen nach Prozessstart messbar langsamer (Erzeugung
	// zusätzlich zur Prüfung) — genau das Timing-Signal, das der Dummy
	// verhindern soll.
	_ = dummyHash()

	return nil
}

// warnAuthDisabled loggt genau einmal, dass der Dienst offen läuft.
var warnAuthDisabled = sync.OnceFunc(func() {
	log.Print("goteach-server: AUTH_USERS nicht gesetzt — /analyze läuft OHNE Login")
})

// dummyHash wird bei unbekannten Benutzern geprüft, damit die Antwortzeit
// nicht verrät, ob ein Benutzername existiert.
var dummyHash = sync.OnceValue(func() string {
	h, err := auth.HashPassword("goteach-dummy", auth.DefaultIterations)

	if err != nil {
		// crypto/rand versagt praktisch nie; im Zweifel bleibt der
		// Timing-Ausgleich aus, die Logik dahinter funktioniert weiter.
		return ""
	}

	return h
})

// loginRequest ist der JSON-Body von POST /login.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginResponse ist die 200-Antwort von POST /login.
type loginResponse struct {
	Token     string `json:"token"`
	TokenType string `json:"tokenType"`
	ExpiresAt string `json:"expiresAt"`
}

// handleLogin prüft Zugangsdaten und stellt ein JWT aus (POST /login).
func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		httpError(w, http.StatusMethodNotAllowed, "POST erwartet")

		return
	}

	if !authEnabled() {
		httpError(w, http.StatusNotFound,
			"Auth nicht konfiguriert (AUTH_USERS fehlt)")

		return
	}

	users, secret, err := authConfig()

	if err != nil {
		log.Printf("goteach-server: Auth fehlkonfiguriert: %v", err)
		httpError(w, http.StatusInternalServerError, "Auth fehlkonfiguriert")

		return
	}

	ttl, err := tokenTTL()

	if err != nil {
		log.Printf("goteach-server: Auth fehlkonfiguriert: %v", err)
		httpError(w, http.StatusInternalServerError, "Auth fehlkonfiguriert")

		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxLoginBytes+1))

	if err != nil || len(body) > maxLoginBytes {
		httpError(w, http.StatusBadRequest, "Body unlesbar oder zu groß")

		return
	}

	var req loginRequest

	if err := json.Unmarshal(body, &req); err != nil ||
		req.Username == "" || req.Password == "" {
		httpError(w, http.StatusBadRequest,
			`JSON {"username":…,"password":…} erwartet`)

		return
	}

	// Einheitliche 401-Antwort für unbekannten Namen und falsches Passwort;
	// bei unbekanntem Namen gleicht ein Dummy-Hash die Antwortzeit an.
	hash, known := users[req.Username]

	if !known {
		auth.VerifyPassword(req.Password, dummyHash())
		httpError(w, http.StatusUnauthorized, "Benutzername oder Passwort falsch")

		return
	}

	if !auth.VerifyPassword(req.Password, hash) {
		httpError(w, http.StatusUnauthorized, "Benutzername oder Passwort falsch")

		return
	}

	now := time.Now()
	exp := now.Add(ttl)

	token := auth.SignHS256(secret, auth.Claims{
		Sub: req.Username,
		Iss: tokenIssuer,
		Iat: now.Unix(),
		Exp: exp.Unix(),
	})

	writeJSON(w, http.StatusOK, loginResponse{
		Token:     token,
		TokenType: "Bearer",
		ExpiresAt: exp.UTC().Format(time.RFC3339),
	})
}

// requireAuth schützt einen Handler per JWT-Bearer-Token. Ohne
// konfigurierte Benutzer läuft der Handler offen (einmalige Warnung).
func requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authEnabled() {
			warnAuthDisabled()
			next(w, r)

			return
		}

		users, secret, err := authConfig()

		if err != nil {
			log.Printf("goteach-server: Auth fehlkonfiguriert: %v", err)
			httpError(w, http.StatusInternalServerError, "Auth fehlkonfiguriert")

			return
		}

		token, ok := bearerToken(r)

		if !ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			httpError(w, http.StatusUnauthorized,
				"Login nötig: Authorization: Bearer <Token> (Token via POST /login)")

			return
		}

		claims, err := auth.VerifyHS256(secret, token, time.Now())

		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			httpError(w, http.StatusUnauthorized, "Token ungültig: %v", err)

			return
		}

		// Signatur und Ablauf genügen nicht: Der Benutzer muss auch noch
		// existieren. So wirkt das Entfernen aus AUTH_USERS sofort, nicht
		// erst beim Token-Ablauf; der iss-Check lehnt fremde Aussteller ab.
		if _, ok := users[claims.Sub]; !ok || claims.Iss != tokenIssuer {
			w.Header().Set("WWW-Authenticate", "Bearer")
			httpError(w, http.StatusUnauthorized, "Token ungültig: Benutzer unbekannt")

			return
		}

		next(w, r)
	}
}

// memberKeyEnv trägt den öffentlichen Signierschlüssel der Vereinsseite
// (der JWK aus deren frontend/src/keys_generated.rs). Kein Geheimnis — er
// prüft Tokens, er stellt keine aus — und darf darum wie die CORS-Origins
// versioniert in der fly.toml stehen.
const memberKeyEnv = "FLB_JWT_PUBLIC_JWK"

// memberKey liest den Vereinsschlüssel aus der Umgebung. (nil, nil) heißt
// „nicht konfiguriert"; ein Fehler heißt „konfiguriert, aber kaputt" — und
// das darf nicht als „Weg nicht verfügbar" durchgehen.
func memberKey() (*ecdsa.PublicKey, error) {
	raw := strings.TrimSpace(os.Getenv(memberKeyEnv))

	if raw == "" {
		return nil, nil
	}

	pub, err := auth.PublicKeyFromJWK(raw)

	if err != nil {
		return nil, fmt.Errorf("%s: %w", memberKeyEnv, err)
	}

	return pub, nil
}

// requireMember schützt einen Handler und reicht den Inhaber weiter.
//
// Zwei Ausweise werden angenommen, in dieser Reihenfolge:
//
//  1. Der Mitglieder-Token der Vereinsseite (ES256, FLB_JWT_PUBLIC_JWK).
//     Den haben die Mitglieder ohnehin schon; sie brauchen kein zweites
//     Passwort, und der Browser schickt ihn still aus dem localStorage.
//  2. Das Dienst-Token aus POST /login (HS256, AUTH_USERS) — derselbe Weg,
//     den die Analyse-Seite geht, und der einzige, der sich serverseitig
//     sofort entziehen lässt.
//
// Anders als requireAuth läuft das hier NIE offen. Ohne Login ist /analyze
// eine Kostenfrage; eine offene Galerie wären Fotos von Vereinsmitgliedern
// im freien Netz. Ist gar kein Ausweis konfiguriert, ist das
// Fehlkonfiguration und keine Einladung.
func requireMember(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pub, err := memberKey()

		if err != nil {
			log.Printf("goteach-server: Auth fehlkonfiguriert: %v", err)
			httpError(w, http.StatusInternalServerError, "Auth fehlkonfiguriert")

			return
		}

		if pub == nil && !authEnabled() {
			log.Printf("goteach-server: Galerie ohne jeden Ausweis konfiguriert "+
				"(weder %s noch AUTH_USERS)", memberKeyEnv)
			httpError(w, http.StatusInternalServerError, "Auth fehlkonfiguriert")

			return
		}

		token, ok := bearerToken(r)

		if !ok {
			denyMember(w, "Login nötig: Authorization: Bearer <Token> "+
				"(Vereins-Token oder Token via POST /login)")

			return
		}

		now := time.Now()

		if pub != nil {
			if claims, err := auth.VerifyES256(pub, token, now); err == nil {
				next(w, r, claims.Sub)

				return
			}
		}

		if !authEnabled() {
			denyMember(w, "Token ungültig")

			return
		}

		users, secret, err := authConfig()

		if err != nil {
			log.Printf("goteach-server: Auth fehlkonfiguriert: %v", err)
			httpError(w, http.StatusInternalServerError, "Auth fehlkonfiguriert")

			return
		}

		claims, err := auth.VerifyHS256(secret, token, now)

		if err != nil {
			denyMember(w, "Token ungültig")

			return
		}

		// Wie in requireAuth: Signatur und Ablauf genügen nicht, der Benutzer
		// muss noch existieren — Entfernen aus AUTH_USERS wirkt sofort.
		if _, ok := users[claims.Sub]; !ok || claims.Iss != tokenIssuer {
			denyMember(w, "Token ungültig: Benutzer unbekannt")

			return
		}

		next(w, r, claims.Sub)
	}
}

// denyMember antwortet einheitlich 401. Einheitlich ist Absicht: welcher der
// beiden Wege gescheitert ist, geht den Aufrufer nichts an.
func denyMember(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	httpError(w, http.StatusUnauthorized, "%s", msg)
}

// bearerToken extrahiert das Token aus "Authorization: Bearer <Token>".
func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	scheme, token, ok := strings.Cut(header, " ")

	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}

	token = strings.TrimSpace(token)

	return token, token != ""
}
