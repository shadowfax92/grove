package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"grove/internal/shadow"
	"grove/internal/state"
	"grove/internal/tmux"
	"grove/internal/workspaces"

	"github.com/spf13/cobra"
)

func init() {
	jumpCmd.Flags().BoolP("panes", "p", false, "Search panes instead of sessions")
	jumpCmd.Flags().BoolP("all", "a", false, "Include ghost (shadow) sessions")
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
  grove jump -a   — search all sessions including ghosts (gs/)
  grove jump -p   — search panes
  grove jump -pa  — search all panes including ghosts

Bind in tmux.conf for quick access:
  bind-key -n M-s display-popup -E "grove jump"
  bind-key -n M-i display-popup -E "grove jump -a"
  bind-key -n M-u display-popup -E "grove jump -p"
  bind-key -n M-f display-popup -E "grove jump -pa"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		panes, _ := cmd.Flags().GetBool("panes")
		all, _ := cmd.Flags().GetBool("all")
		if panes {
			return jumpPanes(all)
		}
		return jumpSessions(all)
	},
}

func jumpSessions(all bool) error {
	sessions, err := tmux.ListSessionInfo()
	if err != nil {
		return err
	}

	windows, err := tmux.ListWindowInfo()
	if err != nil {
		return err
	}
	if !all {
		var visible []tmux.WindowInfo
		for _, w := range windows {
			if !shadow.IsSession(w.Session) {
				visible = append(visible, w)
			}
		}
		windows = visible
	}
	if len(windows) == 0 {
		return fmt.Errorf("no tmux windows")
	}

	// Load grove state for LastUsedAt sorting (best-effort, no lock needed for read)
	mgr, _ := state.NewManager()
	var inv *workspaces.Inventory
	if mgr != nil {
		if st, err := mgr.Load(); err == nil {
			inv, _ = workspaces.Build(st, nil)
		}
	}

	// Build session sort keys: grove LastUsedAt for grove sessions, tmux activity for others
	sessionSort := make(map[string]int64, len(sessions))
	for _, s := range sessions {
		sortKey := s.Activity
		if inv != nil {
			if managed, ok := inv.FindManagedBySession(s.Name); ok && managed.Workspace.LastUsedAt != "" {
				if t, err := time.Parse(time.RFC3339, managed.Workspace.LastUsedAt); err == nil {
					sortKey = t.Unix()
				}
			}
		}
		sessionSort[s.Name] = sortKey
	}

	// Sort: session by last-used desc, then window index asc
	sort.Slice(windows, func(i, j int) bool {
		si, sj := sessionSort[windows[i].Session], sessionSort[windows[j].Session]
		if si != sj {
			return si > sj
		}
		return windows[i].Index < windows[j].Index
	})

	// Determine current window target (session:window from session:window.pane)
	currentTarget, _ := tmux.CurrentTarget()
	currentWindow := currentTarget
	if idx := strings.LastIndex(currentTarget, "."); idx >= 0 {
		currentWindow = currentTarget[:idx]
	}

	home, _ := os.UserHomeDir()

	// Fields: 1=target(hidden) 2=session 3=idx 4=label 5=path
	var lines []string
	for _, w := range windows {
		path := w.Path
		if home != "" && strings.HasPrefix(path, home) {
			path = "~" + path[len(home):]
		}
		marker := "  "
		if w.Target == currentWindow {
			marker = "● "
		}
		label := w.Label
		if label == "" {
			label = w.Name
		}
		lines = append(lines, fmt.Sprintf("%s\t%s%-24s\t%-3d\t%-14s\t%s",
			w.Target, marker, w.Session, w.Index, label, path))
	}

	target, err := runFzf("session > ", lines, []string{
		"--with-nth", "2..",
		"--nth", "2..",
		"--tiebreak", "begin,index",
	})
	if err != nil {
		return err
	}

	if tmux.IsInsideTmux() {
		if err := tmux.SwitchClient(target); err != nil {
			return err
		}
	} else {
		if err := tmux.Attach(target); err != nil {
			return err
		}
	}

	// Extract session name for grove touch
	sessionName := target
	if idx := strings.Index(target, ":"); idx >= 0 {
		sessionName = target[:idx]
	}
	touchGroveSession(mgr, sessionName)
	return nil
}

func jumpPanes(all bool) error {
	panes, err := tmux.ListPaneInfo()
	if err != nil {
		return err
	}
	if !all {
		panes = visibleJumpPanes(panes)
	}
	if len(panes) == 0 {
		return fmt.Errorf("no tmux panes")
	}

	current, _ := tmux.CurrentTarget()
	home, _ := os.UserHomeDir()

	// Fields: 1=target(hidden) 2=session 3=label 4=command 5=path
	// fzf searches: 2,3,5 (session, label, path)
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
		label := p.Label
		if label == "" {
			label = p.WindowName
		}
		lines = append(lines, fmt.Sprintf("%s\t%s%-24s\t%-14s\t%-12s\t%s",
			p.Target, marker, p.Session, label, p.Command, path))
	}

	target, err := runFzfPanes(lines)
	if err != nil {
		return err
	}

	if tmux.IsInsideTmux() {
		if err := tmux.SwitchClient(target); err != nil {
			return err
		}
	} else {
		if err := tmux.Attach(target); err != nil {
			return err
		}
	}

	// Extract session name from pane target (session:window.pane)
	sessionName := target
	if idx := strings.LastIndex(target, ":"); idx >= 0 {
		sessionName = target[:idx]
	}
	mgr, _ := state.NewManager()
	touchGroveSession(mgr, sessionName)
	return nil
}

func touchGroveSession(mgr *state.StateManager, sessionName string) {
	if mgr == nil {
		return
	}
	if err := mgr.Lock(); err != nil {
		return
	}
	defer mgr.Unlock()
	st, err := mgr.Load()
	if err != nil {
		return
	}
	inv, err := workspaces.Build(st, nil)
	if err != nil {
		return
	}
	if _, ok := inv.FindManagedBySession(sessionName); ok {
		mgr.ClearNotifications(st, sessionName)
		mgr.TouchWorkspace(st, sessionName)
		st.LastActive = sessionName
		_ = mgr.Save(st)
	}
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
		"--height", "100%",
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

func visibleJumpSessions(sessions []tmux.SessionInfo) []tmux.SessionInfo {
	visible := make([]tmux.SessionInfo, 0, len(sessions))
	for _, session := range sessions {
		if shadow.IsSession(session.Name) {
			continue
		}
		visible = append(visible, session)
	}
	return visible
}

func visibleJumpPanes(panes []tmux.PaneInfo) []tmux.PaneInfo {
	visible := make([]tmux.PaneInfo, 0, len(panes))
	for _, pane := range panes {
		if shadow.IsSession(pane.Session) {
			continue
		}
		visible = append(visible, pane)
	}
	return visible
}
