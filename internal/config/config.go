package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Notify NotifyConfig `yaml:"notify"`
	Shadow ShadowConfig `yaml:"shadow"`
	Repos  []RepoConfig `yaml:"repos"`
}

type ShadowConfig struct {
	Popup ShadowPopupConfig `yaml:"popup"`
	Keys  ShadowKeys        `yaml:"keys"`
}

type PopupSize struct {
	Width  string `yaml:"width,omitempty"`
	Height string `yaml:"height,omitempty"`
}

type PopupMatch struct {
	MinClientWidth int `yaml:"min_client_width,omitempty"`
	MaxClientWidth int `yaml:"max_client_width,omitempty"`
}

type PopupProfile struct {
	Name  string     `yaml:"name"`
	Match PopupMatch `yaml:"match,omitempty"`
	Vim   PopupSize  `yaml:"vim,omitempty"`
	Shell PopupSize  `yaml:"shell,omitempty"`
}

type ShadowPopupConfig struct {
	Width    string         `yaml:"width,omitempty"`
	Height   string         `yaml:"height,omitempty"`
	Vim      PopupSize      `yaml:"vim,omitempty"`
	Shell    PopupSize      `yaml:"shell,omitempty"`
	Profiles []PopupProfile `yaml:"profiles,omitempty"`
}

type ShadowKeys struct {
	Vim   string `yaml:"vim"`
	Shell string `yaml:"shell"`
}

type NotifyConfig struct {
	Forward []string `yaml:"forward"`
}

type RepoConfig struct {
	Path          string   `yaml:"path"`
	Name          string   `yaml:"name"`
	Type          string   `yaml:"type"`
	DefaultBranch string   `yaml:"default_branch"`
	Layout        string   `yaml:"layout"`
	Workdir       string   `yaml:"workdir"`
	Prepare       []string `yaml:"prepare"`
	Setup         []string `yaml:"setup"`
}

func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "grove", "config.yaml"), nil
}

func Load() (*Config, error) {
	return load(true)
}

func LoadFast() (*Config, error) {
	return load(false)
}

func load(validate bool) (*Config, error) {
	path, err := DefaultConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return createDefault(path)
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := cfg.resolve(); err != nil {
		return nil, err
	}

	if validate {
		if err := cfg.validate(); err != nil {
			return nil, err
		}
	}

	return &cfg, nil
}

func (c *Config) resolve() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	for i := range c.Repos {
		c.Repos[i].Path = expandTilde(c.Repos[i].Path, home)
		if c.Repos[i].Name == "" {
			c.Repos[i].Name = filepath.Base(c.Repos[i].Path)
		}
		if c.Repos[i].Type == "" {
			c.Repos[i].Type = "worktree"
		}
	}

	if c.Shadow.Popup.Width == "" {
		c.Shadow.Popup.Width = "80%"
	}
	if c.Shadow.Popup.Height == "" {
		c.Shadow.Popup.Height = "85%"
	}
	if c.Shadow.Keys.Vim == "" {
		c.Shadow.Keys.Vim = "M-v"
	}
	if c.Shadow.Keys.Shell == "" {
		c.Shadow.Keys.Shell = "M-b"
	}

	return nil
}

func (c *Config) validate() error {
	seen := make(map[string]bool)
	for _, r := range c.Repos {
		if seen[r.Name] {
			return fmt.Errorf("duplicate repo name: %s", r.Name)
		}
		seen[r.Name] = true

		if r.Type == "plain" {
			continue
		}

		info, err := os.Stat(r.Path)
		if err != nil {
			return fmt.Errorf("repo %s: path %s does not exist", r.Name, r.Path)
		}
		if !info.IsDir() {
			return fmt.Errorf("repo %s: path %s is not a directory", r.Name, r.Path)
		}
	}
	return nil
}

func (c *Config) FindRepo(name string) *RepoConfig {
	for i := range c.Repos {
		if c.Repos[i].Name == name {
			return &c.Repos[i]
		}
	}
	return nil
}

func createDefault(path string) (*Config, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}

	cfg := &Config{
		Repos: []RepoConfig{},
	}
	if err := cfg.resolve(); err != nil {
		return nil, err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}

	header := "# Grove configuration\n# See: grove config --path for file location\n\n"
	if err := os.WriteFile(path, []byte(header+string(data)), 0644); err != nil {
		return nil, err
	}

	return cfg, nil
}

func expandTilde(path, home string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	if path == "~" {
		return home
	}
	return path
}

// PopupFor returns the popup size for the given shadow type ("vim"/"shell"/"sh").
// Resolution order (first non-empty wins per-field):
//   1. Active profile (GROVE_PROFILE env, else matched by tmux client width)
//   2. Top-level vim/shell blocks
//   3. Legacy top-level width/height
//   4. Hard-coded 90%/90%
func (p ShadowPopupConfig) PopupFor(typ string) PopupSize {
	size, _ := p.ResolvePopup(typ)
	return size
}

// ResolvePopup picks the popup size and returns the active profile name
// ("" when no profile matched).
func (p ShadowPopupConfig) ResolvePopup(typ string) (PopupSize, string) {
	profile := p.SelectProfile(TmuxClientWidth())

	size := PopupSize{}
	if profile != nil {
		size = popupSizeFor(*profile, typ)
	}
	if size.Width == "" || size.Height == "" {
		top := topLevelSize(p, typ)
		if size.Width == "" {
			size.Width = top.Width
		}
		if size.Height == "" {
			size.Height = top.Height
		}
	}
	if size.Width == "" {
		size.Width = p.Width
	}
	if size.Height == "" {
		size.Height = p.Height
	}
	if size.Width == "" {
		size.Width = "90%"
	}
	if size.Height == "" {
		size.Height = "90%"
	}

	name := ""
	if profile != nil {
		name = profile.Name
	}
	return size, name
}

// SelectProfile returns the profile chosen by $GROVE_PROFILE env override or
// by matching the given tmux client width. Returns nil when none applies.
func (p ShadowPopupConfig) SelectProfile(clientWidth int) *PopupProfile {
	if name := strings.TrimSpace(os.Getenv("GROVE_PROFILE")); name != "" {
		for i := range p.Profiles {
			if p.Profiles[i].Name == name {
				return &p.Profiles[i]
			}
		}
	}
	if clientWidth <= 0 {
		return nil
	}
	for i := range p.Profiles {
		if p.Profiles[i].Match.Matches(clientWidth) {
			return &p.Profiles[i]
		}
	}
	return nil
}

func (m PopupMatch) Matches(width int) bool {
	if m.MinClientWidth == 0 && m.MaxClientWidth == 0 {
		return false
	}
	if m.MinClientWidth > 0 && width < m.MinClientWidth {
		return false
	}
	if m.MaxClientWidth > 0 && width > m.MaxClientWidth {
		return false
	}
	return true
}

func popupSizeFor(profile PopupProfile, typ string) PopupSize {
	switch typ {
	case "vim":
		return profile.Vim
	case "shell", "sh":
		return profile.Shell
	}
	return PopupSize{}
}

func topLevelSize(p ShadowPopupConfig, typ string) PopupSize {
	switch typ {
	case "vim":
		return p.Vim
	case "shell", "sh":
		return p.Shell
	}
	return PopupSize{}
}

// TmuxClientWidth returns the current tmux client width in cells, or 0 if
// unavailable (not inside tmux, command failed, etc.).
func TmuxClientWidth() int {
	if os.Getenv("TMUX") == "" {
		return 0
	}
	out, err := exec.Command("tmux", "display", "-p", "#{client_width}").Output()
	if err != nil {
		return 0
	}
	w, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return w
}
