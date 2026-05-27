package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Repos []RepoConfig `yaml:"repos"`
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

const DefaultPrepareCleanCommand = `git diff --quiet && git diff --cached --quiet || (echo "uncommitted changes in base repo" && exit 1)`

func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "grove", "config.yaml"), nil
}

// NewWorktreeRepo builds the default config entry created by `grove init`.
// The prepare commands keep the base checkout clean and on the configured branch before forking worktrees.
func NewWorktreeRepo(path, name, defaultBranch string) RepoConfig {
	return RepoConfig{
		Path:          path,
		Name:          name,
		DefaultBranch: defaultBranch,
		Layout:        "dev",
		Prepare: []string{
			DefaultPrepareCleanCommand,
			"git checkout " + defaultBranch,
		},
		Setup: []string{},
	}
}

// AddRepoToFile appends a repo entry to a Grove config file without rewriting unrelated sections.
// It rejects duplicate names or paths before editing so `grove init` is safe to run repeatedly.
func AddRepoToFile(path string, repo RepoConfig) error {
	if strings.TrimSpace(repo.Path) == "" {
		return fmt.Errorf("repo path is required")
	}
	if strings.TrimSpace(repo.Name) == "" {
		return fmt.Errorf("repo name is required")
	}

	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("stat config: %w", err)
		}
		if _, err := createDefault(path); err != nil {
			return fmt.Errorf("creating config: %w", err)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parsing config: %w", err)
	}
	if err := cfg.resolve(); err != nil {
		return err
	}
	if err := rejectDuplicateRepo(&cfg, repo); err != nil {
		return err
	}

	updated, err := appendRepoEntry(data, repo)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, updated, 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
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

func rejectDuplicateRepo(cfg *Config, repo RepoConfig) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	repoPath := normalizeRepoPath(repo.Path, home)

	for _, existing := range cfg.Repos {
		if existing.Name == repo.Name {
			return fmt.Errorf("repo name %s already exists", repo.Name)
		}
		if normalizeRepoPath(existing.Path, home) == repoPath {
			return fmt.Errorf("repo path %s already exists as %s", repoPath, existing.Name)
		}
	}
	return nil
}

func normalizeRepoPath(path, home string) string {
	path = expandTilde(path, home)
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func appendRepoEntry(data []byte, repo RepoConfig) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	mapping, err := rootMappingNode(&root)
	if err != nil {
		return nil, err
	}

	reposKey, reposNode := mappingValue(mapping, "repos")
	entry := renderRepoEntry(repo)
	if reposNode == nil {
		out := strings.TrimRight(string(data), "\n")
		if strings.TrimSpace(out) != "" {
			out += "\n\n"
		}
		out += "repos:\n" + entry
		return []byte(out + "\n"), nil
	}
	if reposNode.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("config repos must be a list")
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	keyIndex := reposKey.Line - 1
	if keyIndex < 0 || keyIndex >= len(lines) {
		return nil, fmt.Errorf("could not locate repos in config")
	}

	if isEmptyFlowSequenceLine(lines[keyIndex]) {
		lines[keyIndex] = strings.TrimSuffix(lines[keyIndex], " []")
		lines = insertLines(lines, keyIndex+1, strings.TrimRight(entry, "\n"))
		return []byte(strings.Join(lines, "\n") + "\n"), nil
	}

	insertAt := len(lines)
	for i := keyIndex + 1; i < len(lines); i++ {
		if isNextTopLevelKey(lines[i]) {
			insertAt = i
			break
		}
	}

	if len(reposNode.Content) > 0 {
		entry = "\n" + entry
	}
	lines = insertLines(lines, insertAt, strings.TrimRight(entry, "\n"))
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

func rootMappingNode(root *yaml.Node) (*yaml.Node, error) {
	if root.Kind == yaml.DocumentNode && len(root.Content) == 0 {
		root.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config must be a YAML mapping")
	}
	return root.Content[0], nil
}

func mappingValue(mapping *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i], mapping.Content[i+1]
		}
	}
	return nil, nil
}

func isEmptyFlowSequenceLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed == "repos: []"
}

func isNextTopLevelKey(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return false
	}
	return line == strings.TrimLeft(line, " \t")
}

func insertLines(lines []string, index int, block string) []string {
	blockLines := strings.Split(block, "\n")
	out := make([]string, 0, len(lines)+len(blockLines))
	out = append(out, lines[:index]...)
	out = append(out, blockLines...)
	out = append(out, lines[index:]...)
	return out
}

func renderRepoEntry(repo RepoConfig) string {
	var b strings.Builder
	b.WriteString("  - path: " + yamlScalar(repo.Path) + "\n")
	b.WriteString("    name: " + yamlScalar(repo.Name) + "\n")
	if repo.Type != "" {
		b.WriteString("    type: " + yamlScalar(repo.Type) + "\n")
	}
	if repo.DefaultBranch != "" {
		b.WriteString("    default_branch: " + yamlScalar(repo.DefaultBranch) + "\n")
	}
	if repo.Layout != "" {
		b.WriteString("    layout: " + yamlScalar(repo.Layout) + "\n")
	}
	if repo.Workdir != "" {
		b.WriteString("    workdir: " + yamlScalar(repo.Workdir) + "\n")
	}
	if repo.Prepare != nil {
		writeStringList(&b, "prepare", repo.Prepare)
	}
	if repo.Setup != nil {
		writeStringList(&b, "setup", repo.Setup)
	}
	return b.String()
}

func writeStringList(b *strings.Builder, key string, values []string) {
	if len(values) == 0 {
		b.WriteString("    " + key + ": []\n")
		return
	}
	b.WriteString("    " + key + ":\n")
	for _, value := range values {
		b.WriteString("      - " + yamlScalar(value) + "\n")
	}
}

func yamlScalar(value string) string {
	if isPlainYAMLScalar(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func isPlainYAMLScalar(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	if strings.ContainsAny(value, "\n\r\t") || strings.Contains(value, ": ") || strings.Contains(value, " #") {
		return false
	}
	switch strings.ToLower(value) {
	case "null", "~", "true", "false", "yes", "no", "on", "off":
		return false
	}
	for _, prefix := range []string{"#", "- ", "? ", "{", "}", "[", "]", ",", "&", "*", "!", "|", ">", "@", "`"} {
		if strings.HasPrefix(value, prefix) {
			return false
		}
	}
	return true
}
