package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func handleOpenCodeHooks(args []string) {
	if len(args) == 0 {
		printOpenCodeHooksUsage(os.Stderr)
		os.Exit(1)
	}

	switch args[0] {
	case "help", "--help", "-h":
		printOpenCodeHooksUsage(os.Stdout)
	case "install":
		handleOpenCodeHooksInstall()
	case "uninstall":
		handleOpenCodeHooksUninstall()
	case "status":
		handleOpenCodeHooksStatus()
	default:
		fmt.Fprintf(os.Stderr, "Unknown opencode-hooks subcommand: %s\n", args[0])
		printOpenCodeHooksUsage(os.Stderr)
		os.Exit(1)
	}
}

func printOpenCodeHooksUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: agent-deck opencode-hooks <command>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Manage OpenCode plugin hook integration.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  install      Remove legacy agent-deck OpenCode prompt context hook")
	fmt.Fprintln(w, "  uninstall    Remove agent-deck OpenCode hooks")
	fmt.Fprintln(w, "  status       Show OpenCode hooks install status")
}

func handleOpenCodeHooksInstall() {
	configDir := getOpenCodeConfigDirForHooks()
	removed, err := session.InjectOpenCodeHooks(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error updating OpenCode hooks: %v\n", err)
		os.Exit(1)
	}
	if removed {
		fmt.Println("Legacy OpenCode hub context hook removed.")
		fmt.Printf("Config: %s\n", filepath.Join(configDir, "plugins", "agent-deck-hub-context.js"))
	} else {
		fmt.Println("No OpenCode prompt context hook installed.")
	}
}

func handleOpenCodeHooksUninstall() {
	configDir := getOpenCodeConfigDirForHooks()
	removed, err := session.RemoveOpenCodeHooks(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error removing OpenCode hooks: %v\n", err)
		os.Exit(1)
	}
	if removed {
		fmt.Println("OpenCode hooks removed successfully.")
	} else {
		fmt.Println("No agent-deck OpenCode hooks found to remove.")
	}
}

func handleOpenCodeHooksStatus() {
	configDir := getOpenCodeConfigDirForHooks()
	installed := session.CheckOpenCodeHooksInstalled(configDir)
	configPath := filepath.Join(configDir, "plugins", "agent-deck-hub-context.js")

	if installed {
		fmt.Println("Status: LEGACY_CONTEXT_HOOK")
		fmt.Printf("Config: %s\n", configPath)
	} else {
		fmt.Println("Status: NOT INSTALLED")
		fmt.Println("No Agent Deck OpenCode prompt context hook is installed.")
	}
}

func getOpenCodeConfigDirForHooks() string {
	return session.GetOpenCodeConfigDir()
}
