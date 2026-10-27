// Command evalctl manages the eval sets in this repo — the trigger queries and the output
// cases — and nothing else. It never launches a run, grades a transcript or touches results/.
package main

import (
	"os"

	"evalctl/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
