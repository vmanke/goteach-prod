// Package capped puffert die Ausgabe eines Kindprozesses mit harter
// Obergrenze.
//
// Ein bytes.Buffer wächst unbegrenzt. Wird die Grenze erst nach dem
// Prozesslauf geprüft, hat ein Kindprozess, der viel schreibt — ein
// Fehlstart, eine Endlosschleife, ein falsch konfiguriertes Kommando — den
// Speicher längst gefüllt. Die Prüfung muss deshalb beim Schreiben greifen
// und den Lauf abbrechen, statt hinterher festzustellen, dass es zu viel war.
package capped

import (
	"bytes"
	"context"
	"fmt"
)

// Buffer nimmt bis zu Limit Bytes auf und bricht darüber hinaus ab.
type Buffer struct {
	buf      bytes.Buffer
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

// New liefert einen Puffer, der bei Überschreitung cancel aufruft — damit
// endet der Prozesslauf sofort statt erst nach dem Timeout.
func New(limit int, cancel context.CancelFunc) *Buffer {
	return &Buffer{limit: limit, cancel: cancel}
}

// Write erfüllt io.Writer.
//
// Über der Grenze werden weitere Bytes verworfen und trotzdem als
// geschrieben gemeldet: Ein Fehler an dieser Stelle bräche das Kopieren aus
// der Pipe ab und überdeckte den eigentlichen Grund mit einem
// Schreibfehler. Den Abbruch besorgt cancel, die Diagnose Overflow.
func (b *Buffer) Write(p []byte) (int, error) {
	if b.overflow {
		return len(p), nil
	}

	if remaining := b.limit - b.buf.Len(); len(p) > remaining {
		if remaining > 0 {
			b.buf.Write(p[:remaining])
		}

		b.overflow = true

		if b.cancel != nil {
			b.cancel()
		}

		return len(p), nil
	}

	return b.buf.Write(p)
}

// Overflow meldet, ob die Grenze überschritten wurde.
func (b *Buffer) Overflow() bool {
	return b.overflow
}

// Limit liefert die vereinbarte Obergrenze.
func (b *Buffer) Limit() int {
	return b.limit
}

// Bytes liefert das Aufgenommene (höchstens Limit Bytes).
func (b *Buffer) Bytes() []byte {
	return b.buf.Bytes()
}

// String liefert das Aufgenommene als Text.
func (b *Buffer) String() string {
	return b.buf.String()
}

// Len liefert die Zahl der aufgenommenen Bytes.
func (b *Buffer) Len() int {
	return b.buf.Len()
}

// Err liefert einen sprechenden Fehler, falls die Grenze gerissen wurde.
func (b *Buffer) Err(what string) error {
	if !b.overflow {
		return nil
	}

	return fmt.Errorf("%s größer als %d Bytes — Lauf abgebrochen", what, b.limit)
}
