package cmd

import (
	"fmt"
	"os"

	"grove/internal/git"
	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	renameCmd.Flags().Bool("clear", false, "Unset the pane label")
	renameCmd.Flags().BoolP("window", "w", false, "Also rename the current window (disables automatic-rename)")
	rootCmd.AddCommand(renameCmd)
}

var renameCmd = &cobra.Command{
	Use:         "rename [label]",
	Aliases:     []string{"rn"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Label the current tmux pane",
	Long: `Set @pane_label for the current tmux pane (rendered by pane-border-format).

  grove rename           — auto-detect label from current pane cwd
  grove rename <label>   — set an explicit label
  grove rename --clear   — unset the pane label
  grove rename -w        — also rename the window and disable automatic-rename

Auto-detect order:
  1. git repo on a feature branch → branch name
  2. git repo on main/master/trunk → repo folder name
  3. detached HEAD → <repo>@<short-sha>
  4. $HOME → "home"
  5. any other dir → folder name (basename of cwd)
  6. last resort → grove session name (trimmed of "g/")`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !tmux.IsInsideTmux() {
			return fmt.Errorf("grove rename must run inside tmux")
		}

		clear, _ := cmd.Flags().GetBool("clear")
		alsoWindow, _ := cmd.Flags().GetBool("window")

		if clear {
			if len(args) > 0 {
				return fmt.Errorf("--clear takes no label argument")
			}
			if err := tmux.UnsetCurrentPaneLabel(); err != nil {
				return fmt.Errorf("clearing pane label: %w", err)
			}
			fmt.Println("pane label cleared")
			return nil
		}

		var label string
		if len(args) == 1 {
			label = args[0]
		} else {
			resolved, err := autoPaneLabel()
			if err != nil {
				return err
			}
			if resolved == "" {
				return fmt.Errorf("could not infer a pane label")
			}
			label = resolved
		}

		if err := tmux.SetCurrentPaneLabel(label); err != nil {
			return fmt.Errorf("setting pane label: %w", err)
		}

		if alsoWindow {
			if err := tmux.RenameCurrentWindow(label); err != nil {
				return fmt.Errorf("renaming window: %w", err)
			}
			if err := tmux.DisableCurrentWindowAutoRename(); err != nil {
				return fmt.Errorf("disabling automatic-rename: %w", err)
			}
		}

		fmt.Printf("pane label: %s\n", label)
		return nil
	},
}

func autoPaneLabel() (string, error) {
	paneID, err := tmux.PaneID()
	if err != nil {
		return "", fmt.Errorf("reading pane id: %w", err)
	}
	cwd, err := tmux.PaneCwd(paneID)
	if err != nil {
		return "", fmt.Errorf("reading pane cwd: %w", err)
	}

	in := paneLabelInputs{cwd: cwd}
	in.home, _ = os.UserHomeDir()

	if mgr, err := state.NewManager(); err == nil {
		if st, err := mgr.Load(); err == nil {
			if ws, _ := findWorkspaceByCwd(st, cwd); ws != nil {
				in.workspace = ws
			}
		}
	}

	in.branch = git.CurrentBranch(cwd)
	in.repoRoot = git.RepoRoot(cwd)
	if in.branch == "" && in.repoRoot != "" {
		in.headSha = git.HeadShortSha(cwd)
	}

	return resolvePaneLabel(in), nil
}
