package server

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vmanke/goteach-prod/vision"
)

// fakeDetector legt ein ausführbares Skript ab, das sich wie die Python-CLI
// verhält: PNG von stdin, JSON-Vertrag nach stdout. Damit prüfen die Tests
// den echten Subprozesspfad, ohne einen Python-Stack vorauszusetzen.
func fakeDetector(t *testing.T, script string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "erkenner")
	body := "#!/bin/sh\ncat >/dev/null\n" + script + "\n"

	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("Attrappe schreiben: %v", err)
	}

	t.Setenv(vision.EnvCommand, path)
}

func imageRequest(t *testing.T, field string, png []byte, query string) *http.Request {
	t.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile(field, "brett.png")

	if err != nil {
		t.Fatalf("Formular: %v", err)
	}

	if _, err := part.Write(png); err != nil {
		t.Fatalf("Formular: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("Formular: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/analyze"+query, &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	return req
}

// pngHeader ist der PNG-Magic-Header; der Inhalt ist für die Attrappe egal,
// aber der Upload soll wie ein Bild aussehen.
var pngHeader = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}

func TestAnalyzeBildUploadLiefertStellungsbericht(t *testing.T) {
	fakeDetector(t, `echo '{"size":9,"komi":6.5,"rows":`+
		`["....X....","....O....",".........",".........",".........",`+
		`".........",".........",".........","........."]}'`)

	rr := serve(t, imageRequest(t, "image", pngHeader, "?visits=5"))

	if rr.Code != http.StatusOK {
		t.Fatalf("Status %d: %s", rr.Code, rr.Body.String())
	}

	got := decodeAnalyze(t, rr)

	if got.Size != 9 {
		t.Fatalf("Size = %d, erwartet 9", got.Size)
	}

	if got.Position == nil {
		t.Fatal("Stellungsbericht fehlt")
	}

	// Ein Foto zeigt eine Stellung, keine Partie: kein Teaching pro Zug.
	if len(got.Reports) != 0 {
		t.Fatalf("%d Zug-Reports, erwartet 0", len(got.Reports))
	}

	if got.Position.Stones != 2 {
		t.Fatalf("Stones = %d, erwartet 2", got.Position.Stones)
	}

	if got.Position.Text == "" {
		t.Fatal("Lehrtext fehlt")
	}
}

func TestAnalyzeBildOhneErkennerMeldet501(t *testing.T) {
	t.Setenv(vision.EnvCommand, "")

	rr := serve(t, imageRequest(t, "image", pngHeader, ""))

	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("Status %d, erwartet 501: %s", rr.Code, rr.Body.String())
	}

	if !strings.Contains(rr.Body.String(), vision.EnvCommand) {
		t.Fatalf("Hinweis auf die Umgebungsvariable fehlt: %s", rr.Body.String())
	}
}

func TestAnalyzeBildErkennerFehlerWirdWeitergereicht(t *testing.T) {
	fakeDetector(t, `echo "goteach-vision: kein Brett erkannt" >&2; exit 1`)

	rr := serve(t, imageRequest(t, "image", pngHeader, ""))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("Status %d, erwartet 400: %s", rr.Code, rr.Body.String())
	}

	if !strings.Contains(rr.Body.String(), "kein Brett erkannt") {
		t.Fatalf("Ursache fehlt in der Antwort: %s", rr.Body.String())
	}
}

func TestAnalyzeBildReichtGroesseWeiter(t *testing.T) {
	// Die Attrappe gibt die empfangenen Argumente als Fehlermeldung zurück;
	// so lässt sich prüfen, dass --size tatsächlich ankommt.
	fakeDetector(t, `echo "args: $*" >&2; exit 1`)

	rr := serve(t, imageRequest(t, "image", pngHeader, "?size=13"))

	if !strings.Contains(rr.Body.String(), "--size 13") {
		t.Fatalf("--size wurde nicht weitergereicht: %s", rr.Body.String())
	}
}

func TestAnalyzeBildLehntUnsinnigeGroesseAb(t *testing.T) {
	fakeDetector(t, `echo '{"size":9,"rows":[".........","...","..."]}'`)

	rr := serve(t, imageRequest(t, "image", pngHeader, "?size=11"))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("Status %d, erwartet 400: %s", rr.Code, rr.Body.String())
	}
}

func TestAnalyzeSGFBleibtUnberuehrt(t *testing.T) {
	// Regressionsschutz: Der Bildpfad darf den bestehenden SGF-Pfad nicht
	// verändern, auch nicht wenn ein Erkenner konfiguriert ist.
	fakeDetector(t, `echo '{"size":9,"rows":["........."]}'`)

	rr := serve(t, httptest.NewRequest(http.MethodPost, "/analyze?visits=5",
		strings.NewReader("(;GM[1]FF[4]SZ[9]KM[6.5];B[ee];W[gg])")))

	if rr.Code != http.StatusOK {
		t.Fatalf("Status %d: %s", rr.Code, rr.Body.String())
	}

	got := decodeAnalyze(t, rr)

	if len(got.Reports) != 2 {
		t.Fatalf("%d Reports, erwartet 2", len(got.Reports))
	}

	if got.Position != nil {
		t.Fatal("Stellungsbericht darf beim SGF-Pfad nicht gesetzt sein")
	}
}
