package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"grove/internal/config"
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
	quickCmd.Flags().Bool("inside", false, "Run inside the quick popup")
	_ = quickCmd.Flags().MarkHidden("inside")
	rootCmd.AddCommand(jumpCmd)
	rootCmd.AddCommand(quickCmd)
}

var jumpCmd = &cobra.Command{
	Use:         "jump",
	Aliases:     []string{"j"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Fuzzy-find and jump to any tmux session, window, or pane",
	Long: `Search all tmux sessions, windows, or panes with fzf and jump to the selection.
Not limited to grove workspaces — searches everything in tmux.

  grove jump      — search sessions  (session name)
  grove jump -w   — search windows   (session + window name)
  grove jump -p   — search panes     (window + pane label + command + path)
  grove jump -a   — include ghost (shadow) sessions in any mode

Each picker shows only what it searches across; window/pane pickers add a
live tmux capture-pane preview on the right for visual context.

Bind in tmux.conf for quick access:
  bind-key -n M-s display-popup -E "grove jump"
  bind-key -n M-w display-popup -E "grove jump -w"
  bind-key -n M-p display-popup -E "grove jump -p"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		panes, _ := cmd.Flags().GetBool("panes")
		windows, _ := cmd.Flags().GetBool("windows")
		all, _ := cmd.Flags().GetBool("all")
		if panes && windows {
			return fmt.Errorf("-p and -w cannot be combined; pick one")
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

var quickCmd = &cobra.Command{
	Use:         "quick [client_name]",
	Aliases:     []string{"q"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Fuzzy-find a pane and open it in a popup",
	Long: `Open a popup pane picker (includes shadow sessions).
The selected pane attaches inside the popup so closing it returns to the original pane.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		inside, _ := cmd.Flags().GetBool("inside")
		if inside {
			return runQuickInside()
		}
		return openQuickPopup(args)
	},
}

func openQuickPopup(args []string) error {
	clientName := ""
	if len(args) > 0 {
		clientName = args[0]
	} else {
		var err error
		clientName, err = tmux.CurrentClient()
		if err != nil {
			return err
		}
	}

	cfg, err := config.LoadFast()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable path: %w", err)
	}

	command := fmt.Sprintf("exec %s quick --inside", strconv.Quote(selfPath))
	size := cfg.Shadow.Popup.PopupFor("sh")
	return tmux.DisplayPopup(clientName, size.Width, size.Height, command)
}

func runQuickInside() error {
	target, mgr, err := selectPane(true)
	if err != nil {
		return err
	}
	touchGroveSession(mgr, jumpTargetSessionName(target))
	return tmux.Attach(target)
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

	// Fields: 1=name(hidden) 2=marker+name. Everything visible is searchable.
	var lines []string
	for _, s := range sessions {
		marker := "  "
		if s.Name == currentSession {
			marker = "● "
		}
		lines = append(lines, fmt.Sprintf("%s\t%s%s", s.Name, marker, s.Name))
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

	sort.Slice(windows, func(i, j int) bool {
		si, sj := sessionSort[windows[i].Session], sessionSort[windows[j].Session]
		if si != sj {
			return si > sj
		}
		return windows[i].Index < windows[j].Index
	})

	currentTarget, _ := tmux.CurrentTarget()
	currentWindow := currentTarget
	if idx := strings.LastIndex(currentTarget, "."); idx >= 0 {
		currentWindow = currentTarget[:idx]
	}

	// Fields: 1=target(hidden) 2=session 3=window
	// Everything visible is searchable.
	var lines []string
	for _, w := range windows {
		marker := "  "
		if w.Target == currentWindow {
			marker = "● "
		}
		name := w.Name
		if name == "" {
			name = w.Label
		}
		window := fmt.Sprintf("%d:%s", w.Index, name)
		lines = append(lines, fmt.Sprintf("%s\t%s%-24s\t%s",
			w.Target, marker, w.Session, window))
	}

	target, err := runFzf("window > ", lines, []string{
		"--with-nth", "2..",
		"--nth", "2..",
		"--tiebreak", "begin,index",
		"--preview", "tmux capture-pane -ep -t {1}",
		"--preview-window", "right:50%",
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

	touchGroveSession(mgr, jumpTargetSessionName(target))
	return nil
}

func jumpPanes(all bool) error {
	target, mgr, err := selectPane(all)
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

	touchGroveSession(mgr, jumpTargetSessionName(target))
	return nil
}

// selectPane runs the fzf pane picker and returns the selected target plus a state manager
// for the caller to use when touching the grove session. Shared by jumpPanes and runQuickInside.
func selectPane(all bool) (string, *state.StateManager, error) {
	panes, err := tmux.ListPaneInfo()
	if err != nil {
		return "", nil, err
	}
	if !all {
		panes = visibleJumpPanes(panes)
	}
	if len(panes) == 0 {
		return "", nil, fmt.Errorf("no tmux panes")
	}

	current, _ := tmux.CurrentTarget()
	home, _ := os.UserHomeDir()

	// Fields: 1=target(hidden) 2=window 3=pane 4=command 5=path
	// Everything visible is searchable. Session is not shown (you have few sessions and the
	// preview makes the context obvious).
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
		lines = append(lines, fmt.Sprintf("%s\t%s%-20s\t%-16s\t%-12s\t%s",
			p.Target, marker, window, pane, p.Command, path))
	}

	target, err := runFzf("pane > ", lines, []string{
		"--with-nth", "2..",
		"--nth", "2..",
		"--preview", "tmux capture-pane -ep -t {1}",
		"--preview-window", "right:50%",
	})
	if err != nil {
		return "", nil, err
	}

	mgr, _ := state.NewManager()
	return target, mgr, nil
}

func jumpTargetSessionName(target string) string {
	if idx := strings.Index(target, ":"); idx >= 0 {
		return target[:idx]
	}
	return target
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
