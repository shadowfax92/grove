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
	jumpCmd.Flags().BoolP("panes", "p", false, "Search panes")
	jumpCmd.Flags().BoolP("windows", "w", false, "Search windows")
	jumpCmd.Flags().BoolP("all", "a", false, "Include ghost (shadow) sessions")
	rootCmd.AddCommand(jumpCmd)
}

var jumpCmd = &cobra.Command{
	Use:         "jump",
	Aliases:     []string{"j"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Fuzzy-find and jump to any tmux session, window, or pane",
	Long: `Search all tmux sessions, windows, or panes with fzf and jump to the selection.
Not limited to grove workspaces — searches everything in tmux.

  grove jump      — search sessions
  grove jump -w   — search windows
  grove jump -p   — search panes
  grove jump -pw  — combined windows+panes, each row tagged (w) or (p)
  grove jump -a   — include ghost (shadow) sessions in any mode

Bind in tmux.conf for quick access:
  bind-key -n M-s display-popup -E "grove jump"
  bind-key -n M-w display-popup -E "grove jump -w"
  bind-key -n M-u display-popup -E "grove jump -p"
  bind-key -n M-f display-popup -E "grove jump -pw"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		panes, _ := cmd.Flags().GetBool("panes")
		windows, _ := cmd.Flags().GetBool("windows")
		all, _ := cmd.Flags().GetBool("all")
		if panes && windows {
			return jumpPanesAndWindows(all)
		}
		if panes {
			return jumpPanes(all)
		}
		if windows {
			return jumpWindows(all)
		}
		return jumpSessions(all)
	},
}

func jumpSessions(all bool) error {
	sessions, err := tmux.ListSessionInfo()
	if err != nil {
		return err
	}
	if !all {
		sessions = visibleJumpSessions(sessions)
	}
	if len(sessions) == 0 {
		return fmt.Errorf("no tmux sessions")
	}

	mgr, _ := state.NewManager()
	var inv *workspaces.Inventory
	if mgr != nil {
		if st, err := mgr.Load(); err == nil {
			inv, _ = workspaces.Build(st, nil)
		}
	}

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

	sort.Slice(sessions, func(i, j int) bool {
		return sessionSort[sessions[i].Name] > sessionSort[sessions[j].Name]
	})

	currentSession, _ := tmux.CurrentSession()

	var lines []string
	for _, s := range sessions {
		marker := "  "
		if s.Name == currentSession {
			marker = "● "
		}
		attached := ""
		if s.Attached {
			attached = "(attached)"
		}
		lines = append(lines, fmt.Sprintf("%s\t%s%-32s\t%-3d windows\t%s",
			s.Name, marker, s.Name, s.Windows, attached))
	}

	target, err := runFzf("session > ", lines, []string{
		"--with-nth", "2..",
		"--nth", "2",
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

	touchGroveSession(mgr, target)
	return nil
}

func jumpWindows(all bool) error {
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

	// Fields: 1=target(hidden) 2=session 3=window 4=path
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
		name := w.Name
		if name == "" {
			name = w.Label
		}
		window := fmt.Sprintf("%d:%s", w.Index, name)
		lines = append(lines, fmt.Sprintf("%s\t%s%-24s\t%-20s\t%s",
			w.Target, marker, w.Session, window, path))
	}

	target, err := runFzf("window > ", lines, []string{
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

	// Fields: 1=target(hidden) 2=session 3=window 4=pane 5=command 6=path
	// fzf searches: 2,3,4,6 (session, window, pane, path)
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
		window := fmt.Sprintf("%d:%s", p.WindowIndex, p.WindowName)
		pane := fmt.Sprintf("%d", p.PaneIndex)
		if p.Label != "" {
			pane = fmt.Sprintf("%d:%s", p.PaneIndex, p.Label)
		}
		lines = append(lines, fmt.Sprintf("%s\t%s%-24s\t%-20s\t%-16s\t%-12s\t%s",
			p.Target, marker, p.Session, window, pane, p.Command, path))
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

const (
	colorWindow = "\x1b[36m" // cyan
	colorPane   = "\x1b[32m" // green
	colorReset  = "\x1b[0m"
)

func jumpPanesAndWindows(all bool) error {
	windows, err := tmux.ListWindowInfo()
	if err != nil {
		return err
	}
	panes, err := tmux.ListPaneInfo()
	if err != nil {
		return err
	}
	if !all {
		visibleW := make([]tmux.WindowInfo, 0, len(windows))
		for _, w := range windows {
			if !shadow.IsSession(w.Session) {
				visibleW = append(visibleW, w)
			}
		}
		windows = visibleW
		panes = visibleJumpPanes(panes)
	}
	if len(windows) == 0 && len(panes) == 0 {
		return fmt.Errorf("no tmux windows or panes")
	}

	mgr, _ := state.NewManager()
	var inv *workspaces.Inventory
	if mgr != nil {
		if st, err := mgr.Load(); err == nil {
			inv, _ = workspaces.Build(st, nil)
		}
	}
	sessions, _ := tmux.ListSessionInfo()
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

	currentTarget, _ := tmux.CurrentTarget()
	currentWindow := currentTarget
	if idx := strings.LastIndex(currentTarget, "."); idx >= 0 {
		currentWindow = currentTarget[:idx]
	}
	home, _ := os.UserHomeDir()

	panesByWindow := make(map[string][]tmux.PaneInfo)
	for _, p := range panes {
		wt := fmt.Sprintf("%s:%d", p.Session, p.WindowIndex)
		panesByWindow[wt] = append(panesByWindow[wt], p)
	}
	for key, ps := range panesByWindow {
		sort.Slice(ps, func(i, j int) bool { return ps[i].PaneIndex < ps[j].PaneIndex })
		panesByWindow[key] = ps
	}

	sort.Slice(windows, func(i, j int) bool {
		si, sj := sessionSort[windows[i].Session], sessionSort[windows[j].Session]
		if si != sj {
			return si > sj
		}
		return windows[i].Index < windows[j].Index
	})

	var lines []string
	for _, w := range windows {
		lines = append(lines, tagWindowLine(w, currentWindow))
		for _, p := range panesByWindow[w.Target] {
			lines = append(lines, tagPaneLine(p, currentTarget, home))
		}
	}

	target, err := runFzf("window/pane > ", lines, []string{
		"--ansi",
		"--with-nth", "2..",
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

	sessionName := target
	if idx := strings.Index(target, ":"); idx >= 0 {
		sessionName = target[:idx]
	}
	touchGroveSession(mgr, sessionName)
	return nil
}

func tagWindowLine(w tmux.WindowInfo, currentWindow string) string {
	marker := "  "
	if w.Target == currentWindow {
		marker = "● "
	}
	name := w.Name
	if name == "" {
		name = w.Label
	}
	window := fmt.Sprintf("%d:%s", w.Index, name)
	body := fmt.Sprintf("(w) %s%-24s\t%-28s\t\t", marker, w.Session, window)
	return fmt.Sprintf("%s\t%s%s%s", w.Target, colorWindow, body, colorReset)
}

func tagPaneLine(p tmux.PaneInfo, currentTarget, home string) string {
	marker := "  "
	if p.Target == currentTarget {
		marker = "● "
	}
	window := fmt.Sprintf("%d:%s", p.WindowIndex, p.WindowName)
	pane := fmt.Sprintf("%d", p.PaneIndex)
	if p.Label != "" {
		pane = fmt.Sprintf("%d:%s", p.PaneIndex, p.Label)
	}
	path := p.Path
	if home != "" && strings.HasPrefix(path, home) {
		path = "~" + path[len(home):]
	}
	hint := paneHint(p.Command, path)
	body := fmt.Sprintf("(p) %s%-24s\t%-28s\t%-18s\t%s", marker, p.Session, window, pane, hint)
	return fmt.Sprintf("%s\t%s%s%s", p.Target, colorPane, body, colorReset)
}

func paneHint(cmd, path string) string {
	switch cmd {
	case "", "fish", "zsh", "bash", "sh", "dash":
	default:
		return cmd
	}
	if path != "" && path != "~" {
		return path
	}
	return ""
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
		"--nth", "2,3,4,6",
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
