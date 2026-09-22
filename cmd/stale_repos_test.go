package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"grove/internal/config"
	"grove/internal/picker"
)

func TestRemovePrunesMissingRepoConfigInEveryMode(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{"exact", []string{"rm", "feat/remove"}},
		{"picker", []string{"rm"}},
		{"null", []string{"--null", "rm", "feat/remove"}},
		{"json", []string{"--json", "rm", "feat/remove"}},
		{"merged", []string{"rm", "--merged"}},
		{"older-than", []string{"rm", "--older-than", "14d"}},
		{"missing", []string{"rm", "--missing"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repoPath := initV2Repo(t)
			linkedPath := filepath.Join(t.TempDir(), "linked")
			runV2Git(t, repoPath, "worktree", "add", "-b", "feat/remove", linkedPath)
			canonicalLinked := canonicalV2Path(t, linkedPath)
			writeV2Config(t, repoPath, "  - path: /definitely/deleted/grove-repo\n    name: deleted\n")
			root := newRootCommand(commandDependencies{
				getwd:       func() (string, error) { return repoPath, nil },
				interactive: func() bool { return true },
				pickMany: func(string, []picker.Item) ([]string, error) {
					return []string{canonicalLinked}, nil
				},
			})
			stdout, stderr, err := executeV2(root, test.args...)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(stderr, "Pruned 1 stale repository entry") || !strings.Contains(stderr, "deleted") {
				t.Fatalf("cleanup summary = %q", stderr)
			}
			cfg, err := config.Load()
			if err != nil || len(cfg.Repos) != 1 || cfg.Repos[0].Name != "app" {
				t.Fatalf("config = %#v, %v", cfg, err)
			}
			switch test.name {
			case "exact", "picker":
				if stdout != canonicalV2Path(t, repoPath)+"\n" {
					t.Fatalf("path stdout = %q", stdout)
				}
			case "null":
				if stdout != canonicalV2Path(t, repoPath)+"\x00" {
					t.Fatalf("NUL stdout = %q", stdout)
				}
			case "json":
				if !json.Valid([]byte(stdout)) {
					t.Fatalf("JSON stdout = %q", stdout)
				}
			}
		})
	}
}

func TestRemovalDryRunFailureAndCancellationKeepStaleRepoConfig(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"missing dry run", []string{"rm", "--missing", "--dry-run"}, ""},
		{"merged dry run", []string{"rm", "--merged", "--dry-run"}, ""},
		{"age dry run", []string{"rm", "--older-than", "14d", "--dry-run"}, ""},
		{"invalid selector", []string{"rm", "does-not-exist"}, "error"},
		{"protected main", []string{"rm", "."}, "error"},
		{"invalid flags", []string{"rm", "--missing", "--merged"}, "error"},
		{"cancellation", []string{"rm"}, "cancel"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repoPath := initV2Repo(t)
			writeV2Config(t, repoPath, "  - path: /definitely/deleted/grove-repo\n    name: deleted\n")
			path, err := config.DefaultConfigPath()
			if err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			root := newRootCommand(commandDependencies{
				getwd:       func() (string, error) { return repoPath, nil },
				interactive: func() bool { return true },
				pickMany: func(string, []picker.Item) ([]string, error) {
					return nil, picker.ErrCancelled
				},
			})
			_, stderr, err := executeV2(root, test.args...)
			switch test.want {
			case "":
				if err != nil || !strings.Contains(stderr, "Would prune 1 stale repository entry") {
					t.Fatalf("dry-run stderr = %q, error = %v", stderr, err)
				}
			case "error":
				if err == nil {
					t.Fatal("invalid removal succeeded")
				}
			case "cancel":
				if !errors.Is(err, picker.ErrCancelled) {
					t.Fatalf("cancellation error = %v", err)
				}
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != string(original) {
				t.Fatalf("config changed: %q, %v", data, err)
			}
		})
	}
}
