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
	Workdir       string   `yaml:"workdir"`
	Setup         []string `yaml:"setup"`
}

func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "grove", "config.yaml"), nil
}

func NewWorktreeRepo(path, name, defaultBranch string) RepoConfig {
	return RepoConfig{
		Path:          path,
		Name:          name,
		DefaultBranch: defaultBranch,
		Setup:         []string{},
	}
}

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
	if err := writeFileAtomic(path, updated); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

func Load() (*Config, error) {
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
	return &cfg, nil
}

func (c *Config) resolve() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	for index := range c.Repos {
		c.Repos[index].Path = expandTilde(c.Repos[index].Path, home)
		if c.Repos[index].Name == "" {
			c.Repos[index].Name = filepath.Base(c.Repos[index].Path)
		}
		if c.Repos[index].Type == "" {
			c.Repos[index].Type = "worktree"
		}
	}
	return nil
}

func createDefault(path string) (*Config, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	raw := Config{Repos: []RepoConfig{}}
	data, err := yaml.Marshal(raw)
	if err != nil {
		return nil, err
	}
	data = append([]byte("# Grove configuration\n\n"), data...)
	if err := writeFileAtomic(path, data); err != nil {
		return nil, err
	}
	return &raw, nil
}

func expandTilde(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
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
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
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
	if strings.TrimSpace(lines[keyIndex]) == "repos: []" {
		lines[keyIndex] = strings.TrimSuffix(lines[keyIndex], " []")
		lines = insertLines(lines, keyIndex+1, strings.TrimRight(entry, "\n"))
		return []byte(strings.Join(lines, "\n") + "\n"), nil
	}

	insertAt := len(lines)
	for index := keyIndex + 1; index < len(lines); index++ {
		if isNextTopLevelKey(lines[index]) {
			insertAt = index
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
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index], mapping.Content[index+1]
		}
	}
	return nil, nil
}

func isNextTopLevelKey(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed != "" && !strings.HasPrefix(trimmed, "#") && line == strings.TrimLeft(line, " \t")
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
	var out strings.Builder
	out.WriteString("  - path: " + yamlScalar(repo.Path) + "\n")
	out.WriteString("    name: " + yamlScalar(repo.Name) + "\n")
	if repo.DefaultBranch != "" {
		out.WriteString("    default_branch: " + yamlScalar(repo.DefaultBranch) + "\n")
	}
	if repo.Workdir != "" {
		out.WriteString("    workdir: " + yamlScalar(repo.Workdir) + "\n")
	}
	if repo.Setup != nil {
		writeStringList(&out, "setup", repo.Setup)
	}
	return out.String()
}

func writeStringList(out *strings.Builder, key string, values []string) {
	if len(values) == 0 {
		out.WriteString("    " + key + ": []\n")
		return
	}
	out.WriteString("    " + key + ":\n")
	for _, value := range values {
		out.WriteString("      - " + yamlScalar(value) + "\n")
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

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".grove-config-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	mode := os.FileMode(0644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
