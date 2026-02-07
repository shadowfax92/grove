package tui

import (
	"fmt"
	"strings"

	"grove/internal/config"
	"grove/internal/git"
	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type mode int

const (
	modeBrowse mode = iota
	modeCreate
	modeDelete
	modeFilter
	modeRename
)

type Model struct {
	cfg            *config.Config
	stateMgr       *state.StateManager
	st             *state.State
	nodes          []TreeNode
	cursor         int
	expanded       map[string]bool
	currentSession string
	mode           mode
	filterInput    textinput.Model
	filterText     string
	createForm     CreateForm
	deleteTarget   *TreeNode
	renameTarget   *state.Workspace
	renameInput    textinput.Model
	width          int
	height         int
	styles         Styles
	err            error
}

func RunSidebar(cfg *config.Config, mgr *state.StateManager, st *state.State, currentSession string) error {
	m := initialModel(cfg, mgr, st, currentSession)
	p := tea.NewProgram(m, tea.WithoutCatchPanics())
	_, err := p.Run()
	return err
}

func initialModel(cfg *config.Config, mgr *state.StateManager, st *state.State, currentSession string) Model {
	fi := textinput.New()
	fi.Placeholder = "filter..."
	fi.CharLimit = 64

	m := Model{
		cfg:            cfg,
		stateMgr:       mgr,
		st:             st,
		currentSession: currentSession,
		expanded:       make(map[string]bool),
		styles:         DefaultStyles(),
		filterInput:    fi,
	}
	m.rebuildTree()
	return m
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) reloadState() tea.Msg {
	st, err := m.stateMgr.Load()
	if err != nil {
		return errMsg{err}
	}
	cur, _ := tmux.CurrentSession()
	return stateLoadedMsg{st: st, currentSession: cur}
}

type stateLoadedMsg struct {
	st             *state.State
	currentSession string
}

type errMsg struct{ err error }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case stateLoadedMsg:
		m.st = msg.st
		m.currentSession = msg.currentSession
		m.rebuildTree()
		m.ensureCursorVisible()
		return m, nil

	case errMsg:
		m.err = msg.err
		return m, nil

	case workspaceCreatedMsg:
		m.stateMgr.AddWorkspace(m.st, msg.workspace)
		_ = m.stateMgr.Save(m.st)
		m.rebuildTree()
		m.mode = modeBrowse
		return m, nil

	case createCancelledMsg:
		m.mode = modeBrowse
		return m, nil

	case createErrorMsg:
		m.createForm.err = msg.err
		return m, nil
	}

	switch m.mode {
	case modeBrowse:
		return m.updateBrowse(msg)
	case modeCreate:
		return m.updateCreate(msg)
	case modeDelete:
		return m.updateDelete(msg)
	case modeFilter:
		return m.updateFilter(msg)
	case modeRename:
		return m.updateRename(msg)
	}

	return m, nil
}

func (m Model) updateBrowse(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+s":
			return m, tea.Quit

		case "j", "down":
			m.cursor = nextVisibleCursor(m.nodes, m.expanded, m.filterText, m.cursor, 1)
			return m, nil

		case "k", "up":
			m.cursor = nextVisibleCursor(m.nodes, m.expanded, m.filterText, m.cursor, -1)
			return m, nil

		case "enter":
			return m.selectWorkspace()

		case "o":
			return m.toggleExpand()

		case "c":
			return m.startCreate()

		case "d":
			return m.startDelete()

		case "R":
			return m.startRename()

		case "/":
			m.mode = modeFilter
			m.filterInput.Focus()
			return m, textinput.Blink

		case "r":
			return m, m.reloadState
		}
	}
	return m, nil
}

func (m Model) updateCreate(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.createForm, cmd = m.createForm.Update(msg)
	return m, cmd
}

func (m Model) updateDelete(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y":
			return m.confirmDelete()
		case "n", "N", "esc":
			m.mode = modeBrowse
			m.deleteTarget = nil
			return m, nil
		}
	}
	return m, nil
}

func (m Model) updateFilter(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter", "esc":
			m.filterText = m.filterInput.Value()
			m.mode = modeBrowse
			m.filterInput.Blur()
			if msg.String() == "esc" {
				m.filterText = ""
				m.filterInput.SetValue("")
			}
			m.ensureCursorVisible()
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	m.filterText = m.filterInput.Value()
	m.ensureCursorVisible()
	return m, cmd
}

func (m Model) cursorNotification() string {
	if m.cursor >= len(m.nodes) {
		return ""
	}
	node := m.nodes[m.cursor]
	if node.Kind != NodeWorkspace || node.Workspace == nil {
		return ""
	}
	return node.Workspace.Notification
}

