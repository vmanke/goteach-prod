// Alternativer Entrypoint für Builder, die cmd/server/main.go erwarten;
// identisch zum main.go in der Repo-Wurzel.
package main

import (
	"log"

	"github.com/vmanke/goteach-prod/internal/server"
)

func main() {
	log.Fatalf("goteach-server: %v", server.Run())
}
