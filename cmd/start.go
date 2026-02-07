package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"grove/internal/config"
	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(startCmd)
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start grove — create sessions, bind keys, and attach",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		mgr, err := state.NewManager()
		if err != nil {
			return fmt.Errorf("creating state manager: %w", err)
		}

		if err := mgr.Lock(); err != nil {
			return err
		}
		defer mgr.Unlock()

		st, err := mgr.Load()
		if err != nil {
			return fmt.Errorf("loading state: %w", err)
		}

		// Process auto_start entries
		for _, as := range cfg.AutoStart {
			if as.Repo != "" {
				repo := cfg.FindRepo(as.Repo)
				if repo == nil {
					fmt.Fprintf(os.Stderr, "warning: auto_start repo %q not found in config\n", as.Repo)
					continue
				}
				for _, wt := range as.Worktrees {
					sessionName := fmt.Sprintf("grove/%s/%s", repo.Name, wt)
					if mgr.FindBySession(st, sessionName) != nil {
						continue
					}

					var dir string
					if wt == repo.DefaultBranch {
						dir = repo.Path
					} else {
						dir = filepath.Join(repo.Path, ".grove", "worktrees", wt)
					}

					ws := state.Workspace{
						Name:         fmt.Sprintf("%s/%s", repo.Name, wt),
						Type:         "worktree",
						Repo:         repo.Name,
						RepoPath:     repo.Path,
						WorktreePath: dir,
						Branch:       wt,
						SessionName:  sessionName,
					}
					mgr.AddWorkspace(st, ws)
				}
			} else if as.Workspace != "" {
				sessionName := fmt.Sprintf("grove/%s", as.Workspace)
				if mgr.FindBySession(st, sessionName) != nil {
					continue
				}

				dir := as.Path
				if dir == "" {
					home, _ := os.UserHomeDir()
					dir = home
				}

				ws := state.Workspace{
					Name:        as.Workspace,
					Type:        "plain",
					Path:        dir,
					SessionName: sessionName,
				}
				mgr.AddWorkspace(st, ws)
			}
		}

		// Reconcile: ensure tmux sessions exist for all workspaces
		for _, ws := range st.Workspaces {
			if tmux.SessionExists(ws.SessionName) {
				continue
			}
			dir := ws.WorktreePath
			if ws.Type == "plain" {
				dir = ws.Path
			}
			if dir == "" {
				home, _ := os.UserHomeDir()
				dir = home
			}
			if err := tmux.NewSession(ws.SessionName, dir); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to create session %s: %v\n", ws.SessionName, err)
			}
		}

		if err := mgr.Save(st); err != nil {
			return fmt.Errorf("saving state: %w", err)
		}

		// Bind sidebar keybinding
		sidebarCmd := fmt.Sprintf("grove sidebar")
		popupArgs := []string{
			"-n", cfg.Prefix,
			"display-popup", "-x", "0", "-y", "0",
			"-w", cfg.Sidebar.Width, "-h", "100%", "-E", sidebarCmd,
		}
		if cfg.Sidebar.Position == "right" {
			popupArgs = []string{
				"-n", cfg.Prefix,
				"display-popup", "-y", "0",
				"-w", cfg.Sidebar.Width, "-h", "100%", "-E", sidebarCmd,
			}
		}
		if err := tmux.BindKeyRaw(popupArgs...); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to bind key: %v\n", err)
		}

		// Attach to last active or first workspace
		target := st.LastActive
		if target == "" && len(st.Workspaces) > 0 {
			target = st.Workspaces[0].SessionName
		}

		if target == "" {
			fmt.Println("No workspaces configured. Add repos to config and re-run grove start.")
			return nil
		}

		// Must unlock before attaching since attach blocks
		mgr.Unlock()

		if tmux.IsInsideTmux() {
			return tmux.SwitchClient(target)
		}

		// exec into tmux attach to replace this process
		tmuxPath, err := exec.LookPath("tmux")
		if err != nil {
			return fmt.Errorf("tmux not found: %w", err)
		}
		return execSyscall(tmuxPath, []string{"tmux", "attach-session", "-t", target}, os.Environ())
	},
}

// Thin wrapper for testing seam
var execSyscall = defaultExecSyscall

func defaultExecSyscall(argv0 string, argv []string, envv []string) error {
	cmd := exec.Command(argv0, argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