func (m Model) selectWorkspace() (tea.Model, tea.Cmd) {
	if m.cursor >= len(m.nodes) {
		return m, nil
	}
	node := m.nodes[m.cursor]
	if node.Kind == NodeRepo {
		return m.toggleExpand()
	}
	if node.Workspace == nil {
		return m, nil
	}

	// Clear notification on switch
	m.stateMgr.ClearNotification(m.st, node.Workspace.SessionName)

	// Update last_active
	m.st.LastActive = node.Workspace.SessionName
	_ = m.stateMgr.Save(m.st)

	// Recreate session if it doesn't exist
	if !tmux.SessionExists(node.Workspace.SessionName) {
		dir := node.Workspace.WorktreePath
		if node.Workspace.Type == "plain" {
			dir = node.Workspace.Path
		}
		_ = tmux.NewSession(node.Workspace.SessionName, dir)
	}

	return m, tea.Sequence(
		func() tea.Msg {
			_ = tmux.SwitchClient(node.Workspace.SessionName)
			return nil
		},
		tea.Quit,
	)
}

func (m Model) toggleExpand() (tea.Model, tea.Cmd) {
	if m.cursor >= len(m.nodes) {
		return m, nil
	}
	node := m.nodes[m.cursor]
	repoName := node.RepoName
	if repoName == "" {
		return m, nil
	}
	m.expanded[repoName] = !m.expanded[repoName]
	return m, nil
}

func (m Model) startCreate() (tea.Model, tea.Cmd) {
	createMode := CreatePlain
	repoName := ""

	if m.cursor < len(m.nodes) {
		node := m.nodes[m.cursor]
		if node.RepoName != "" {
			createMode = CreateWorktree
			repoName = node.RepoName
		}
	}

	m.mode = modeCreate
	m.createForm = NewCreateForm(createMode, repoName, m.cfg, m.stateMgr, m.st)
	return m, textinput.Blink
}

func (m Model) startDelete() (tea.Model, tea.Cmd) {
	if m.cursor >= len(m.nodes) {
		return m, nil
	}
	node := m.nodes[m.cursor]
	if node.Kind != NodeWorkspace || node.Workspace == nil {
		return m, nil
	}
	m.mode = modeDelete
	m.deleteTarget = &node
	return m, nil
}

func (m Model) confirmDelete() (tea.Model, tea.Cmd) {
	if m.deleteTarget == nil || m.deleteTarget.Workspace == nil {
		m.mode = modeBrowse
		return m, nil
	}

	ws := m.deleteTarget.Workspace

	if tmux.SessionExists(ws.SessionName) {
		_ = tmux.KillSession(ws.SessionName)
	}

	if ws.Type == "worktree" && ws.WorktreePath != ws.RepoPath {
		_ = git.RemoveWorktree(ws.RepoPath, ws.WorktreePath)
	}

	m.stateMgr.RemoveWorkspace(m.st, ws.SessionName)
	_ = m.stateMgr.Save(m.st)

	m.rebuildTree()
	m.mode = modeBrowse
	m.deleteTarget = nil
	m.ensureCursorVisible()

	return m, nil
}

func (m Model) startRename() (tea.Model, tea.Cmd) {
	if m.cursor >= len(m.nodes) {
		return m, nil
	}
	node := m.nodes[m.cursor]
	if node.Kind != NodeWorkspace || node.Workspace == nil {
		return m, nil
	}

	ri := textinput.New()
	ri.Focus()
	ri.CharLimit = 64
	ri.Width = 30
	ri.SetValue(node.DisplayName)

	m.mode = modeRename
	m.renameTarget = node.Workspace
	m.renameInput = ri
	return m, textinput.Blink
}

func (m Model) updateRename(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			return m.confirmRename()
		case "esc":
			m.mode = modeBrowse
			m.renameTarget = nil
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.renameInput, cmd = m.renameInput.Update(msg)
	return m, cmd
}

