package board

import (
	"fmt"
	"strconv"
	"strings"
)

// Game ist eine geparste Partie (nur Hauptvariante).
type Game struct {
	Size  int
	Komi  float64
	Rules string

	// Setup sind vorab gesetzte Steine (AB/AW, z. B. Handicap).
	Setup []Move

	Moves []Move
}

type sgfNode struct {
	props map[string][]string
}

// ParseSGF parst eine SGF-Datei und liefert die Hauptvariante.
// Nebenvarianten werden bewusst verworfen; unbekannte Properties ignoriert.
func ParseSGF(data string) (*Game, error) {
	p := &sgfParser{src: data}
	nodes, err := p.parse()

	if err != nil {
		return nil, err
	}

	if len(nodes) == 0 {
		return nil, fmt.Errorf("sgf: keine Knoten gefunden")
	}

	g := &Game{Size: 19, Komi: 0, Rules: ""}

	// Wurzelknoten: Metadaten.
	root := nodes[0]

	if v, ok := root.props["SZ"]; ok && len(v) > 0 {
		// Rechteckige Bretter "m:n" werden nicht unterstützt → erster Wert.
		sz := strings.SplitN(v[0], ":", 2)[0]
		n, err := strconv.Atoi(strings.TrimSpace(sz))

		if err != nil || n < 2 || n > 25 {
			return nil, fmt.Errorf("sgf: ungültige Brettgröße %q", v[0])
		}

		g.Size = n
	}

	if v, ok := root.props["KM"]; ok && len(v) > 0 {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v[0]), 64); err == nil {
			g.Komi = f
		}
	}

	if v, ok := root.props["RU"]; ok && len(v) > 0 {
		g.Rules = strings.ToLower(strings.TrimSpace(v[0]))
	}

	for _, n := range nodes {
		if err := n.appendSetup(g, "AB", Black); err != nil {
			return nil, err
		}

		if err := n.appendSetup(g, "AW", White); err != nil {
			return nil, err
		}

		if err := n.appendMove(g, "B", Black); err != nil {
			return nil, err
		}

		if err := n.appendMove(g, "W", White); err != nil {
			return nil, err
		}
	}

	return g, nil
}

func (n sgfNode) appendSetup(g *Game, key string, c Color) error {
	vals, ok := n.props[key]

	if !ok {
		return nil
	}

	for _, v := range vals {
		pt, pass, err := FromSGF(v, g.Size)

		if err != nil {
			return fmt.Errorf("sgf: %s: %w", key, err)
		}

		if !pass {
			g.Setup = append(g.Setup, Move{Color: c, Point: pt})
		}
	}

	return nil
}

func (n sgfNode) appendMove(g *Game, key string, c Color) error {
	vals, ok := n.props[key]

	if !ok || len(vals) == 0 {
		return nil
	}

	pt, pass, err := FromSGF(vals[0], g.Size)

	if err != nil {
		return fmt.Errorf("sgf: %s: %w", key, err)
	}

	g.Moves = append(g.Moves, Move{Color: c, Point: pt, Pass: pass})

	return nil
}

// Positions spielt die Partie nach und liefert alle N+1 Stellungen
// (Index i = Stellung vor Zug i). Illegale Züge brechen mit Fehler ab.
func (g *Game) Positions() ([]*Board, error) {
	b := New(g.Size)

	for _, s := range g.Setup {
		if err := b.SetStone(s.Point, s.Color); err != nil {
			return nil, err
		}
	}

	out := make([]*Board, 0, len(g.Moves)+1)
	out = append(out, b.Clone())

	for i, m := range g.Moves {
		if err := b.Play(m); err != nil {
			return nil, fmt.Errorf("sgf: Zug %d (%s %s) illegal: %w",
				i+1, m.Color, ToGTP(m.Point, g.Size), err)
		}

		out = append(out, b.Clone())
	}

	return out, nil
}

