package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Prefix  string                  `yaml:"prefix"`
	Sidebar SidebarConfig           `yaml:"sidebar"`
	Layouts map[string]LayoutConfig `yaml:"layouts"`
	Repos   []RepoConfig            `yaml:"repos"`
}

type LayoutConfig struct {
	Windows []WindowConfig `yaml:"windows"`
}

type WindowConfig struct {
	Name  string       `yaml:"name"`
	Split string       `yaml:"split"` // "horizontal" (side by side, default) or "vertical" (stacked)
	Panes []PaneConfig `yaml:"panes"`
}

type PaneConfig struct {
	Name string `yaml:"name"`
	Cmd  string `yaml:"cmd"`
	Size string `yaml:"size"` // e.g. "70%"
}

type SidebarConfig struct {
	Width    string `yaml:"width"`
	Height   string `yaml:"height"`
	Position string `yaml:"position"`
}

type RepoConfig struct {
	Path          string   `yaml:"path"`
	Name          string   `yaml:"name"`
	DefaultBranch string   `yaml:"default_branch"`
	Layout        string   `yaml:"layout"`
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

// LoadFast skips repo path validation — used by the sidebar for speed.
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
	}

	if c.Prefix == "" {
		c.Prefix = "C-s"
	}
	if c.Sidebar.Width == "" {
		c.Sidebar.Width = "30%"
	}
	if c.Sidebar.Height == "" {
		c.Sidebar.Height = "50%"
	}
	if c.Sidebar.Position == "" {
		c.Sidebar.Position = "center"
	}

	return nil
}

func (c *Config) validate() error {
	for name, layout := range c.Layouts {
		if len(layout.Windows) == 0 {
			return fmt.Errorf("layout %s: must have at least one window", name)
		}
		for _, win := range layout.Windows {
			if win.Name == "" {
				return fmt.Errorf("layout %s: window name is required", name)
			}
			if win.Split != "" && win.Split != "horizontal" && win.Split != "vertical" {
				return fmt.Errorf("layout %s: window %s: split must be \"horizontal\" or \"vertical\"", name, win.Name)
			}
			if len(win.Panes) == 0 {
				return fmt.Errorf("layout %s: window %s: must have at least one pane", name, win.Name)
			}
			for j, pane := range win.Panes {
				if pane.Size != "" {
					s := strings.TrimSuffix(strings.TrimSpace(pane.Size), "%")
					if _, err := strconv.Atoi(s); err != nil {
						return fmt.Errorf("layout %s: window %s: pane %d: invalid size %q", name, win.Name, j, pane.Size)
					}
				}
			}
		}
	}

	seen := make(map[string]bool)
	for _, r := range c.Repos {
		if seen[r.Name] {
			return fmt.Errorf("duplicate repo name: %s", r.Name)
		}
		seen[r.Name] = true

		info, err := os.Stat(r.Path)
		if err != nil {
			return fmt.Errorf("repo %s: path %s does not exist", r.Name, r.Path)
		}
		if !info.IsDir() {
			return fmt.Errorf("repo %s: path %s is not a directory", r.Name, r.Path)
		}

		if r.Layout != "" && c.FindLayout(r.Layout) == nil {
			return fmt.Errorf("repo %s: layout %q not found", r.Name, r.Layout)
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

func (c *Config) FindLayout(name string) *LayoutConfig {
	if c.Layouts == nil || name == "" {
		return nil
	}
	if l, ok := c.Layouts[name]; ok {
		return &l
	}
	return nil
}

func (c *Config) LayoutNames() []string {
	var names []string
	for name := range c.Layouts {
		names = append(names, name)
	}
	return names
}

func createDefault(path string) (*Config, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}

	cfg := &Config{
		Prefix: "C-s",
		Sidebar: SidebarConfig{
			Width:    "30%",
			Height:   "50%",
			Position: "center",
		},
		Repos:     []RepoConfig{},
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
