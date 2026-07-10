package cmd

import (
	"bytes"
	"strings"
	"testing"

	"grove/internal/syncfile"
)

func TestRenderPullPickerInputShowsBranchAndDirtyMarker(t *testing.T) {
	targets := []syncfile.PullTarget{
		{Repo: syncfile.Repo{Group: "clis", Entry: syncfile.Entry{Name: "clean"}}, State: syncfile.RepoState{Exists: true, Git: true, CurrentBranch: "main"}},
		{Repo: syncfile.Repo{Group: "hacks", Entry: syncfile.Entry{Name: "dirty"}}, State: syncfile.RepoState{Exists: true, Git: true, CurrentBranch: "feat/x", Dirty: true}},
	}
	lookup := make(map[string]syncfile.PullTarget)
	input := renderPullPickerInput(targets, lookup)
	for _, want := range []string{"clis/clean", "main", "hacks/dirty", "feat/x", "!"} {
		if !strings.Contains(input, want) {
			t.Fatalf("picker input missing %q: %q", want, input)
		}
	}
	if len(lookup) != 2 {
		t.Fatalf("lookup size = %d", len(lookup))
	}
}

func TestWritePullSummaryGroupsEveryOutcome(t *testing.T) {
	results := []syncfile.PullResult{
		{Repo: syncfile.Repo{Group: "clis", Entry: syncfile.Entry{Name: "updated"}}, Status: syncfile.PullUpdated, Reason: "main → abc123"},
		{Repo: syncfile.Repo{Group: "clis", Entry: syncfile.Entry{Name: "current"}}, Status: syncfile.PullCurrent, Reason: "main"},
		{Repo: syncfile.Repo{Group: "clis", Entry: syncfile.Entry{Name: "missing"}}, Status: syncfile.PullSkipped, Reason: "missing — run grove sync"},
		{Repo: syncfile.Repo{Group: "clis", Entry: syncfile.Entry{Name: "dirty"}}, Status: syncfile.PullFailed, Reason: "uncommitted changes"},
	}
	var out bytes.Buffer
	counts := writePullSummary(&out, results)
	if counts.Updated != 1 || counts.Current != 1 || counts.Skipped != 1 || counts.Failed != 1 {
		t.Fatalf("counts = %#v", counts)
	}
	for _, want := range []string{"Updated (1)", "Already current (1)", "Skipped (1)", "Failed (1)", "clis/dirty: uncommitted changes"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("summary missing %q:\n%s", want, out.String())
		}
	}
}
