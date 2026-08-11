package server

import (
	"strings"
	"testing"
)

// Ohne AUTH_USERS läuft der Dienst offen — das bleibt der Standard, damit
// lokale Entwicklung und Tests unverändert funktionieren.
func TestValidateAuthEnvOpenByDefault(t *testing.T) {
	t.Setenv("AUTH_USERS", "")
	t.Setenv("GOTEACH_REQUIRE_AUTH", "")

	if err := validateAuthEnv(); err != nil {
		t.Fatalf("offener Betrieb sollte erlaubt sein, Fehler: %v", err)
	}
}

// Mit GOTEACH_REQUIRE_AUTH verweigert der Dienst den Start, statt still
// öffentlich zu werden.
func TestValidateAuthEnvRefusesOpenWhenRequired(t *testing.T) {
	for _, v := range []string{"1", "true", "yes"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("AUTH_USERS", "")
			t.Setenv("GOTEACH_REQUIRE_AUTH", v)

			err := validateAuthEnv()

			if err == nil {
				t.Fatal("Start ohne AUTH_USERS wurde erlaubt")
			}

			if !strings.Contains(err.Error(), "AUTH_USERS") {
				t.Fatalf("Fehlermeldung nennt AUTH_USERS nicht: %v", err)
			}
		})
	}
}

// "0" und "false" schalten den Zwang wieder ab — sonst könnte man ihn in
// einer geerbten Umgebung nicht mehr loswerden.
func TestValidateAuthEnvRequireOff(t *testing.T) {
	for _, v := range []string{"0", "false", "False"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("AUTH_USERS", "")
			t.Setenv("GOTEACH_REQUIRE_AUTH", v)

			if err := validateAuthEnv(); err != nil {
				t.Fatalf("%q sollte den Zwang abschalten, Fehler: %v", v, err)
			}
		})
	}
}

// Sind Benutzer konfiguriert, ändert der Schalter nichts: die bestehende
// Prüfung auf AUTH_JWT_SECRET bleibt maßgeblich.
func TestValidateAuthEnvRequiredWithUsers(t *testing.T) {
	t.Setenv("AUTH_USERS", "alice:"+testUserHash)
	t.Setenv("GOTEACH_REQUIRE_AUTH", "1")
	t.Setenv("AUTH_JWT_SECRET", "")

	if err := validateAuthEnv(); err == nil {
		t.Fatal("AUTH_USERS ohne AUTH_JWT_SECRET muss weiterhin scheitern")
	}

	t.Setenv("AUTH_JWT_SECRET", "geheim")

	if err := validateAuthEnv(); err != nil {
		t.Fatalf("vollständige Konfiguration abgelehnt: %v", err)
	}
}
