package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func handleKiroHooks(args []string) {
	if len(args) == 0 {
		printKiroHooksUsage(os.Stderr)
		os.Exit(1)
	}

	switch args[0] {
	case "help", "--help", "-h":
		printKiroHooksUsage(os.Stdout)
	case "install":
		handleKiroHooksInstall()
	case "uninstall":
		handleKiroHooksUninstall()
	case "status":
		handleKiroHooksStatus()
	default:
		fmt.Fprintf(os.Stderr, "Unknown kiro-hooks subcommand: %s\n", args[0])
		printKiroHooksUsage(os.Stderr)
		os.Exit(1)
	}
}

func printKiroHooksUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: agent-deck kiro-hooks <command>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Manage Kiro CLI hook integration.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  install      Install agent-deck Kiro hooks")
	fmt.Fprintln(w, "  uninstall    Remove agent-deck Kiro hooks")
	fmt.Fprintln(w, "  status       Show Kiro hooks install status")
}

func handleKiroHooksInstall() {
	configDir := getKiroConfigDirForHooks()
	installed, err := session.InjectKiroHooks(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error installing Kiro hooks: %v\n", err)
		os.Exit(1)
	}
	if installed {
		fmt.Println("Kiro hooks installed successfully.")
		fmt.Printf("Config: %s\n", filepath.Join(configDir, "agents", session.AgentDeckKiroAgentName+".json"))
	} else {
		fmt.Println("Kiro hooks are already installed.")
	}
}

func handleKiroHooksUninstall() {
	configDir := getKiroConfigDirForHooks()
	removed, err := session.RemoveKiroHooks(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error removing Kiro hooks: %v\n", err)
		os.Exit(1)
	}
	if removed {
		fmt.Println("Kiro hooks removed successfully.")
	} else {
		fmt.Println("No agent-deck Kiro hooks found to remove.")
	}
}

func handleKiroHooksStatus() {
	configDir := getKiroConfigDirForHooks()
	installed := session.CheckKiroHooksInstalled(configDir)
	configPath := filepath.Join(configDir, "agents", session.AgentDeckKiroAgentName+".json")

	if installed {
		fmt.Println("Status: INSTALLED")
		fmt.Printf("Config: %s\n", configPath)
	} else {
		fmt.Println("Status: NOT INSTALLED")
		fmt.Println("Run 'agent-deck kiro-hooks install' to install.")
	}
}

func getKiroConfigDirForHooks() string {
	return session.GetKiroConfigDir()
}
