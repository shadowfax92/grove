package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"grove/internal/config"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(layoutCmd)
}

var layoutCmd = &cobra.Command{
	Use:         "layout [name]",
	Aliases:     []string{"lo"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Apply a layout to the current session",
	Long: `Apply a layout to the current tmux session.

  grove layout          — pick layout via fzf
  grove layout <name>   — apply named layout`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !tmux.IsInsideTmux() {
			return fmt.Errorf("must be inside a tmux session")
		}

		cfg, err := config.Load()
		if err != nil {
			return err
		}

		var layoutName string
		if len(args) >= 1 {
			layoutName = args[0]
		} else {
			picked, err := pickLayoutFzf(cfg)
			if err != nil {
				return err
			}
			layoutName = picked
		}

		layout := cfg.FindLayout(layoutName)
		if layout == nil {
			return fmt.Errorf("layout %q not found in config", layoutName)
		}

		sessionName, err := tmux.CurrentSession()
		if err != nil {
			return fmt.Errorf("getting current session: %w", err)
		}

		// Get the current pane's working directory
		dir, err := currentPaneDir()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}

		if err := tmux.ApplyLayoutToCurrentSession(sessionName, dir, layout); err != nil {
			return fmt.Errorf("applying layout: %w", err)
		}

		fmt.Printf("Applied layout %q\n", layoutName)
		return nil
	},
}

func pickLayoutFzf(cfg *config.Config) (string, error) {
	names := cfg.LayoutNames()
	if len(names) == 0 {
		return "", fmt.Errorf("no layouts defined in config")
	}

	fzfCmd := exec.Command("fzf",
		"--prompt", "layout > ",
		"--header", "Pick a layout to apply",
		"--height", "~40%",
		"--reverse",
	)
	fzfCmd.Stdin = strings.NewReader(strings.Join(names, "\n"))
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil {
		return "", ErrCancelled
	}

	result := strings.TrimSpace(string(out))
	if result == "" {
		return "", ErrCancelled
	}
	return result, nil
}

func currentPaneDir() (string, error) {
	cmd := exec.Command("tmux", "display-message", "-p", "#{pane_current_path}")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
