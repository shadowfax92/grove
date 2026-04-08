package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type SessionInfo struct {
	Name     string
	Windows  int
	Attached bool
	Activity int64
}

type PaneInfo struct {
	Target     string
	Session    string
	WindowName string
	Command    string
	Path       string
}

func run(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %s (%w)", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func IsInsideTmux() bool {
	return os.Getenv("TMUX") != ""
}

func SessionExists(name string) bool {
	_, err := run("has-session", "-t", "="+name)
	return err == nil
}

func NewSession(name, startDir string) error {
	_, err := run("new-session", "-d", "-s", name, "-c", startDir)
	return err
}

func NewSessionWithCommand(name, startDir string, env []string, command string) error {
	args := []string{"new-session", "-d", "-s", name, "-c", startDir}
	for _, entry := range env {
		args = append(args, "-e", entry)
	}
	if command != "" {
		args = append(args, command)
	}
	_, err := run(args...)
	return err
}

func KillSession(name string) error {
	_, err := run("kill-session", "-t", "="+name)
	return err
}

func SwitchClient(target string) error {
	_, err := run("switch-client", "-t", "="+target)
	return err
}

func Attach(target string) error {
	cmd := exec.Command("tmux", "attach-session", "-t", "="+target)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func BindKey(key, command string) error {
	_, err := run("bind-key", "-n", key, "display-popup", "-x", "0", "-y", "0", "-w", command, "-E", "grove sidebar")
	return err
}

func BindKeyRaw(args ...string) error {
	fullArgs := append([]string{"bind-key"}, args...)
	_, err := run(fullArgs...)
	return err
}

func ListSessions() ([]string, error) {
	out, err := run("list-sessions", "-F", "#{session_name}")
	if err != nil {
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "no sessions") {
			return nil, nil
		}
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

func RenameSession(oldName, newName string) error {
	_, err := run("rename-session", "-t", "="+oldName, newName)
	return err
}

func CurrentSession() (string, error) {
	return run("display-message", "-p", "#{session_name}")
}

func CurrentTarget() (string, error) {
	return run("display-message", "-p", "#{session_name}:#{window_index}.#{pane_index}")
}

func ListSessionInfo() ([]SessionInfo, error) {
	out, err := run("list-sessions", "-F", "#{session_name}\t#{session_windows}\t#{session_attached}\t#{session_activity}")
	if err != nil {
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "no sessions") {
			return nil, nil
		}
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	var sessions []SessionInfo
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 4 {
			continue
		}
		wins, _ := strconv.Atoi(parts[1])
		activity, _ := strconv.ParseInt(parts[3], 10, 64)
		sessions = append(sessions, SessionInfo{
			Name:     parts[0],
			Windows:  wins,
			Attached: parts[2] != "0",
			Activity: activity,
		})
	}
	return sessions, nil
}

func PaneID() (string, error) {
	return run("display-message", "-p", "#{pane_id}")
}

func SetPaneVar(target, key, value string) error {
	_, err := run("set-option", "-p", "-t", target, "@"+key, value)
	return err
}

func SetCurrentPaneLabel(value string) error {
	target, err := PaneID()
	if err != nil {
		return err
	}
	return SetPaneVar(target, "pane_label", value)
}

func UnsetCurrentPaneLabel() error {
	target, err := PaneID()
	if err != nil {
		return err
	}
	_, err = run("set-option", "-p", "-t", target, "-u", "@pane_label")
	return err
}

func RenameCurrentWindow(name string) error {
	_, err := run("rename-window", name)
	return err
}

func DisableCurrentWindowAutoRename() error {
	_, err := run("set-option", "-w", "automatic-rename", "off")
	return err
}

func PaneCwd(target string) (string, error) {
	return run("display-message", "-t", target, "-p", "#{pane_current_path}")
}

func SetSessionVar(session, key, value string) error {
	_, err := run("set-option", "-t", session, "@"+key, value)
	return err
}

func GetSessionVar(session, key string) (string, error) {
	return run("show-options", "-t", session, "-v", "@"+key)
}

func DisplayPopup(client, width, height, command string) error {
	_, err := run("display-popup", "-c", client, "-w", width, "-h", height, "-E", command)
	return err
}

func SetHook(hookName, command string) error {
	_, err := run("set-hook", "-g", hookName, command)
	return err
}

func ClosePopup(client string) error {
	_, err := run("display-popup", "-C", "-c", client)
	return err
}

func ListSessionsByPrefix(prefix string) ([]string, error) {
	out, err := run("list-sessions", "-F", "#{session_name}")
	if err != nil {
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "no sessions") {
			return nil, nil
		}
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	var result []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			result = append(result, line)
		}
	}
	return result, nil
}

func PaneExists(paneID string) bool {
	_, err := run("display-message", "-t", paneID, "-p", "")
	return err == nil
}

func ListPaneInfo() ([]PaneInfo, error) {
	out, err := run("list-panes", "-a", "-F", "#{session_name}:#{window_index}.#{pane_index}\t#{session_name}\t#{window_name}\t#{pane_current_command}\t#{pane_current_path}")
	if err != nil {
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "no sessions") {
			return nil, nil
		}
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	var panes []PaneInfo
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) < 5 {
			continue
		}
		panes = append(panes, PaneInfo{
			Target:     parts[0],
			Session:    parts[1],
			WindowName: parts[2],
			Command:    parts[3],
			Path:       parts[4],
		})
	}
	return panes, nil
}
