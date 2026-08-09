package vision

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/vmanke/goteach-prod/internal/capped"
)

// EnvCommand benennt das Erkennungskommando. Ist es nicht gesetzt, ist die
// Bilderkennung auf dieser Instanz schlicht nicht eingerichtet — der
// Vercel-Fall, wo kein Python-Stack mitläuft.
const EnvCommand = "GOTEACH_VISION_CMD"

// DefaultCommand liest das PNG von stdin und schreibt den JSON-Vertrag nach
// stdout; genau so ist die Python-CLI gebaut.
const DefaultCommand = "python3 -m goteach_vision detect -"

// DefaultTimeout deckelt einen Erkennungslauf. Die Erkennung braucht auf
// üblichen Bildern deutlich unter einer Sekunde; das Limit fängt einen
// hängenden Kindprozess ab, nicht den Normalfall.
const DefaultTimeout = 60 * time.Second

// maxOutput begrenzt, was vom Kindprozess entgegengenommen wird. Der Vertrag
// ist eine Handvoll Kilobyte; alles darüber ist ein Fehlerfall.
const maxOutput = 1 << 20

// maxStderr begrenzt die Diagnoseausgabe. Gebraucht wird davon nur die
// letzte Zeile.
const maxStderr = 1 << 16

// ErrNotConfigured meldet, dass auf dieser Instanz kein Erkenner eingerichtet
// ist. Aufrufer sollen das von einem Erkennungsfehler unterscheiden können:
// Ersteres ist Konfiguration, Letzteres ein Problem mit dem Bild.
var ErrNotConfigured = errors.New("vision: kein Erkenner konfiguriert (" +
	EnvCommand + " nicht gesetzt)")

// Options steuert einen Erkennungslauf.
type Options struct {
	// Command überschreibt das Kommando; leer = Umgebung, dann Default.
	Command string

	// Size erzwingt eine Brettgröße (9, 13, 19); 0 = automatisch.
	Size int

	// Komi wird in die Ausgabe des Erkenners übernommen.
	Komi *float64

	// Timeout begrenzt die Laufzeit; 0 = DefaultTimeout.
	Timeout time.Duration
}

// Configured meldet, ob ein Erkenner eingerichtet ist.
func Configured() bool {
	return strings.TrimSpace(os.Getenv(EnvCommand)) != ""
}

// Detect schickt ein PNG durch den Erkenner und liefert die Stellung.
//
// Die Brücke ist bewusst ein Subprozess und kein Netzdienst: Die Go-Seite
// bleibt ohne externe Abhängigkeiten (nur Standardbibliothek), und der
// Python-Stack bleibt austauschbar — er muss allein den JSON-Vertrag aus
// adapter.go einhalten.
func Detect(ctx context.Context, png []byte, opt Options) (*Position, error) {
	command := opt.Command

	if command == "" {
		command = strings.TrimSpace(os.Getenv(EnvCommand))
	}

	// Bewusst kein stiller Rückfall auf DefaultCommand: Wo kein Python-Stack
	// liegt, soll die Antwort "nicht eingerichtet" lauten und nicht ein
	// Prozessstart, der mit "python3: not found" scheitert.
	if command == "" {
		return nil, ErrNotConfigured
	}

	if len(png) == 0 {
		return nil, fmt.Errorf("vision: leeres Bild")
	}

	fields := strings.Fields(command)

	if len(fields) == 0 {
		return nil, fmt.Errorf("vision: %s ist leer", EnvCommand)
	}

	args := append(fields[1:], flags(opt)...)

	timeout := opt.Timeout

	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, fields[0], args...)
	cmd.Stdin = bytes.NewReader(png)

	// Gedeckelt schon beim Schreiben, nicht erst danach: Ein Kommando, das
	// unerwartet viel ausgibt, würde sonst erst den Speicher füllen und dann
	// abgewiesen werden.
	stdout := capped.New(maxOutput, cancel)
	stderr := capped.New(maxStderr, nil)

	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()

	if err := stdout.Err("vision: Ausgabe"); err != nil {
		return nil, err
	}

	if runErr != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("vision: Erkennung abgebrochen nach %s: %w",
				timeout, ctx.Err())
		}

		return nil, fmt.Errorf("vision: %s fehlgeschlagen: %w%s",
			fields[0], runErr, diagnostics(stderr))
	}

	pos, err := FromJSON(bytes.TrimSpace(stdout.Bytes()))

	if err != nil {
		return nil, fmt.Errorf("%w%s", err, diagnostics(stderr))
	}

	return pos, nil
}

// flags übersetzt die Optionen in Kommandozeilenargumente der Python-CLI.
func flags(opt Options) []string {
	var out []string

	if opt.Size > 0 {
		out = append(out, "--size", strconv.Itoa(opt.Size))
	}

	if opt.Komi != nil {
		out = append(out, "--komi", strconv.FormatFloat(*opt.Komi, 'f', -1, 64))
	}

	return out
}

// diagnostics hängt die letzte stderr-Zeile des Erkenners an eine Fehlermeldung.
// Ohne sie stünde nur "exit status 1" da — die eigentliche Ursache meldet der
// Erkenner aber genau dort.
func diagnostics(stderr *capped.Buffer) string {
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")

	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return " (" + line + ")"
		}
	}

	return ""
}
