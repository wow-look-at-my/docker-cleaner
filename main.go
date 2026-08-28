// Command docker-cleaner removes docker resources nothing will use again.
package main

import (
	"os"

	"github.com/wow-look-at-my/docker-cleaner/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
