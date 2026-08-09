package teaching

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/vmanke/goteach-prod/board"
)

// EnvSalienceCommand benennt das Kommando des gelernten Salienzmoduls. Ist es
// nicht gesetzt, bleibt es bei der deterministischen Fensterung über den
// Kopplungsgraphen — die trägt für sich allein.
const EnvSalienceCommand = "GOTEACH_SALIENCE_CMD"

// DefaultSalienceCommand liest den Partieverlauf von stdin und schreibt die
// Fenster nach stdout.
const DefaultSalienceCommand = "python3 -m goteach_salience score -"

// salienceTimeout deckelt einen Aufruf des Moduls.
const salienceTimeout = 120 * time.Second

// maxSalienceOutput begrenzt, was vom Kindprozess entgegengenommen wird.
const maxSalienceOutput = 8 << 20

// ErrSalienceNotConfigured meldet, dass kein Salienzmodul eingerichtet ist.
var ErrSalienceNotConfigured = errors.New(
	"teaching: kein Salienzmodul konfiguriert (" + EnvSalienceCommand + " nicht gesetzt)")

// SalienceWindow ist ein Fenster, wie es das gelernte Modul liefert.
type SalienceWindow struct {
	FromTurn int      `json:"fromTurn"`
	ToTurn   int      `json:"toTurn"`
	Points   []string `json:"points"`
	Score    float64  `json:"score"`
}

// SalienceConfigured meldet, ob ein Salienzmodul eingerichtet ist.
func SalienceConfigured() bool {
	return strings.TrimSpace(os.Getenv(EnvSalienceCommand)) != ""
}

// salienceInput ist der Vertrag zum Modul: je Stellung die Brettzeilen und
// das Ownership-Feld.
type salienceInput struct {
	Size  int            `json:"size"`
	Turns []salienceTurn `json:"turns"`
}

type salienceTurn struct {
	Rows      []string  `json:"rows"`
	Ownership []float64 `json:"ownership"`
}

// requestSalience schickt den Partieverlauf durch das Modul.
//
// Wie bei der Bilderkennung ein Subprozess und kein Netzdienst: Die Go-Seite
// bleibt ohne externe Abhängigkeiten, und das Modul bleibt austauschbar — es
// muss allein diesen JSON-Vertrag einhalten.
func requestSalience(ctx context.Context, size int, positions []*board.Board,
	ownership [][]float64, lo int, command string) ([]SalienceWindow, error) {

	if command == "" {
		command = strings.TrimSpace(os.Getenv(EnvSalienceCommand))
	}

	if command == "" {
		return nil, ErrSalienceNotConfigured
	}

	payload := salienceInput{Size: size}

	for k := range ownership {
		turn := lo + k

		if turn < 0 || turn >= len(positions) {
			continue
		}

		payload.Turns = append(payload.Turns, salienceTurn{
			Rows:      boardRows(positions[turn]),
			Ownership: ownership[k],
		})
	}

	if len(payload.Turns) < 2 {
		return nil, fmt.Errorf("teaching: zu wenige Stellungen für die Salienz")
	}

	body, err := json.Marshal(payload)

	if err != nil {
		return nil, err
	}

	fields := strings.Fields(command)

	if len(fields) == 0 {
		return nil, fmt.Errorf("teaching: %s ist leer", EnvSalienceCommand)
	}

	ctx, cancel := context.WithTimeout(ctx, salienceTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, fields[0], fields[1:]...)
	cmd.Stdin = bytes.NewReader(body)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("teaching: Salienz abgebrochen: %w", ctx.Err())
		}

		return nil, fmt.Errorf("teaching: %s fehlgeschlagen: %w%s",
			fields[0], err, lastLine(&stderr))
	}

	if stdout.Len() > maxSalienceOutput {
		return nil, fmt.Errorf("teaching: Salienz-Ausgabe größer als %d Bytes",
			maxSalienceOutput)
	}

	var result struct {
		Windows []SalienceWindow `json:"windows"`
	}

	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &result); err != nil {
		return nil, fmt.Errorf("teaching: Salienz-Ausgabe unlesbar: %w%s",
			err, lastLine(&stderr))
	}

	return result.Windows, nil
}

// boardRows schreibt ein Brett im Zeilenformat des Vision-Vertrags.
func boardRows(b *board.Board) []string {
	rows := make([]string, b.Size)

	for y := 0; y < b.Size; y++ {
		var sb strings.Builder

		for x := 0; x < b.Size; x++ {
			switch b.Get(board.Point{X: x, Y: y}) {
			case board.Black:
				sb.WriteByte('X')
			case board.White:
				sb.WriteByte('O')
			default:
				sb.WriteByte('.')
			}
		}

		rows[y] = sb.String()
	}

	return rows
}

// lastLine hängt die letzte stderr-Zeile an eine Fehlermeldung; ohne sie
// stünde dort nur "exit status 1".
func lastLine(stderr *bytes.Buffer) string {
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")

	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return " (" + line + ")"
		}
	}

	return ""
}

// salienceRegions übersetzt die Fenster in Brettpunkte.
func salienceRegions(windows []SalienceWindow, size int) [][]board.Point {
	out := make([][]board.Point, 0, len(windows))

	for _, w := range windows {
		var region []board.Point

		for _, coord := range w.Points {
			point, pass, err := board.FromGTP(coord, size)

			if err != nil || pass {
				continue
			}

			region = append(region, point)
		}

		if len(region) > 0 {
			out = append(out, region)
		}
	}

	return out
}
