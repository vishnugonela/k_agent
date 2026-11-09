package main

import (
	"os"

	"github.com/vishnugonela/k_agent/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr, nil))
}
