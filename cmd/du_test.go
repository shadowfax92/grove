package cmd

import (
	"encoding/json"
	"grove/internal/footprint"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDUJSONCacheAndRefresh(t *testing.T) {
	repo := initV2Repo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	runV2Git(t, repo, "worktree", "add", "-b", "feat/example", linked)
	writeV2Config(t, repo, "")
	if err := os.WriteFile(filepath.Join(linked, "object"), make([]byte, 8192), 0644); err != nil {
		t.Fatal(err)
	}
	root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repo, nil }})
	stdout, stderr, err := executeV2(root, "--json", "du", "app", "--private-size=false")
	if err != nil {
		t.Fatal(err)
	}
	var doc duDocument
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Version != 1 || doc.Worktrees != 1 || doc.Bytes < 8192 || len(doc.Repositories) != 1 {
		t.Fatalf("document = %#v", doc)
	}
	if !strings.Contains(stderr, "Scanning worktree footprints") {
		t.Fatalf("no cold progress: %s", stderr)
	}
	rows := doc.Repositories[0].Worktrees
	if len(rows) != 2 || rows[0].Main || rows[0].Footprint == nil || rows[1].Footprint != nil {
		t.Fatalf("rows = %#v", rows)
	}
	first := rows[0].Footprint.ScannedAt
	launched := false
	deps := commandDependencies{getwd: func() (string, error) { return repo, nil }, startFootprintRefresh: func(args []string) error { launched = true; return nil }}
	stdout, stderr, err = executeV2(newRootCommand(deps), "--json", "du", "--private-size=false")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Repositories[0].Worktrees[0].Footprint.ScannedAt.Equal(first) || strings.Contains(stderr, "Scanning") || launched {
		t.Fatalf("warm read rescanned: %s", stderr)
	}
	stdout, stderr, err = executeV2(newRootCommand(deps), "--json", "du", "--private-size=false", "--max-age=0s")
	if err != nil {
		t.Fatal(err)
	}
	if !launched || !strings.Contains(stderr, "background") {
		t.Fatalf("stale read did not detach: %s", stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Repositories[0].Worktrees[0].Stale {
		t.Fatal("stale cache not labeled")
	}
	stdout, _, err = executeV2(newRootCommand(deps), "--json", "du", "--private-size=false", "--refresh")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Repositories[0].Worktrees[0].Footprint.ScannedAt.After(first) {
		t.Fatal("refresh reused old scan")
	}
}

func TestDUHumanTotalsExcludeNestedMainAndScope(t *testing.T) {
	repo := initV2Repo(t)
	linked := filepath.Join(repo, ".wt", "feature")
	runV2Git(t, repo, "worktree", "add", "-b", "feat/example", linked)
	other := initV2Repo(t)
	writeV2Config(t, repo, "  - path: "+other+"\n    name: second\n")
	stdout, _, err := executeV2(newRootCommand(commandDependencies{getwd: func() (string, error) { return repo, nil }}), "du", "app", "--include-main", "--private-size=false")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, "second") || strings.Contains(stdout, "  main") || !strings.Contains(stdout, "1 linked worktrees (main excluded)") || !strings.Contains(stdout, "created") {
		t.Fatalf("human output = %s", stdout)
	}
	_, _, err = executeV2(newRootCommand(commandDependencies{getwd: func() (string, error) { return repo, nil }}), "du", "unknown")
	if err == nil {
		t.Fatal("unknown repository accepted")
	}
}

func TestDUTotalsAndOrdering(t *testing.T) {
	now := time.Now()
	doc := duDocument{Repositories: []*duRepository{
		{Name: "small", Worktrees: []*duWorktree{{Path: "a", Footprint: footprintForTest(4, now)}, {Main: true, Footprint: footprintForTest(1000, now)}}},
		{Name: "big", Worktrees: []*duWorktree{{Footprint: footprintForTest(3, now)}, {Footprint: footprintForTest(9, now)}}},
	}}
	finishDUTotals(&doc)
	if doc.Bytes != 16 || doc.Worktrees != 3 || doc.Repositories[0].Name != "big" || duBytes(doc.Repositories[0].Worktrees[0]) != 9 {
		t.Fatalf("totals = %#v", doc)
	}
}

func footprintForTest(size int64, now time.Time) *footprint.Entry {
	return &footprint.Entry{Bytes: size, ScannedAt: now}
}

func TestDURefreshPreservesDashPrefixedRepository(t *testing.T) {
	repo := initV2Repo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	runV2Git(t, repo, "worktree", "add", "-b", "feature", linked)
	writeV2Config(t, "", "  - path: "+repo+"\n    name: -example\n")
	deps := commandDependencies{getwd: func() (string, error) { return repo, nil }}
	if _, _, err := executeV2(newRootCommand(deps), "du", "--", "-example"); err != nil {
		t.Fatal(err)
	}
	var forwarded []string
	deps.startFootprintRefresh = func(args []string) error { forwarded = args; return nil }
	if _, _, err := executeV2(newRootCommand(deps), "du", "--max-age=0s", "--", "-example"); err != nil {
		t.Fatal(err)
	}
	if len(forwarded) < 2 || forwarded[len(forwarded)-2] != "--" || forwarded[len(forwarded)-1] != "-example" {
		t.Fatalf("repository alias became flags: %q", forwarded)
	}
}
