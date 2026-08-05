// Package dotenv lädt KEY=VALUE-Paare aus einer .env-Datei in die
// Prozessumgebung — ohne Fremdabhängigkeit. Bereits gesetzte Variablen
// werden nicht überschrieben. Werte werden nie geloggt.
package dotenv

import (
	"bufio"
	"os"
	"strings"
)

// Load liest die Datei path; fehlende Datei ist kein Fehler.
func Load(path string) error {
	f, err := os.Open(path)

	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return err
	}

	defer f.Close()

	sc := bufio.NewScanner(f)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, val, ok := strings.Cut(line, "=")

		if !ok {
			continue
		}

		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)

		if key == "" || os.Getenv(key) != "" {
			continue
		}

		if err := os.Setenv(key, val); err != nil {
			return err
		}
	}

	return sc.Err()
}
