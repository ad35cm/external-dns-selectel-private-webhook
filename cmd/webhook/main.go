package main

import (
	"os"

	"github.com/ad35cm/external-dns-selectel-private-webhook/cmd/webhook/cmd"
)

// version and commit are set by goreleaser via -ldflags.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	if err := cmd.Execute(version + " (" + commit + ")"); err != nil {
		os.Exit(1)
	}
}
