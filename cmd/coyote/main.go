// Comando coyote: CLI de Coyote.
package main

import (
	"os"

	"github.com/Emmanuel93/coyote/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
