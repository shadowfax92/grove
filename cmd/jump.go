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
	sessions = visibleJumpSessions(sessions)
	if len(sessions) == 0 {
		return fmt.Errorf("no tmux sessions")
	}

	// Load grove state for LastUsedAt sorting (best-effort, no lock needed for read)
	mgr, _ := state.NewManager()
	var st *state.State
	var inv *workspaces.Inventory
	if mgr != nil {
		st, _ = mgr.Load()
		if st != nil {
			inv, _ = workspaces.Build(st, nil)
		}
	}

	// Build sort keys: grove LastUsedAt for grove sessions, tmux activity for others
	type entry struct {
		info    tmux.SessionInfo
		sortKey int64
	}
	entries := make([]entry, len(sessions))
	for i, s := range sessions {
		sortKey := s.Activity
		if inv != nil {
			if managed, ok := inv.FindManagedBySession(s.Name); ok && managed.Workspace.LastUsedAt != "" {
				if t, err := time.Parse(time.RFC3339, managed.Workspace.LastUsedAt); err == nil {
					sortKey = t.Unix()
				}
			}
		}
		entries[i] = entry{info: s, sortKey: sortKey}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].sortKey > entries[j].sortKey
	})

	current, _ := tmux.CurrentSession()
	infoBySession := make(map[string]tmux.SessionInfo, len(entries))
	sessionNames := make([]string, 0, len(entries))
	for _, e := range entries {
		infoBySession[e.info.Name] = e.info
		sessionNames = append(sessionNames, e.info.Name)
	}

	var lines []string
	for _, row := range buildSessionTreeRows(sessionNames) {
		target := row.defaultTarget
		if target == "" {
			continue
		}

		s, ok := infoBySession[row.sessionName]
		marker := "  "
		if ok && s.Name == current {
			marker = "● "
		}

		detail := fmt.Sprintf("%d sessions", row.leafCount)
		if ok {
			winLabel := "windows"
			if s.Windows == 1 {
				winLabel = "window"
			}
			detail = fmt.Sprintf("%d %s", s.Windows, winLabel)
		}

		display := fmt.Sprintf("%s\t%s%-30s %s", target, marker, row.label, detail)
		lines = append(lines, display)
	}

	target, err := runFzfJump("session > ", lines)
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

	// Update grove state: touch workspace, set last active
	touchGroveSession(mgr, target)
	return nil
}

func jumpPanes() error {
	panes, err := tmux.ListPaneInfo()
	if err != nil {
		return err
	}
	panes = visibleJumpPanes(panes)
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
