// Package vision ist die Brücke zu den Stufen 1–2 der Architektur
// (Homographie + preinformed U-Net). Die Bilderkennung selbst verbleibt
// bewusst im Python/ONNX-Stack (siehe README, Abschnitt Vision-Brücke);
// dieses Paket nimmt deren symbolische Ausgabe entgegen und macht sie der
// Go-Pipeline (Gruppen, Stärke, Teaching) verfügbar.
package vision

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vmanke/goteach-prod/board"
)

// Position ist das Austauschformat des Erkenners: rows[0] ist die oberste
// Brettzeile; Zeichen: '.' leer, 'X'/'B' schwarz, 'O'/'W' weiß.
type Position struct {
	Size int      `json:"size"`
	Rows []string `json:"rows"`
	Komi float64  `json:"komi,omitempty"`
}

// FromJSON parst die Erkennerausgabe.
func FromJSON(data []byte) (*Position, error) {
	var p Position

	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("vision: %w", err)
	}

	if p.Size == 0 {
		p.Size = len(p.Rows)
	}

	if len(p.Rows) != p.Size {
		return nil, fmt.Errorf("vision: %d Zeilen, %d erwartet",
			len(p.Rows), p.Size)
	}

	return &p, nil
}

// Board materialisiert die Position als Brett.
func (p *Position) Board() (*board.Board, error) {
	b := board.New(p.Size)

	for y, row := range p.Rows {
		row = strings.ReplaceAll(row, " ", "")

		if len(row) != p.Size {
			return nil, fmt.Errorf("vision: Zeile %d hat Länge %d, %d erwartet",
				y, len(row), p.Size)
		}

		for x := 0; x < p.Size; x++ {
			var c board.Color

			switch row[x] {
			case '.':
				continue

			case 'X', 'B', 'x', 'b':
				c = board.Black

			case 'O', 'W', 'o', 'w':
				c = board.White

			default:
				return nil, fmt.Errorf("vision: unbekanntes Zeichen %q", row[x])
			}

			if err := b.SetStone(board.Point{X: x, Y: y}, c); err != nil {
				return nil, err
			}
		}
	}

	return b, nil
}

// Game verpackt die Stellung als Partie ohne Zughistorie (initialStones),
// damit sie direkt durch teaching/katago analysierbar ist.
func (p *Position) Game() (*board.Game, error) {
	b, err := p.Board()

	if err != nil {
		return nil, err
	}

	g := &board.Game{Size: p.Size, Komi: p.Komi}

	for _, s := range b.Stones(board.Black) {
		g.Setup = append(g.Setup, board.Move{Color: board.Black, Point: s})
	}

	for _, s := range b.Stones(board.White) {
		g.Setup = append(g.Setup, board.Move{Color: board.White, Point: s})
	}

	return g, nil
}