func (m Model) confirmRename() (tea.Model, tea.Cmd) {
	if m.renameTarget == nil {
		m.mode = modeBrowse
		return m, nil
	}

	newName := strings.TrimSpace(m.renameInput.Value())
	currentName := m.renameTarget.Branch
	if currentName == "" {
		currentName = m.renameTarget.Name
	}
	if newName == "" || newName == currentName {
		m.mode = modeBrowse
		m.renameTarget = nil
		return m, nil
	}

	ws := m.renameTarget
	oldSessionName := ws.SessionName

	// Build new session name
	var newSessionName string
	if ws.Type == "worktree" {
		newSessionName = fmt.Sprintf("grove/%s/%s", ws.Repo, newName)
	} else {
		newSessionName = fmt.Sprintf("grove/%s", newName)
	}

	// Check for duplicates
	if m.stateMgr.FindBySession(m.st, newSessionName) != nil {
		m.err = fmt.Errorf("workspace %q already exists", newName)
		m.mode = modeBrowse
		m.renameTarget = nil
		return m, nil
	}

	// Rename tmux session
	if tmux.SessionExists(oldSessionName) {
		if err := tmux.RenameSession(oldSessionName, newSessionName); err != nil {
			m.err = fmt.Errorf("rename failed: %w", err)
			m.mode = modeBrowse
			m.renameTarget = nil
			return m, nil
		}
	}

	// Update state
	for i := range m.st.Workspaces {
		if m.st.Workspaces[i].SessionName == oldSessionName {
			m.st.Workspaces[i].SessionName = newSessionName
			if ws.Type == "worktree" {
				m.st.Workspaces[i].Name = fmt.Sprintf("%s/%s", ws.Repo, newName)
				m.st.Workspaces[i].Branch = newName
			} else {
				m.st.Workspaces[i].Name = newName
			}
			break
		}
	}

	if m.st.LastActive == oldSessionName {
		m.st.LastActive = newSessionName
	}

	_ = m.stateMgr.Save(m.st)
	m.rebuildTree()
	m.mode = modeBrowse
	m.renameTarget = nil
	m.ensureCursorVisible()

	return m, nil
}

func (m *Model) rebuildTree() {
	m.nodes = buildTree(m.st, m.cfg, m.currentSession)
	// Expand all repos by default
	for _, node := range m.nodes {
		if node.Kind == NodeRepo {
			if _, ok := m.expanded[node.RepoName]; !ok {
				m.expanded[node.RepoName] = true
			}
		}
	}
}

func (m *Model) ensureCursorVisible() {
	visible := visibleNodes(m.nodes, m.expanded, m.filterText)
	if len(visible) == 0 {
		return
	}
	for _, vn := range visible {
		if vn.originalIdx == m.cursor {
			return
		}
	}
	m.cursor = visible[0].originalIdx
}

func (m Model) View() string {
	if m.st == nil {
		return "Loading..."
	}

	var b strings.Builder

	b.WriteString(m.styles.Header.Render(" grove"))
	b.WriteString("\n")

	if m.mode == modeCreate {
		b.WriteString(renderTree(m.nodes, -1, m.expanded, m.currentSession, m.filterText, m.styles))
		b.WriteString("\n\n")
		b.WriteString(m.createForm.View(m.styles))
	} else if m.mode == modeDelete && m.deleteTarget != nil {
		b.WriteString(renderTree(m.nodes, m.cursor, m.expanded, m.currentSession, m.filterText, m.styles))
		b.WriteString("\n\n")
		b.WriteString(m.styles.Error.Render(fmt.Sprintf(" Delete %s? (y/n)", m.deleteTarget.DisplayName)))
	} else if m.mode == modeRename && m.renameTarget != nil {
		b.WriteString(renderTree(m.nodes, m.cursor, m.expanded, m.currentSession, m.filterText, m.styles))
		b.WriteString("\n\n")
		b.WriteString(m.styles.Form.Render(" Rename") + "\n")
		b.WriteString(fmt.Sprintf("  %s", m.renameInput.View()))
	} else {
		b.WriteString(renderTree(m.nodes, m.cursor, m.expanded, m.currentSession, m.filterText, m.styles))
	}

	if m.mode == modeFilter {
		b.WriteString("\n\n")
		b.WriteString(fmt.Sprintf(" / %s", m.filterInput.View()))
	}

	if m.err != nil {
		b.WriteString("\n")
		b.WriteString(m.styles.Error.Render(m.err.Error()))
	}

	// Notification preview
	if notif := m.cursorNotification(); notif != "" {
		b.WriteString("\n\n")
		b.WriteString(m.styles.Notification.Render(fmt.Sprintf(" ★ %s", notif)))
	}

	// Help bar
	b.WriteString("\n\n")
	sep := m.styles.Separator.Render(strings.Repeat("─", max(m.width-2, 14)))
	b.WriteString(" " + sep)
	b.WriteString("\n")
	help := " c new  d delete  R rename  / filter"
	b.WriteString(m.styles.HelpBar.Render(help))

	return b.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
