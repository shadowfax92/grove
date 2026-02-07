package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"grove/internal/config"
	"grove/internal/git"
	"grove/internal/names"
	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type CreateMode int

const (
	CreateWorktree CreateMode = iota
	CreatePlain
)

type CreateForm struct {
	mode     CreateMode
	repoName string
	repo     *config.RepoConfig
	input    textinput.Model
	cfg      *config.Config
	stateMgr *state.StateManager
	st       *state.State
	err      error
}

type workspaceCreatedMsg struct {
	workspace state.Workspace
}

type createCancelledMsg struct{}
type createErrorMsg struct{ err error }

func NewCreateForm(mode CreateMode, repoName string, cfg *config.Config, stateMgr *state.StateManager, st *state.State) CreateForm {
	ti := textinput.New()
	ti.Focus()
	ti.CharLimit = 64
	ti.Width = 30

	var repo *config.RepoConfig
	if mode == CreateWorktree {
		ti.Placeholder = "branch name (empty = random)"
		repo = cfg.FindRepo(repoName)
	} else {
		ti.Placeholder = "workspace name (empty = random)"
	}

	return CreateForm{
		mode:     mode,
		repoName: repoName,
		repo:     repo,
		input:    ti,
		cfg:      cfg,
		stateMgr: stateMgr,
		st:       st,
	}
}

func (f CreateForm) Update(msg tea.Msg) (CreateForm, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			return f, f.submit()
		case "esc":
			return f, func() tea.Msg { return createCancelledMsg{} }
		}
	}

	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return f, cmd
}

func (f CreateForm) View(styles Styles) string {
	header := "New workspace"
	prompt := "Name"
	if f.mode == CreateWorktree {
		header = fmt.Sprintf("New worktree in %s", f.repoName)
		prompt = "Branch"
	}

	s := styles.Form.Render(header) + "\n"
	s += fmt.Sprintf(" %s: %s", prompt, f.input.View())

	if f.err != nil {
		s += "\n" + styles.Error.Render(f.err.Error())
	}

	return s
}

func (f CreateForm) submit() tea.Cmd {
	return func() tea.Msg {
		name := f.input.Value()

		if f.mode == CreateWorktree {
			return f.createWorktree(name)
		}
		return f.createPlain(name)
	}
}

func (f CreateForm) createWorktree(branch string) tea.Msg {
	if f.repo == nil {
		return createErrorMsg{fmt.Errorf("repo %q not found", f.repoName)}
	}

	if branch == "" {
		existing := existingNames(f.st, f.repoName)
		branch = names.Generate(existing)
	}

	sessionName := fmt.Sprintf("grove/%s/%s", f.repoName, branch)
	if f.stateMgr.FindBySession(f.st, sessionName) != nil {
		return createErrorMsg{fmt.Errorf("workspace %s/%s already exists", f.repoName, branch)}
	}

	worktreePath := filepath.Join(f.repo.Path, ".grove", "worktrees", branch)

	_ = git.EnsureGitignore(f.repo.Path)

	if err := git.AddWorktree(f.repo.Path, worktreePath, branch); err != nil {
		return createErrorMsg{fmt.Errorf("creating worktree: %w", err)}
	}

	for _, setupCmd := range f.repo.Setup {
		c := exec.Command("sh", "-c", setupCmd)
		c.Dir = worktreePath
		if err := c.Run(); err != nil {
			return createErrorMsg{fmt.Errorf("setup %q failed: %w", setupCmd, err)}
		}
	}

	if err := tmux.NewSession(sessionName, worktreePath); err != nil {
		return createErrorMsg{fmt.Errorf("creating session: %w", err)}
	}

	ws := state.Workspace{
		Name:         fmt.Sprintf("%s/%s", f.repoName, branch),
		Type:         "worktree",
		Repo:         f.repoName,
		RepoPath:     f.repo.Path,
		WorktreePath: worktreePath,
		Branch:       branch,
		SessionName:  sessionName,
	}

	return workspaceCreatedMsg{workspace: ws}
}

func (f CreateForm) createPlain(name string) tea.Msg {
	if name == "" {
		existing := existingNames(f.st, "")
		name = names.Generate(existing)
	}

	sessionName := fmt.Sprintf("grove/%s", name)
	if f.stateMgr.FindBySession(f.st, sessionName) != nil {
		return createErrorMsg{fmt.Errorf("workspace %q already exists", name)}
	}

	home, _ := os.UserHomeDir()
	if err := tmux.NewSession(sessionName, home); err != nil {
		return createErrorMsg{fmt.Errorf("creating session: %w", err)}
	}

	ws := state.Workspace{
		Name:        name,
		Type:        "plain",
		Path:        home,
		SessionName: sessionName,
	}

	return workspaceCreatedMsg{workspace: ws}
}

func existingNames(st *state.State, repoName string) []string {
	var names []string
	for _, ws := range st.Workspaces {
		if repoName == "" || ws.Repo == repoName {
			if ws.Branch != "" {
				names = append(names, ws.Branch)
			} else {
				names = append(names, ws.Name)
			}
		}
	}
	return names
}
