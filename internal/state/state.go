package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type State struct {
	Version    int         `json:"version"`
	Workspaces []Workspace `json:"workspaces"`
	LastActive string      `json:"last_active"`
	Collapsed  []string    `json:"collapsed,omitempty"`
}

type Notification struct {
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
}

type Workspace struct {
	Name          string         `json:"name"`
	Type          string         `json:"type"` // "worktree", "dir", or "plain"
	Repo          string         `json:"repo,omitempty"`
	RepoPath      string         `json:"repo_path,omitempty"`
	WorktreePath  string         `json:"worktree_path,omitempty"`
	Branch        string         `json:"branch,omitempty"`
	Path          string         `json:"path,omitempty"`
	SessionName   string         `json:"session_name"`
	CreatedAt     string         `json:"created_at"`
	LastUsedAt    string         `json:"last_used_at,omitempty"`
	Notifications []Notification `json:"notifications,omitempty"`
}

type StateManager struct {
	path     string
	lockPath string
	lockFile *os.File
}

func NewManager() (*StateManager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	dir := filepath.Join(home, ".local", "state", "grove")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	return &StateManager{
		path:     filepath.Join(dir, "state.json"),
		lockPath: filepath.Join(dir, "state.lock"),
	}, nil
}

func (m *StateManager) Lock() error {
	f, err := os.OpenFile(m.lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("opening lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return fmt.Errorf("acquiring lock: %w", err)
	}
	m.lockFile = f
	return nil
}

func (m *StateManager) Unlock() {
	if m.lockFile != nil {
		syscall.Flock(int(m.lockFile.Fd()), syscall.LOCK_UN)
		m.lockFile.Close()
		m.lockFile = nil
	}
}

func (m *StateManager) Load() (*State, error) {
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{Version: 1}, nil
		}
		return nil, fmt.Errorf("reading state: %w", err)
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing state: %w", err)
	}
	return &s, nil
}

func (m *StateManager) Save(s *State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	tmpPath := m.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("writing temp state: %w", err)
	}

	if err := os.Rename(tmpPath, m.path); err != nil {
		return fmt.Errorf("renaming state: %w", err)
	}
	return nil
}

func (m *StateManager) AddWorkspace(s *State, w Workspace) {
	now := time.Now().UTC().Format(time.RFC3339)
	if w.CreatedAt == "" {
		w.CreatedAt = now
	}
	if w.LastUsedAt == "" {
		w.LastUsedAt = now
	}
	s.Workspaces = append(s.Workspaces, w)
}

func (m *StateManager) RemoveWorkspace(s *State, sessionName string) {
	filtered := s.Workspaces[:0]
	for _, w := range s.Workspaces {
		if w.SessionName != sessionName {
			filtered = append(filtered, w)
		}
	}
	s.Workspaces = filtered
}

func (m *StateManager) FindWorkspace(s *State, name string) *Workspace {
	sessionName := "g/" + name
	for i := range s.Workspaces {
		if s.Workspaces[i].SessionName == sessionName || s.Workspaces[i].Name == name {
			return &s.Workspaces[i]
		}
	}
	return nil
}

func (m *StateManager) FindBySession(s *State, sessionName string) *Workspace {
	for i := range s.Workspaces {
		if s.Workspaces[i].SessionName == sessionName {
			return &s.Workspaces[i]
		}
	}
	return nil
}

func (m *StateManager) AppendNotification(s *State, sessionName, message string) {
	for i := range s.Workspaces {
		if s.Workspaces[i].SessionName == sessionName {
			s.Workspaces[i].Notifications = append(s.Workspaces[i].Notifications, Notification{
				Message:   message,
				CreatedAt: time.Now().UTC().Format(time.RFC3339),
			})
			return
		}
	}
}

func (m *StateManager) ClearNotifications(s *State, sessionName string) {
	for i := range s.Workspaces {
		if s.Workspaces[i].SessionName == sessionName {
			s.Workspaces[i].Notifications = nil
			return
		}
	}
}

func (m *StateManager) TouchWorkspace(s *State, sessionName string) {
	for i := range s.Workspaces {
		if s.Workspaces[i].SessionName == sessionName {
			s.Workspaces[i].LastUsedAt = time.Now().UTC().Format(time.RFC3339)
			return
		}
	}
}

func RelativeTime(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
