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
	case "scan":
		return runScan(rest)
	case "tui":
		return runTUI(rest)
	case "list":
		return runList(rest)
	case "seed":
		return runSeed(rest)
	case "init":
		return runInit(rest)
	case "version", "-v", "--version":
		fmt.Println("swoomps dev (no version yet)")
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
	fmt.Println(`swoomps — Minecraft server scanner

Usage:
  swoomps <command> [flags]

Commands:
  probe               scan seeds from the configured directory or a file
  list                query and display found servers
  scan                long-running scan (em breve)
  tui                 live dashboard (em breve)
  init                initialize config and database
  seed                manage seeds (em breve)
  version             print version

Run 'swoomps help <command>' for more info on a command.`)
}

func helpCmd(cmd string) error {
	switch cmd {
	case "probe":
		fmt.Print(probeUsage())
	case "list":
		fmt.Print(listUsage())
	default:
		fmt.Fprintf(os.Stderr, "no help available for %q\n", cmd)
	}
	return nil
}
