package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"grove/internal/shadow"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(moveCmd)
}

var moveCmd = &cobra.Command{
	Use:         "move <target-session>",
	Aliases:     []string{"mv"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Move the current window to another session",
	Long: `Move the current tmux window to a different session.
Creates the target session if it doesn't exist.

  grove move         — pick target session via fzf
  grove move admin   — move current window to "admin" session
  grove mv g/ops     — same, with alias`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !tmux.IsInsideTmux() {
			return fmt.Errorf("grove move must run inside tmux")
		}

		var target string
		if len(args) == 1 {
			target = args[0]
		} else {
			picked, err := pickMoveTarget()
			if err != nil {
				return err
			}
			target = picked
		}
		session, err := tmux.CurrentSession()
		if err != nil {
			return fmt.Errorf("reading current session: %w", err)
		}
		if session == target {
			return fmt.Errorf("current window is already in %q", target)
		}

		created := false
		if !tmux.SessionExists(target) {
			home, _ := os.UserHomeDir()
			if home == "" {
				home = "/"
			}
			if err := tmux.NewSession(target, home); err != nil {
				return fmt.Errorf("creating session %q: %w", target, err)
			}
			created = true
		}

		if err := tmux.MoveCurrentWindow(target); err != nil {
			return fmt.Errorf("moving window: %w", err)
		}

		// If we just created the session, kill its placeholder window
		if created {
			_ = tmux.KillWindow("=" + target + ":1")
		}

		fmt.Printf("moved window to %s\n", target)
		return nil
	},
}

func pickMoveTarget() (string, error) {
	sessions, err := tmux.ListSessions()
	if err != nil {
		return "", err
	}

	current, _ := tmux.CurrentSession()

	var lines []string
	for _, s := range sessions {
		if shadow.IsSession(s) || s == current {
			continue
		}
		lines = append(lines, s)
	}

	if len(lines) == 0 {
		return "", fmt.Errorf("no other sessions to move to — pass a name to create one")
	}

	fzfCmd := exec.Command("fzf", "--prompt", "move to > ",
		"--height", "100%", "--reverse",
		"--print-query")
	fzfCmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return "", ErrCancelled
		}
		// --print-query exits 1 when query doesn't match but user pressed enter
		// In that case, output has the query on line 1
		if len(out) > 0 {
			query := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
			if query != "" {
				return query, nil
			}
		}
		return "", ErrCancelled
	}

	// --print-query: line 1 = query, line 2 = selected match
	outputLines := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)
	if len(outputLines) >= 2 && outputLines[1] != "" {
		return outputLines[1], nil
	}
	if outputLines[0] != "" {
		return outputLines[0], nil
	}
	return "", ErrCancelled
}
