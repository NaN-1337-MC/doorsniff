package cli

import (
	"fmt"

	"github.com/renanbray/doorsniff/version"
)

func runVersion(args []string) error {
	commit := version.Commit
	if commit != "" {
		commit = "g" + commit
	}

	fmt.Println(banner() + fmt.Sprintf(`
Version:  %s%s

Discord:  https://discord.gg/DpZ49YX47U
GitHub:   https://github.com/NaN-1337-MC/doorsniff

doorsniff is a fast, stealthy Minecraft server scanner and
query tool. Use 'doorsniff <command> --help' to get started.
`, version.Version, versionInfo(commit)))
	return nil
}

func versionInfo(commit string) string {
	if commit == "" && version.Date == "" {
		return ""
	}
	s := ""
	if commit != "" {
		s += " (" + commit + ")"
	}
	if version.Date != "" {
		s += ", " + version.Date
	}
	return s
}
