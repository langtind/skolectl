package main

import (
	"fmt"
	"io/fs"
	"os"

	"github.com/langtind/ctlkit"

	"github.com/langtind/skolectl/internal/cli"
)

// Version information - injected at build time for GitHub releases
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	skill, err := fs.Sub(skillFS, "skills/"+cli.Name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	os.Exit(cli.New(ctlkit.Build{Version: version, Commit: commit, Date: date}, skill).Main())
}