// --- Parser-Interna -------------------------------------------------------

type sgfParser struct {
	src string
	pos int
}

func (p *sgfParser) parse() ([]sgfNode, error) {
	p.skipWS()

	if p.pos >= len(p.src) || p.src[p.pos] != '(' {
		return nil, fmt.Errorf("sgf: '(' erwartet")
	}

	p.pos++
	var nodes []sgfNode

	if err := p.parseSequence(&nodes); err != nil {
		return nil, err
	}

	return nodes, nil
}

// parseSequence liest Knoten der aktuellen Ebene; bei Verzweigungen wird nur
// die erste Variante verfolgt, weitere werden balanciert übersprungen.
func (p *sgfParser) parseSequence(nodes *[]sgfNode) error {
	firstBranchTaken := false

	for p.pos < len(p.src) {
		p.skipWS()

		if p.pos >= len(p.src) {
			return nil
		}

		switch p.src[p.pos] {
		case ';':
			p.pos++
			n, err := p.parseNode()

			if err != nil {
				return err
			}

			*nodes = append(*nodes, n)

		case '(':
			p.pos++

			if firstBranchTaken {
				if err := p.skipTree(); err != nil {
					return err
				}
			} else {
				firstBranchTaken = true

				if err := p.parseSequence(nodes); err != nil {
					return err
				}
			}

		case ')':
			p.pos++

			return nil

		default:
			return fmt.Errorf("sgf: unerwartetes Zeichen %q an Position %d",
				p.src[p.pos], p.pos)
		}
	}

	return nil
}

// skipTree überspringt einen bereits geöffneten Baum balanciert und
// wertbewusst ('(' / ')' innerhalb von [...] zählen nicht).
func (p *sgfParser) skipTree() error {
	depth := 1

	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case '[':
			p.pos++
			p.skipValueBody()

		case '(':
			depth++
			p.pos++

		case ')':
			depth--
			p.pos++

			if depth == 0 {
				return nil
			}

		default:
			p.pos++
		}
	}

	return fmt.Errorf("sgf: unbalancierte Klammern")
}

func (p *sgfParser) parseNode() (sgfNode, error) {
	n := sgfNode{props: map[string][]string{}}

	for {
		p.skipWS()

		if p.pos >= len(p.src) {
			return n, nil
		}

		ch := p.src[p.pos]

		if ch < 'A' || ch > 'Z' {
			return n, nil
		}

		start := p.pos

		for p.pos < len(p.src) && p.src[p.pos] >= 'A' && p.src[p.pos] <= 'Z' {
			p.pos++
		}

		ident := p.src[start:p.pos]
		p.skipWS()

		if p.pos >= len(p.src) || p.src[p.pos] != '[' {
			return n, fmt.Errorf("sgf: '[' nach %s erwartet", ident)
		}

		for p.pos < len(p.src) && p.src[p.pos] == '[' {
			p.pos++
			val := p.readValueBody()
			n.props[ident] = append(n.props[ident], val)
			p.skipWS()
		}
	}
}

// readValueBody liest bis zum unmaskierten ']' und löst "\x"-Escapes auf.
func (p *sgfParser) readValueBody() string {
	var sb strings.Builder

	for p.pos < len(p.src) {
		ch := p.src[p.pos]

		if ch == '\\' && p.pos+1 < len(p.src) {
			sb.WriteByte(p.src[p.pos+1])
			p.pos += 2

			continue
		}

		if ch == ']' {
			p.pos++

			return sb.String()
		}

		sb.WriteByte(ch)
		p.pos++
	}

	return sb.String()
}

func (p *sgfParser) skipValueBody() {
	for p.pos < len(p.src) {
		ch := p.src[p.pos]

		if ch == '\\' && p.pos+1 < len(p.src) {
			p.pos += 2

			continue
		}

		p.pos++

		if ch == ']' {
			return
		}
	}
}

func (p *sgfParser) skipWS() {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}
