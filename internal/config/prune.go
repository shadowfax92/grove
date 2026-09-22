package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Missing reports a vanished repository directory, not a malformed row or an
// inaccessible repository. Callers use resolved config rows so ~/ is expanded.
// Legacy directory entries are outside Grove's Git repository lifecycle.
func (r RepoConfig) Missing() bool {
	kind := strings.TrimSpace(r.Type)
	if (kind != "" && kind != "worktree") || !filepath.IsAbs(r.Path) {
		return false
	}
	_, err := os.Stat(r.Path)
	return os.IsNotExist(err)
}

// PruneMissingRepos removes only config rows whose repository directories are
// gone. It shares the registration lock and atomic writer with AddRepoToFile,
// rereading current config so a concurrent registration is never overwritten.
func PruneMissingRepos(path string, dryRun bool) ([]RepoConfig, error) {
	if !dryRun {
		lock, err := lockConfig(path)
		if err != nil {
			return nil, fmt.Errorf("locking config: %w", err)
		}
		defer unlockConfig(lock)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	cfg, err := parseConfig(data)
	if err != nil {
		return nil, err
	}
	var removed []RepoConfig
	missing := make(map[int]bool)
	for index, repo := range cfg.Repos {
		if repo.Missing() {
			missing[index] = true
			removed = append(removed, repo)
		}
	}
	if len(removed) == 0 || dryRun {
		return removed, nil
	}

	// Edit YAML nodes instead of serializing Config: unknown settings, aliases,
	// setup commands, and comments on retained rows belong to the user.
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	mapping, err := rootMappingNode(&root)
	if err != nil {
		return nil, err
	}
	_, repos := mappingValue(mapping, "repos")
	if repos == nil || repos.Kind != yaml.SequenceNode || len(repos.Content) != len(cfg.Repos) {
		return nil, fmt.Errorf("config repos must be a direct list to prune entries")
	}
	kept := make([]*yaml.Node, 0, len(repos.Content)-len(removed))
	for index, row := range repos.Content {
		if !missing[index] {
			kept = append(kept, row)
		}
	}
	repos.Content = kept
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&root); err != nil {
		return nil, fmt.Errorf("encoding config: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("encoding config: %w", err)
	}
	// Removing a YAML anchor used by a surviving row must fail without writing.
	if _, err := parseConfig(output.Bytes()); err != nil {
		return nil, fmt.Errorf("validating pruned config: %w", err)
	}
	if err := writeFileAtomic(path, output.Bytes()); err != nil {
		return nil, fmt.Errorf("writing config: %w", err)
	}
	return removed, nil
}
