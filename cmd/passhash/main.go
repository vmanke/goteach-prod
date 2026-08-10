// passhash erzeugt Passwort-Hashes für AUTH_USERS des goteach-Servers.
//
// Das Passwort kommt von stdin (die Standardbibliothek bietet kein
// Terminal-Lesen ohne Echo; wer das Passwort nicht in der Shell-History
// haben will, nutzt eine Datei oder einen Here-String):
//
//	echo -n 'geheim' | go run ./cmd/passhash
//	AUTH_USERS="alice:$(echo -n 'geheim' | go run ./cmd/passhash)"
//
// Prompt und Hinweise gehen auf stderr, der Hash allein auf stdout —
// damit lässt sich die Ausgabe gefahrlos in Variablen fangen.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/vmanke/goteach-prod/internal/auth"
)

func main() {
	iter := flag.Int("iter", auth.DefaultIterations,
		"PBKDF2-Iterationen (mehr = langsamer = sicherer)")
	flag.Parse()

	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprint(os.Stderr, "Passwort (Eingabe sichtbar!): ")
	}

	// Nur die erste Zeile lesen; ein abschließender Zeilenumbruch gehört
	// nicht zum Passwort (echo ohne -n, interaktive Eingabe).
	reader := bufio.NewReader(io.LimitReader(os.Stdin, 4096))
	line, err := reader.ReadString('\n')

	if err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, "passhash: stdin: %v\n", err)
		os.Exit(1)
	}

	password := strings.TrimRight(line, "\r\n")

	hash, err := auth.HashPassword(password, *iter)

	if err != nil {
		fmt.Fprintf(os.Stderr, "passhash: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(hash)
}
