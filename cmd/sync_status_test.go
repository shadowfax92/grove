package cmd

import (
	"bytes"
	"strings"
	"testing"

	"grove/internal/syncfile"
)

func TestWriteSyncStatusGroupsManifestReposAndMarksLocalState(t *testing.T) {
	targets := []syncfile.PullTarget{
		{Repo: syncfile.Repo{Group: ".", Entry: syncfile.Entry{Name: "root"}}, State: syncfile.RepoState{Exists: true, Git: true}},
		{Repo: syncfile.Repo{Group: "clis", Entry: syncfile.Entry{Name: "clean"}}, State: syncfile.RepoState{Exists: true, Git: true}},
		{Repo: syncfile.Repo{Group: "clis", Entry: syncfile.Entry{Name: "dirty"}}, State: syncfile.RepoState{Exists: true, Git: true, Dirty: true}},
		{Repo: syncfile.Repo{Group: "clis", Entry: syncfile.Entry{Name: "missing"}}},
		{Repo: syncfile.Repo{Group: "nested/team", Entry: syncfile.Entry{Name: "occupied"}}, State: syncfile.RepoState{Exists: true}},
	}
	var out bytes.Buffer
	writeSyncStatus(&out, targets)
	for _, want := range []string{".\n  ✓ root", "clis\n  ✓ clean", "  ! dirty", "  ✗ missing", "nested/team\n  ✗ occupied (not a git repository)"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("status missing %q:\n%s", want, out.String())
		}
	}
}
