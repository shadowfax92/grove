package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"grove/internal/config"
	"grove/internal/git"
	"grove/internal/names"
	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	newCmd.Flags().Bool("no-switch", false, "Don't switch to the new session after creation")
	newCmd.Flags().Bool("cd", false, "Create workspace, print path (no tmux session)")
	rootCmd.AddCommand(newCmd)
}

var newCmd = &cobra.Command{
	Use:         "new [name] [branch]",
	Aliases:     []string{"n"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Create a new workspace",
	Long: `Create a new workspace and switch to it.

  grove new              — pick repo or type session name via fzf
  grove new <repo>       — pick or auto-generate branch in repo
  grove new <repo> <br>  — specific branch in repo
  grove new <name>       — plain session (if name doesn't match a repo)
  grove new --cd         — create workspace, print path: cd (gv n --cd)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		noSwitch, _ := cmd.Flags().GetBool("no-switch")
		dirOnly, _ := cmd.Flags().GetBool("cd")

		cfg, err := config.Load()
		if err != nil {
			return err
		}

		mgr, err := state.NewManager()
		if err != nil {
			return err
		}
		if err := mgr.Lock(); err != nil {
			return err
		}
		defer mgr.Unlock()

		st, err := mgr.Load()
		if err != nil {
			return err
		}

		var name, branch string
		switch len(args) {
		case 0:
			picked, err := pickRepoOrNameFzf(cfg)
			if err != nil {
				return err
			}
			name = picked
		case 1:
			name = args[0]
		default:
			name = args[0]
			branch = args[1]
		}

		repo := cfg.FindRepo(name)
		if repo != nil {
			return createWorktree(repo, branch, cfg, mgr, st, noSwitch, dirOnly)
		}
		return createPlain(name, mgr, st, noSwitch, dirOnly)
	},
}

func createPlain(name string, mgr *state.StateManager, st *state.State, noSwitch, dirOnly bool) error {
	sessionName := fmt.Sprintf("g/%s", name)
	if mgr.FindBySession(st, sessionName) != nil {
		return fmt.Errorf("workspace %q already exists", name)
	}

	dir, _ := os.UserHomeDir()

	if dirOnly {
		fmt.Println(dir)
		return nil
	}

	if err := tmux.NewSession(sessionName, dir); err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	ws := state.Workspace{
		Name:        name,
		Type:        "plain",
		Path:        dir,
		SessionName: sessionName,
	}
	mgr.AddWorkspace(st, ws)

	if err := mgr.Save(st); err != nil {
		return err
	}

	fmt.Printf("Created workspace %q\n", name)

	if !noSwitch && tmux.IsInsideTmux() {
		return tmux.SwitchClient(sessionName)
	}
	return nil
}

func createWorktree(repo *config.RepoConfig, branch string, cfg *config.Config, mgr *state.StateManager, st *state.State, noSwitch, dirOnly bool) error {
	if branch == "" {
		existing := existingWorktreeNames(st, repo.Name)

		branches, _ := git.ListBranches(repo.Path)
		usedSet := make(map[string]bool)
		for _, e := range existing {
			usedSet[e] = true
		}
		var available []string
		for _, b := range branches {
			if !usedSet[b] {
				available = append(available, b)
			}
		}

		prompted, err := promptNameFzf("branch > ", "Select branch, type new name, or enter for random", available)
		if err != nil {
			return err
		}
		if prompted != "" {
			branch = prompted
		} else {
			branch = names.Generate(existing)
		}
	}

	sessionName := fmt.Sprintf("g/%s/%s", repo.Name, branch)
	if mgr.FindBySession(st, sessionName) != nil {
		return fmt.Errorf("workspace %q already exists", repo.Name+"/"+branch)
	}

	worktreePath := filepath.Join(repo.Path, ".grove", "worktrees", branch)

	if err := git.EnsureGitignore(repo.Path); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update .gitignore: %v\n", err)
	}

	if err := git.AddWorktree(repo.Path, worktreePath, branch); err != nil {
		return fmt.Errorf("creating worktree: %w", err)
	}

	for _, setupCmd := range repo.Setup {
		fmt.Printf("Running: %s\n", setupCmd)
		c := exec.Command("sh", "-c", setupCmd)
		c.Dir = worktreePath
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: setup command failed: %v\n", err)
		}
	}

	if dirOnly {
		fmt.Println(worktreePath)
		return nil
	}

	layout := cfg.FindLayout(repo.Layout)
	if err := tmux.CreateSessionWithLayout(sessionName, worktreePath, layout); err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	ws := state.Workspace{
		Name:         fmt.Sprintf("%s/%s", repo.Name, branch),
		Type:         "worktree",
		Repo:         repo.Name,
		RepoPath:     repo.Path,
		WorktreePath: worktreePath,
		Branch:       branch,
		SessionName:  sessionName,
	}
	mgr.AddWorkspace(st, ws)

	if err := mgr.Save(st); err != nil {
		return err
	}

	fmt.Printf("Created worktree workspace %s/%s\n", repo.Name, branch)

	if !noSwitch && tmux.IsInsideTmux() {
		return tmux.SwitchClient(sessionName)
	}
	return nil
}

func pickRepoOrNameFzf(cfg *config.Config) (string, error) {
	var repoNames []string
	for _, r := range cfg.Repos {
		repoNames = append(repoNames, r.Name)
	}

	fzfCmd := exec.Command("fzf",
		"--prompt", "repo or name > ",
		"--header", "Pick a repo or type a session name",
		"--print-query",
		"--height", "~40%",
		"--reverse",
	)
	fzfCmd.Stdin = strings.NewReader(strings.Join(repoNames, "\n"))
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil && len(out) == 0 {
		return "", ErrCancelled
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	result := ""
	if len(lines) >= 2 && lines[1] != "" {
		result = lines[1]
	} else if len(lines) >= 1 {
		result = lines[0]
	}
	result = strings.TrimSpace(result)

	if result == "" {
		return "", ErrCancelled
	}
	return result, nil
}

const autoGenerateLabel = "(auto-generate)"

func promptNameFzf(prompt, header string, options []string) (string, error) {
	lines := append([]string{autoGenerateLabel}, options...)

	fzfCmd := exec.Command("fzf",
		"--prompt", prompt,
		"--header", header,
		"--print-query",
		"--height", "~40%",
		"--reverse",
	)
	fzfCmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil && len(out) == 0 {
		return "", ErrCancelled
	}

	outputLines := strings.Split(strings.TrimSpace(string(out)), "\n")
	result := ""
	if len(outputLines) >= 2 && outputLines[1] != "" {
		result = outputLines[1]
	} else if len(outputLines) >= 1 {
		result = outputLines[0]
	}
	result = strings.TrimSpace(result)

	if result == autoGenerateLabel || result == "" {
		return "", nil
	}
	return result, nil
}

func existingWorktreeNames(st *state.State, repoName string) []string {
	var result []string
	for _, ws := range st.Workspaces {
		if ws.Type == "worktree" && ws.Repo == repoName {
			result = append(result, ws.Branch)
		}
	}
	return result
}
