package shadow

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"grove/internal/tmux"
)

const Prefix = "gs"
const EnvVersion = "1"

const (
	openedAtKey      = "shadow_opened_at"
	lastToggledAtKey = "shadow_last_toggled_at"
)

type CleanupOptions struct {
	InactiveOlderThan time.Duration
	RemoveAll         bool
	DryRun            bool
}

type CleanupReason string

const (
	CleanupReasonOrphan   CleanupReason = "orphan"
	CleanupReasonInactive CleanupReason = "inactive"
	CleanupReasonAll      CleanupReason = "all"
)

type CleanupCandidate struct {
	SessionName   string
	Type          string
	ParentPane    string
	CreatedAt     time.Time
	LastToggledAt time.Time
	LastActiveAt  time.Time
	Reason        CleanupReason
}

type CleanupFailure struct {
	Candidate CleanupCandidate
	Err       error
}

type CleanupReport struct {
	Matched []CleanupCandidate
	Removed []CleanupCandidate
	Failed  []CleanupFailure
}

type shadowSessionState struct {
	name          string
	typ           string
	parentPane    string
	openedAt      time.Time
	lastToggledAt time.Time
	lastActiveAt  time.Time
	orphan        bool
}

var (
	listSessionSnapshotsByPrefix = tmux.ListSessionSnapshotsByPrefix
	getSessionVar                = tmux.GetSessionVar
	setSessionVar                = tmux.SetSessionVar
	sessionExists                = tmux.SessionExists
	newSessionWithCommand        = tmux.NewSessionWithCommand
	paneExists                   = tmux.PaneExists
	killSession                  = defaultKillSession
	now                          = time.Now
)

// Session describes a live Grove shadow session with cleanup-oriented metadata.
type Session struct {
	SessionName   string
	Type          string
	ParentPane    string
	OpenedAt      time.Time
	LastToggledAt time.Time
	LastActiveAt  time.Time
	Orphan        bool
}

func Name(paneID, typ string) string {
	id := strings.TrimPrefix(paneID, "%")
	return fmt.Sprintf("%s/%s/%s", Prefix, typ, id)
}

func IsSession(name string) bool {
	return strings.HasPrefix(name, Prefix+"/")
}

func ParseInactiveThreshold(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	if strings.HasSuffix(raw, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(raw, "d"))
		if err != nil || days <= 0 {
			return 0, fmt.Errorf("invalid --inactive value %q (examples: 1h, 90m, 1d)", raw)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid --inactive value %q (examples: 1h, 90m, 1d)", raw)
	}
	return d, nil
}

func ParentPane(currentSession, activePane string) (string, error) {
	if !IsSession(currentSession) {
		return activePane, nil
	}
	paneID, err := tmux.GetSessionVar(currentSession, "shadow_parent_pane")
	if err != nil {
		return "", fmt.Errorf("getting shadow parent pane: %w", err)
	}
	if paneID == "" {
		return "", fmt.Errorf("shadow session %s is missing shadow_parent_pane", currentSession)
	}
	return paneID, nil
}

func PopupClient(currentSession, fallback string) (string, error) {
	if !IsSession(currentSession) {
		return fallback, nil
	}
	clientName, err := tmux.GetSessionVar(currentSession, "shadow_client_name")
	if err != nil || clientName == "" {
		return fallback, nil
	}
	return clientName, nil
}

// Ensure creates or re-creates a shadow session for the given pane.
// If the session exists but its cwd doesn't match paneCwd, it is
// killed and recreated so the shadow always follows the pane's project.
func Ensure(sessionName, paneCwd, typ, paneID string) error {
	if sessionExists(sessionName) {
		storedCwd, _ := getSessionVar(sessionName, "shadow_cwd")
		envVersion, _ := getSessionVar(sessionName, "shadow_env_version")
		if storedCwd == paneCwd && envVersion == EnvVersion {
			return nil
		}
		killSession(sessionName)
	}

	env := []string{
		fmt.Sprintf("GROVE_AGENT_PANE=%s", paneID),
		"GROVE_SHADOW=1",
		fmt.Sprintf("GROVE_SHADOW_TYPE=%s", typ),
	}

	command := ""
	if typ == "vim" {
		command = "nvim"
	}

	if err := newSessionWithCommand(sessionName, paneCwd, env, command); err != nil {
		return fmt.Errorf("creating shadow session: %w", err)
	}
	openedAt := now().UTC().Format(time.RFC3339)
	if err := setSessionVar(sessionName, "shadow_cwd", paneCwd); err != nil {
		return fmt.Errorf("storing shadow cwd: %w", err)
	}
	if err := setSessionVar(sessionName, "shadow_parent_pane", paneID); err != nil {
		return fmt.Errorf("storing shadow parent pane: %w", err)
	}
	if err := setSessionVar(sessionName, "shadow_env_version", EnvVersion); err != nil {
		return fmt.Errorf("storing shadow env version: %w", err)
	}
	if err := setSessionVar(sessionName, openedAtKey, openedAt); err != nil {
		return fmt.Errorf("storing shadow opened timestamp: %w", err)
	}
	return nil
}

