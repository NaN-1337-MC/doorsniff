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
	case "version", "-v", "--version":
		fmt.Println("doorsniff dev (no version yet)")
		return nil
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

func usage() {
// Gáttaþefur
	fmt.Println(`
                        ▓████████▓
                       ▓░        █▓
                      █  █████  █
                     █  █     █ █
                      ▓█ ▄   ▄ █
                 ▄▄▄▄  █       █  ▄▄▄▄
            ░░▒▓██  ██████   ██████  ██▓▒░░
                 ▀██▀    █   █    ▀██▀
                          ███
         Doorsniif, a Minecraft server scanner.

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
	default:
		fmt.Fprintf(os.Stderr, "no help available for %q\n", cmd)
	}
	return nil
}
