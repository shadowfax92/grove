package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	jumpCmd.Flags().BoolP("panes", "p", false, "Search panes instead of sessions")
	rootCmd.AddCommand(jumpCmd)
}

var jumpCmd = &cobra.Command{
	Use:         "jump",
	Aliases:     []string{"j"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Fuzzy-find and jump to any tmux session or pane",
	Long: `Search all tmux sessions or panes with fzf and jump to the selection.
Not limited to grove workspaces — searches everything in tmux.

  grove jump      — search sessions
  grove jump -p   — search panes

Bind in tmux.conf for quick access:
  bind-key -n M-f display-popup -E "grove jump"
  bind-key -n M-F display-popup -E "grove jump -p"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		panes, _ := cmd.Flags().GetBool("panes")
		if panes {
			return jumpPanes()
		}
		return jumpSessions()
	},
}

func jumpSessions() error {
	sessions, err := tmux.ListSessionInfo()
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		return fmt.Errorf("no tmux sessions")
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Activity > sessions[j].Activity
	})

	current, _ := tmux.CurrentSession()

	var lines []string
	for _, s := range sessions {
		marker := "  "
		if s.Name == current {
			marker = "● "
		}
		winLabel := "windows"
		if s.Windows == 1 {
			winLabel = "window"
		}
		display := fmt.Sprintf("%s\t%s%-30s %d %s", s.Name, marker, s.Name, s.Windows, winLabel)
		lines = append(lines, display)
	}

	target, err := runFzfJump("session > ", lines)
	if err != nil {
		return err
	}

	if tmux.IsInsideTmux() {
		return tmux.SwitchClient(target)
	}
	return tmux.Attach(target)
}

func jumpPanes() error {
	panes, err := tmux.ListPaneInfo()
	if err != nil {
		return err
	}
	if len(panes) == 0 {
		return fmt.Errorf("no tmux panes")
	}

	current, _ := tmux.CurrentTarget()
	home, _ := os.UserHomeDir()

	// Fields: 1=target(hidden) 2=session 3=window 4=command 5=path
	// fzf searches: 2,3,5 (session, window name, path — not command or target)
	var lines []string
	for _, p := range panes {
		path := p.Path
		if home != "" && strings.HasPrefix(path, home) {
			path = "~" + path[len(home):]
		}
		marker := "  "
		if p.Target == current {
			marker = "● "
		}
		lines = append(lines, fmt.Sprintf("%s\t%s%-24s\t%-14s\t%-12s\t%s",
			p.Target, marker, p.Session, p.WindowName, p.Command, path))
	}

	target, err := runFzfPanes(lines)
	if err != nil {
		return err
	}

	if tmux.IsInsideTmux() {
		return tmux.SwitchClient(target)
	}
	return tmux.Attach(target)
}

func runFzfJump(prompt string, lines []string) (string, error) {
	return runFzf(prompt, lines, nil)
}

func runFzfPanes(lines []string) (string, error) {
	return runFzf("pane > ", lines, []string{
		"--with-nth", "2..",
		"--nth", "2,3,5",
	})
}

func runFzf(prompt string, lines []string, extra []string) (string, error) {
	args := []string{
		"--prompt", prompt,
		"--height", "~80%",
		"--reverse",
		"--delimiter", "\t",
	}
	if len(extra) > 0 {
		args = append(args, extra...)
	} else {
		args = append(args, "--with-nth", "2")
	}

	fzfCmd := exec.Command("fzf", args...)
	fzfCmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return "", ErrCancelled
		}
		if len(out) == 0 {
			return "", ErrCancelled
		}
		return "", fmt.Errorf("fzf: %w", err)
	}

	line := strings.TrimSpace(string(out))
	if idx := strings.Index(line, "\t"); idx >= 0 {
		return line[:idx], nil
	}
	return line, nil
}