// MarkToggled records the latest explicit user interaction with a shadow session.
// It complements tmux activity so stale shadows can be sorted by Grove intent.
func MarkToggled(sessionName string) error {
	return setSessionVar(sessionName, lastToggledAtKey, now().UTC().Format(time.RFC3339))
}

// CleanupOrphans kills shadow sessions whose parent pane no longer exists.
func CleanupOrphans() error {
	_, err := Cleanup(CleanupOptions{})
	return err
}

// ListSessions returns all Grove shadow sessions with Grove metadata and tmux fallbacks.
// Older sessions without Grove timestamps use tmux created/activity times.
func ListSessions() ([]Session, error) {
	states, err := listShadowSessions()
	if err != nil {
		return nil, err
	}

	sessions := make([]Session, 0, len(states))
	for _, state := range states {
		sessions = append(sessions, Session{
			SessionName:   state.name,
			Type:          state.typ,
			ParentPane:    state.parentPane,
			OpenedAt:      state.openedAt,
			LastToggledAt: state.lastToggledAt,
			LastActiveAt:  state.lastActiveAt,
			Orphan:        state.orphan,
		})
	}
	return sessions, nil
}

func SelectCleanupCandidates(opts CleanupOptions) ([]CleanupCandidate, error) {
	sessions, err := listShadowSessions()
	if err != nil {
		return nil, err
	}

	current := now()
	candidates := make([]CleanupCandidate, 0, len(sessions))
	for _, session := range sessions {
		switch {
		case opts.RemoveAll:
			candidates = append(candidates, session.candidate(CleanupReasonAll))
		case session.orphan:
			candidates = append(candidates, session.candidate(CleanupReasonOrphan))
		case opts.InactiveOlderThan > 0 && !session.lastActiveAt.IsZero() && current.Sub(session.lastActiveAt) >= opts.InactiveOlderThan:
			candidates = append(candidates, session.candidate(CleanupReasonInactive))
		}
	}
	return candidates, nil
}

func Cleanup(opts CleanupOptions) (CleanupReport, error) {
	matched, err := SelectCleanupCandidates(opts)
	if err != nil {
		return CleanupReport{}, err
	}

	report := CleanupReport{Matched: matched}
	if opts.DryRun {
		return report, nil
	}

	for _, candidate := range matched {
		if err := killSession(candidate.SessionName); err != nil {
			report.Failed = append(report.Failed, CleanupFailure{Candidate: candidate, Err: err})
			continue
		}
		report.Removed = append(report.Removed, candidate)
	}

	if len(report.Failed) > 0 {
		return report, fmt.Errorf("failed to remove %d shadow sessions", len(report.Failed))
	}
	return report, nil
}

func listShadowSessions() ([]shadowSessionState, error) {
	snapshots, err := listSessionSnapshotsByPrefix(Prefix + "/")
	if err != nil {
		return nil, err
	}

	sessions := make([]shadowSessionState, 0, len(snapshots))
	for _, snapshot := range snapshots {
		session := shadowSessionState{
			name:          snapshot.Name,
			typ:           sessionType(snapshot.Name),
			openedAt:      sessionMetadataTime(snapshot.Name, openedAtKey, snapshot.Created),
			lastToggledAt: sessionMetadataTime(snapshot.Name, lastToggledAtKey, snapshot.Activity),
			lastActiveAt:  snapshot.Activity,
		}

		parentPane, err := getSessionVar(snapshot.Name, "shadow_parent_pane")
		if err != nil || parentPane == "" {
			session.orphan = true
		} else {
			session.parentPane = parentPane
			session.orphan = !paneExists(parentPane)
		}

		sessions = append(sessions, session)
	}
	return sessions, nil
}

func sessionType(name string) string {
	parts := strings.Split(name, "/")
	if len(parts) != 3 {
		return ""
	}
	return parts[1]
}

func sessionMetadataTime(sessionName, key string, fallback time.Time) time.Time {
	raw, err := getSessionVar(sessionName, key)
	if err != nil || strings.TrimSpace(raw) == "" {
		return fallback
	}
	ts, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return ts.UTC()
}

func (s shadowSessionState) candidate(reason CleanupReason) CleanupCandidate {
	return CleanupCandidate{
		SessionName:   s.name,
		Type:          s.typ,
		ParentPane:    s.parentPane,
		CreatedAt:     s.openedAt,
		LastToggledAt: s.lastToggledAt,
		LastActiveAt:  s.lastActiveAt,
		Reason:        reason,
	}
}

func defaultKillSession(name string) error {
	return tmux.KillSession(name)
}
