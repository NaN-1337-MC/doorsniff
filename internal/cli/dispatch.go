package cli

import (
	"fmt"
	"os"
)

func Run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "probe":
		return runProbe(rest)
	case "list":
		return runList(rest)
	case "update":
		return runUpdate(rest)
	case "init":
		return runInit(rest)
	case "version":
		return runVersion(rest)
	case "-v", "--version":
		return runVersion(rest)
	case "help", "-h", "--help":
		if len(rest) > 0 {
			return helpCmd(rest[0])
		}
		usage()
		return nil
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %q\n\n", cmd)
		usage()
		os.Exit(2)
		return nil
	}
}

func banner() string {
	tagline := randomTagline()
	pad := 28 - len(tagline)/2
	if pad < 0 {
		pad = 0
	}
	spaces := ""
	for i := 0; i < pad; i++ {
		spaces += " "
	}
	// Gáttaþefur
	return `
                        ▓████████▓
                       ▓░        █▓
                      █  █████  █
                     █  █     █ █
                      ▓█ ▄   ▄ █
                 ▄▄▄▄  █ ▄   ▄ █  ▄▄▄▄
            ░░▒▓██  ██████   ██████  ██▓▒░░
           ____  ▀██▀    █   █    ▀██▀ ___ ___
          |    \ ___ ___ _███___ ___|_|  _|  _|
          |  |  | . | . |  _|_ -|   | |  _|  _|
          |____/|___|___|_| |___|_|_|_|_| |_|
` + spaces + tagline + "\n"
}

func usage() {
	fmt.Print(banner() + `
Usage:
  doorsniff <command> [flags]

Commands:
  probe               scan seeds from the configured directory or a file
  list                query and display found servers
  update              re-probe known servers to refresh their data
  init                initialize config and database
  version             print version

Run 'doorsniff help <command>' for more info on a command.`)
}

func helpCmd(cmd string) error {
	switch cmd {
	case "probe":
		fmt.Print(probeUsage())
	case "list":
		fmt.Print(listUsage())
	case "update":
		fmt.Print(updateUsage())
	case "init":
		fmt.Print(initUsage())
	default:
		fmt.Fprintf(os.Stderr, "no help available for %q\n", cmd)
	}
	return nil
}

func initUsage() string {
	return `doorsniff init — initialize config file and database

Usage:
  doorsniff init [flags]

Creates:
  - Config file: ~/.config/doorsniff/config.yaml
  - Database: ~/.local/share/doorsniff/doorsniff.db

The config file contains all configurable options with their default
values. Edit it to customize doorsniff behavior.

Flags:
  (none — init has no flags)

Examples:
  doorsniff init
`
}
