// Entrypoint in der Repo-Wurzel: startet den HTTP-Dienst aus
// internal/server. Das Dockerfile baut genau dieses Paket (`go build .`),
// deshalb liegt es hier und nicht nur unter cmd/server. Die CLI bleibt
// unter cmd/goteach.
package main

import (
	"log"

	"github.com/vmanke/goteach-prod/internal/server"
)

func main() {
	log.Fatalf("goteach-server: %v", server.Run())
}
