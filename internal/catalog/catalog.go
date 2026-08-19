package catalog

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"grove/internal/config"
	gitx "grove/internal/git"
)

type Warning struct {
	Name    string
	Path    string
	Message string
}

func (w Warning) Error() string {
	label := w.Name
	if label == "" {
		label = filepath.Base(w.Path)
	}
	return fmt.Sprintf("repo %s (%s): %s", label, w.Path, w.Message)
}

type Profile struct {
	Name          string
	Path          string
	Workdir       string
	DefaultBranch string
	Setup         []string
}

type Repository struct {
	Name           string
	Git            *gitx.Repository
	DefaultBranch  string
	Profiles       []*Profile
	defaultProfile *Profile
	defaultScore   int
}

func (r *Repository) DefaultProfile() *Profile {
	return r.defaultProfile
}

func (r *Repository) Aliases() []string {
	seen := make(map[string]bool)
	aliases := make([]string, 0, len(r.Profiles))
	for _, profile := range r.Profiles {
		if profile.Name == "" || seen[profile.Name] {
			continue
		}
		seen[profile.Name] = true
		aliases = append(aliases, profile.Name)
	}
	sort.Strings(aliases)
	return aliases
}

type binding struct {
	repository *Repository
	profile    *Profile
}

type Catalog struct {
	Repositories      []*Repository
	Current           *Repository
	CurrentRegistered bool
	byCommon          map[string]*Repository
	bindings          map[string][]binding
	reservedNames     map[string]bool
}

func Build(cfg *config.Config, currentDir string) (*Catalog, []Warning) {
	catalog := &Catalog{
		byCommon:      make(map[string]*Repository),
		bindings:      make(map[string][]binding),
		reservedNames: make(map[string]bool),
	}
	var warnings []Warning
	if cfg != nil {
		for _, row := range cfg.Repos {
			name := strings.TrimSpace(row.Name)
			if name != "" {
				catalog.reservedNames[name] = true
			}
			kind := strings.TrimSpace(row.Type)
			if kind != "" && kind != "worktree" {
				warnings = append(warnings, Warning{Name: name, Path: row.Path, Message: fmt.Sprintf("unsupported type %q", kind)})
				continue
			}
			repo, err := gitx.OpenRepository(row.Path)
			if err != nil {
				warnings = append(warnings, Warning{Name: name, Path: row.Path, Message: err.Error()})
				continue
			}
			if name == "" {
				name = filepath.Base(repo.MainPath)
			}
			profile := &Profile{
				Name:          name,
				Path:          row.Path,
				Workdir:       filepath.Clean(row.Workdir),
				DefaultBranch: row.DefaultBranch,
				Setup:         append([]string(nil), row.Setup...),
			}
			if row.Workdir == "" {
				profile.Workdir = ""
			}

			entry := catalog.byCommon[repo.CommonDir]
			if entry == nil {
				entry = &Repository{Git: repo}
				catalog.byCommon[repo.CommonDir] = entry
				catalog.Repositories = append(catalog.Repositories, entry)
			}
			entry.Profiles = append(entry.Profiles, profile)
			score := profileScore(repo.MainPath, row.Path, profile.Workdir)
			if entry.defaultProfile == nil || score > entry.defaultScore {
				entry.defaultProfile = profile
				entry.defaultScore = score
				entry.Name = catalog.uniqueRepositoryName(name, entry)
				entry.DefaultBranch = row.DefaultBranch
			}
			if catalog.bind(name, entry, profile) {
				warnings = append(warnings, Warning{Name: name, Path: row.Path, Message: "duplicate alias refers to more than one repository"})
			}
		}
	}

	if currentDir != "" {
		if current, err := gitx.OpenRepository(currentDir); err == nil {
			if existing := catalog.byCommon[current.CommonDir]; existing != nil {
				catalog.Current = existing
				catalog.CurrentRegistered = true
			} else {
				name := catalog.UniqueName(filepath.Base(current.MainPath))
				profile := &Profile{Name: name, Path: current.MainPath, DefaultBranch: gitx.DefaultBranch(current.MainPath)}
				entry := &Repository{
					Name:           name,
					Git:            current,
					DefaultBranch:  profile.DefaultBranch,
					Profiles:       []*Profile{profile},
					defaultProfile: profile,
					defaultScore:   30,
				}
				catalog.byCommon[current.CommonDir] = entry
				catalog.Repositories = append(catalog.Repositories, entry)
				catalog.Current = entry
				catalog.bind(name, entry, profile)
			}
		}
	}
	return catalog, warnings
}

func (c *Catalog) FindRepository(name string) (*Repository, *Profile, error) {
	bindings := c.bindings[name]
	if len(bindings) == 0 {
		return nil, nil, fmt.Errorf("repository %q not found", name)
	}
	first := bindings[0]
	for _, candidate := range bindings[1:] {
		if candidate.repository != first.repository {
			return nil, nil, fmt.Errorf("repository alias %q is ambiguous", name)
		}
	}
	return first.repository, first.profile, nil
}

func (c *Catalog) UniqueName(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "repo"
	}
	if !c.reservedNames[base] {
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s-%d", base, suffix)
		if !c.reservedNames[candidate] {
			return candidate
		}
	}
}

func (c *Catalog) bind(name string, repository *Repository, profile *Profile) bool {
	if name == "" {
		return false
	}
	conflict := false
	for _, existing := range c.bindings[name] {
		if existing.repository != repository {
			conflict = true
		}
		if existing.repository == repository && existing.profile.Name == profile.Name {
			return conflict
		}
	}
	c.bindings[name] = append(c.bindings[name], binding{repository: repository, profile: profile})
	return conflict
}

func (c *Catalog) uniqueRepositoryName(base string, self *Repository) string {
	used := make(map[string]bool, len(c.Repositories))
	for _, repo := range c.Repositories {
		if repo == self {
			continue
		}
		used[repo.Name] = true
	}
	if !used[base] {
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s-%d", base, suffix)
		if !used[candidate] {
			return candidate
		}
	}
}

func profileScore(mainPath, configuredPath, workdir string) int {
	configured, err := filepath.EvalSymlinks(configuredPath)
	if err != nil {
		return 0
	}
	configured, err = filepath.Abs(configured)
	if err != nil || filepath.Clean(configured) != filepath.Clean(mainPath) {
		return 10
	}
	if workdir == "" {
		return 30
	}
	return 20
}
