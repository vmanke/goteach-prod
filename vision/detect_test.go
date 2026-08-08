package vision

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Die Tests starten sich selbst als Erkenner-Attrappe. Das vermeidet eine
// Abhängigkeit von einer Shell oder einem Python im Testlauf und prüft
// trotzdem den echten Subprozesspfad inklusive stdin, stdout und stderr.
const (
	envFake    = "GOTEACH_VISION_FAKE"
	envArgFile = "GOTEACH_VISION_ARGFILE"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(envFake); mode != "" {
		os.Exit(fakeDetector(mode))
	}

	os.Exit(m.Run())
}

// fakeDetector spielt die Python-CLI: PNG von stdin, Vertrag nach stdout.
func fakeDetector(mode string) int {
	if path := os.Getenv(envArgFile); path != "" {
		_ = os.WriteFile(path, []byte(strings.Join(os.Args[1:], " ")), 0o600)
	}

	switch mode {
	case "ok":
		fmt.Fprint(os.Stdout, `{"size":3,"komi":6.5,"rows":["X..",".O.","..."]}`)

		return 0

	case "garbage":
		fmt.Fprint(os.Stdout, "kein JSON")
		fmt.Fprintln(os.Stderr, "goteach-vision: Brett 3x3")

		return 0

	case "fail":
		fmt.Fprintln(os.Stderr, "goteach-vision: kein Brett erkannt: zu wenige Linien")

		return 1

	case "slow":
		time.Sleep(10 * time.Second)

		return 0
	}

	return 0
}

func fakeOptions(t *testing.T, mode string) Options {
	t.Helper()
	t.Setenv(envFake, mode)

	return Options{Command: os.Args[0]}
}

func TestDetectLiefertStellung(t *testing.T) {
	pos, err := Detect(context.Background(), []byte("PNG"), fakeOptions(t, "ok"))

	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if pos.Size != 3 || pos.Komi != 6.5 {
		t.Fatalf("Size %d Komi %v", pos.Size, pos.Komi)
	}

	g, err := pos.Game()

	if err != nil {
		t.Fatalf("Game: %v", err)
	}

	if len(g.Setup) != 2 {
		t.Fatalf("%d Setup-Steine, erwartet 2", len(g.Setup))
	}
}

func TestDetectReichtGroesseUndKomiWeiter(t *testing.T) {
	argFile := filepath.Join(t.TempDir(), "args")
	opt := fakeOptions(t, "ok")
	t.Setenv(envArgFile, argFile)

	komi := 7.5
	opt.Size = 19
	opt.Komi = &komi

	if _, err := Detect(context.Background(), []byte("PNG"), opt); err != nil {
		t.Fatalf("Detect: %v", err)
	}

	args, err := os.ReadFile(argFile)

	if err != nil {
		t.Fatalf("Argumente lesen: %v", err)
	}

	for _, want := range []string{"--size 19", "--komi 7.5"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("Argumente %q enthalten nicht %q", args, want)
		}
	}
}

func TestDetectMeldetFehlerMitStderr(t *testing.T) {
	_, err := Detect(context.Background(), []byte("PNG"), fakeOptions(t, "fail"))

	if err == nil {
		t.Fatal("Fehler erwartet")
	}

	// Ohne die stderr-Zeile stünde hier nur "exit status 1"; die Ursache
	// meldet der Erkenner aber genau dort.
	if !strings.Contains(err.Error(), "zu wenige Linien") {
		t.Fatalf("stderr fehlt in der Meldung: %v", err)
	}
}

func TestDetectMeldetUnbrauchbareAusgabe(t *testing.T) {
	_, err := Detect(context.Background(), []byte("PNG"), fakeOptions(t, "garbage"))

	if err == nil {
		t.Fatal("Fehler bei unlesbarer Ausgabe erwartet")
	}

	if !strings.Contains(err.Error(), "vision:") {
		t.Fatalf("unerwartete Meldung: %v", err)
	}
}

func TestDetectBrichtNachTimeoutAb(t *testing.T) {
	opt := fakeOptions(t, "slow")
	opt.Timeout = 200 * time.Millisecond

	started := time.Now()
	_, err := Detect(context.Background(), []byte("PNG"), opt)

	if err == nil {
		t.Fatal("Timeout-Fehler erwartet")
	}

	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("Abbruch dauerte %s — Timeout greift nicht", elapsed)
	}
}

func TestDetectOhneKonfigurationMeldetKlar(t *testing.T) {
	t.Setenv(EnvCommand, "")

	_, err := Detect(context.Background(), []byte("PNG"), Options{})

	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("ErrNotConfigured erwartet, erhalten: %v", err)
	}
}

func TestDetectLehntLeeresBildAb(t *testing.T) {
	if _, err := Detect(context.Background(), nil, fakeOptions(t, "ok")); err == nil {
		t.Fatal("Fehler bei leerem Bild erwartet")
	}
}

func TestConfiguredFolgtDerUmgebung(t *testing.T) {
	t.Setenv(EnvCommand, "")

	if Configured() {
		t.Fatal("ohne Umgebungsvariable darf nichts konfiguriert sein")
	}

	t.Setenv(EnvCommand, DefaultCommand)

	if !Configured() {
		t.Fatal("mit Umgebungsvariable muss Configured() wahr sein")
	}
}
