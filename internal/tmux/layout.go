package tmux

import (
	"fmt"
	"os/exec"
	"strings"
)

// CreateSessionWithLayout creates a tmux session and applies a layout via the layouts CLI.
// Falls back to a plain session if the layouts CLI is not installed.
func CreateSessionWithLayout(name, startDir, layoutName string) error {
	if layoutName == "" {
		return NewSession(name, startDir)
	}
	if _, err := exec.LookPath("layouts"); err != nil {
		fmt.Println("warning: layouts CLI not found, creating session without layout")
		fmt.Println("  install: go install github.com/shadowfax92/layouts@latest")
		return NewSession(name, startDir)
	}
	cmd := exec.Command("layouts", "new", name, layoutName, "-d", startDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("layouts new: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}
