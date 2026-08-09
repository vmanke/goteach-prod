package capped

import (
	"context"
	"strings"
	"testing"
)

func TestUnterhalbDerGrenzeBleibtAllesErhalten(t *testing.T) {
	b := New(100, nil)

	if _, err := b.Write([]byte("hallo")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if b.String() != "hallo" {
		t.Fatalf("Inhalt %q", b.String())
	}

	if b.Overflow() {
		t.Fatal("kein Überlauf erwartet")
	}

	if err := b.Err("Ausgabe"); err != nil {
		t.Fatalf("Err: %v", err)
	}
}

func TestUeberDerGrenzeWirdAbgeschnitten(t *testing.T) {
	b := New(8, nil)

	if _, err := b.Write([]byte("0123456789abcdef")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if b.Len() != 8 {
		t.Fatalf("%d Bytes aufgenommen, erwartet 8", b.Len())
	}

	if !b.Overflow() {
		t.Fatal("Überlauf erwartet")
	}

	err := b.Err("Ausgabe")

	if err == nil {
		t.Fatal("Fehler nach Überlauf erwartet")
	}

	if !strings.Contains(err.Error(), "8") {
		t.Fatalf("Grenze fehlt in der Meldung: %v", err)
	}
}

func TestSchreibenMeldetNieEinenFehler(t *testing.T) {
	// Ein Schreibfehler bräche das Kopieren aus der Prozess-Pipe ab und
	// überdeckte den eigentlichen Grund. Der Abbruch läuft über cancel.
	b := New(4, nil)

	n, err := b.Write([]byte("viel zu lang"))

	if err != nil {
		t.Fatalf("Write meldete %v", err)
	}

	if n != len("viel zu lang") {
		t.Fatalf("Write meldete %d geschriebene Bytes", n)
	}

	// Auch danach bleibt Write folgenlos statt zu scheitern.
	if _, err := b.Write([]byte("noch mehr")); err != nil {
		t.Fatalf("zweiter Write meldete %v", err)
	}
}

func TestUeberlaufBrichtDenKontextAb(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := New(4, cancel)

	if _, err := b.Write([]byte("mehr als vier")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	select {
	case <-ctx.Done():
	default:
		t.Fatal("Kontext hätte abgebrochen werden müssen")
	}
}

func TestGrenzeNullNimmtNichtsAuf(t *testing.T) {
	b := New(0, nil)

	if _, err := b.Write([]byte("x")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if b.Len() != 0 || !b.Overflow() {
		t.Fatalf("Len %d, Overflow %v", b.Len(), b.Overflow())
	}
}
