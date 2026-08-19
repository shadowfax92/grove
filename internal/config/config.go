package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofrs/flock"
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
	lock, err := lockConfig(path)
	if err != nil {
		return fmt.Errorf("locking config: %w", err)
	}
	defer unlockConfig(lock)
	return addRepoToFileLocked(path, repo)
}

func addRepoToFileLocked(path string, repo RepoConfig) error {
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
			lock, lockErr := lockConfig(path)
			if lockErr != nil {
				return nil, fmt.Errorf("locking config: %w", lockErr)
			}
			defer unlockConfig(lock)
			data, err = os.ReadFile(path)
			if os.IsNotExist(err) {
				return createDefault(path)
			}
			if err != nil {
				return nil, fmt.Errorf("reading config: %w", err)
			}
		} else {
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}
	return parseConfig(data)
}

func parseConfig(data []byte) (*Config, error) {
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
		if repoNeedsNodeEncoding(repo) || mapping.Style&yaml.FlowStyle != 0 || len(mapping.Content) == 0 {
			return appendRepoNode(&root, mapping, nil, repo)
		}
		out := strings.TrimRight(string(data), "\n")
		if strings.TrimSpace(out) != "" {
			out += "\n\n"
		}
		out += "repos:\n" + entry
		updated := []byte(out + "\n")
		if err := validateAppendedRepo(updated, repo); err != nil {
			return nil, err
		}
		return updated, nil
	}
	if reposNode.Kind == yaml.ScalarNode && reposNode.Tag == "!!null" {
		return appendRepoNode(&root, mapping, reposNode, repo)
	}
	if reposNode.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("config repos must be a list")
	}
	if repoNeedsNodeEncoding(repo) || reposNode.Style&yaml.FlowStyle != 0 || len(reposNode.Content) == 0 {
		return appendRepoNode(&root, mapping, reposNode, repo)
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	keyIndex := reposKey.Line - 1
	if keyIndex < 0 || keyIndex >= len(lines) {
		return nil, fmt.Errorf("could not locate repos in config")
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
	updated := []byte(strings.Join(lines, "\n") + "\n")
	if err := validateAppendedRepo(updated, repo); err != nil {
		return nil, err
	}
	return updated, nil
}

func appendRepoNode(root, mapping, reposNode *yaml.Node, repo RepoConfig) ([]byte, error) {
	if reposNode == nil {
		reposNode = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		if mapping.Style&yaml.FlowStyle != 0 {
			reposNode.Style = yaml.FlowStyle
		}
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "repos"},
			reposNode,
		)
	} else if reposNode.Kind != yaml.SequenceNode {
		*reposNode = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", HeadComment: reposNode.HeadComment, LineComment: reposNode.LineComment, FootComment: reposNode.FootComment}
	}
	reposNode.Content = append(reposNode.Content, repositoryMappingNode(repo))
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(root); err != nil {
		return nil, fmt.Errorf("encoding config: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("encoding config: %w", err)
	}
	updated := output.Bytes()
	if err := validateAppendedRepo(updated, repo); err != nil {
		return nil, err
	}
	return updated, nil
}

func repositoryMappingNode(repo RepoConfig) *yaml.Node {
	mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	appendScalar := func(key, value string) {
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
		)
	}
	appendScalar("path", repo.Path)
	appendScalar("name", repo.Name)
	if repo.DefaultBranch != "" {
		appendScalar("default_branch", repo.DefaultBranch)
	}
	if repo.Workdir != "" {
		appendScalar("workdir", repo.Workdir)
	}
	if repo.Setup != nil {
		sequence := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, instruction := range repo.Setup {
			sequence.Content = append(sequence.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: instruction})
		}
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "setup"},
			sequence,
		)
	}
	return mapping
}

func repoNeedsNodeEncoding(repo RepoConfig) bool {
	for _, value := range []string{repo.Path, repo.Name, repo.DefaultBranch, repo.Workdir} {
		if strings.ContainsAny(value, "\r\n") {
			return true
		}
	}
	for _, instruction := range repo.Setup {
		if strings.ContainsAny(instruction, "\r\n") {
			return true
		}
	}
	return false
}

func validateAppendedRepo(data []byte, want RepoConfig) error {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("generated invalid config: %w", err)
	}
	if len(cfg.Repos) == 0 {
		return fmt.Errorf("generated config is missing the repository entry")
	}
	got := cfg.Repos[len(cfg.Repos)-1]
	if got.Path != want.Path || got.Name != want.Name || got.DefaultBranch != want.DefaultBranch || got.Workdir != want.Workdir || !sameStrings(got.Setup, want.Setup) {
		return fmt.Errorf("generated config changed repository values")
	}
	return nil
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
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
	if strings.ContainsAny(value, "\n\r\t") || strings.Contains(value, ": ") || strings.HasSuffix(value, ":") || strings.Contains(value, " #") {
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
	target, err := atomicWriteTarget(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".grove-config-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	mode := os.FileMode(0644)
	if info, err := os.Stat(target); err == nil {
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
	return os.Rename(tmpPath, target)
}

func atomicWriteTarget(path string) (string, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolving config symlink: %w", err)
	}
	return target, nil
}

func lockConfig(path string) (*flock.Flock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	lock := flock.New(path + ".lock")
	if err := lock.Lock(); err != nil {
		return nil, err
	}
	return lock, nil
}

func unlockConfig(lock *flock.Flock) {
	_ = lock.Close()
}
