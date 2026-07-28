package main

import (
	"fmt"
	"os"

	"github.com/renanbray/doorsniff/internal/cli"
	"github.com/renanbray/doorsniff/version"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(version.Version)
		return
	}

	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
