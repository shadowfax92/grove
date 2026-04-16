package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"

	"grove/internal/config"
	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(startCmd)
}

var startCmd = &cobra.Command{
	Use:         "start",
	Annotations: map[string]string{"group": "Setup:"},
	Short:       "Start grove — create sessions, bind keys, and attach",
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

		// Reconcile: ensure tmux sessions exist for all workspaces
		for _, ws := range st.Workspaces {
			if tmux.SessionExists(ws.SessionName) {
				continue
			}
			dir := workspaceDir(&ws)
			var layoutName string
			if ws.Type == "worktree" {
				if repo := cfg.FindRepo(ws.Repo); repo != nil {
					layoutName = repo.Layout
				}
			}
			if err := tmux.CreateSessionWithLayout(ws.SessionName, dir, layoutName); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to create session %s: %v\n", ws.SessionName, err)
			}
		}

		if err := mgr.Save(st); err != nil {
			return fmt.Errorf("saving state: %w", err)
		}

		selfPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("finding executable path: %w", err)
		}
		selfCmd := strconv.Quote(selfPath)

		// Bind sidebar keybinding
		sidebarCmd := fmt.Sprintf("%s sidebar", selfCmd)
		var popupArgs []string
		switch cfg.Sidebar.Position {
		case "left":
			popupArgs = []string{
				"-n", cfg.Prefix,
				"display-popup", "-x", "0", "-y", "0",
				"-w", cfg.Sidebar.Width, "-h", cfg.Sidebar.Height, "-E", sidebarCmd,
			}
		case "right":
			popupArgs = []string{
				"-n", cfg.Prefix,
				"display-popup", "-y", "0",
				"-w", cfg.Sidebar.Width, "-h", cfg.Sidebar.Height, "-E", sidebarCmd,
			}
		default: // center
			popupArgs = []string{
				"-n", cfg.Prefix,
				"display-popup",
				"-w", cfg.Sidebar.Width, "-h", cfg.Sidebar.Height, "-E", sidebarCmd,
			}
		}
		if err := tmux.BindKeyRaw(popupArgs...); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to bind key: %v\n", err)
		}

		shadowVimCmd := fmt.Sprintf(`%s shadow toggle vim "#{client_name}" "#{session_name}" "#{pane_id}" >/dev/null 2>&1 || true`, selfCmd)
		if err := tmux.BindKeyRaw("-n", cfg.Shadow.Keys.Vim, "run-shell", "-b", shadowVimCmd); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to bind shadow vim key: %v\n", err)
		}
		shadowShellCmd := fmt.Sprintf(`%s shadow toggle shell "#{client_name}" "#{session_name}" "#{pane_id}" >/dev/null 2>&1 || true`, selfCmd)
		if err := tmux.BindKeyRaw("-n", cfg.Shadow.Keys.Shell, "run-shell", "-b", shadowShellCmd); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to bind shadow shell key: %v\n", err)
		}

		// Clean up orphaned shadow sessions when panes die
		cleanupHook := fmt.Sprintf("run-shell '%s shadow cleanup >/dev/null 2>&1 || true'", selfCmd)
		if err := tmux.SetHook("after-kill-pane", cleanupHook); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to set cleanup hook: %v\n", err)
		}

		// Attach to last active or first workspace
		target := st.LastActive
		if target != "" {
			found := false
			for _, ws := range st.Workspaces {
				if ws.SessionName == target {
					found = true
					break
				}
			}
			if !found {
				target = ""
			}
		}
		if target == "" && len(st.Workspaces) > 0 {
			target = st.Workspaces[0].SessionName
		}

		if target == "" {
			fmt.Println("No workspaces configured. Add repos to config and re-run grove start.")
			return nil
		}

		mgr.TouchWorkspace(st, target)
		st.LastActive = target
		if err := mgr.Save(st); err != nil {
			return fmt.Errorf("saving state: %w", err)
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
		return execSyscall(tmuxPath, []string{"tmux", "attach-session", "-t", "=" + target}, os.Environ())
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
