package syncfile

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestPlanApplyClassifiesTargetsAndFilters(t *testing.T) {
	manifest := &Manifest{Root: "/code", Groups: map[string][]Entry{
		"clis": {
			{URL: "https://example.com/clone.git", Name: "clone", Branch: "dev"},
			{URL: "https://example.com/present.git", Name: "present"},
			{URL: "https://example.com/occupied.git", Name: "occupied"},
		},
		"hacks": {{URL: "https://example.com/ignored.git", Name: "ignored"}},
	}}
	kinds := map[string]PathKind{
		"/code/clis/clone":    PathMissing,
		"/code/clis/present":  PathGit,
		"/code/clis/occupied": PathOccupied,
	}
	plan, err := PlanApply(manifest, "clis/*", func(path string) (PathKind, error) {
		return kinds[path], nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []ApplyItem{
		{Repo: Repo{Group: "clis", Entry: Entry{URL: "https://example.com/clone.git", Name: "clone", Branch: "dev"}, Path: "/code/clis/clone"}, Action: ApplyClone},
		{Repo: Repo{Group: "clis", Entry: Entry{URL: "https://example.com/occupied.git", Name: "occupied"}, Path: "/code/clis/occupied"}, Action: ApplyFail, Reason: "path exists but is not a git repository"},
		{Repo: Repo{Group: "clis", Entry: Entry{URL: "https://example.com/present.git", Name: "present"}, Path: "/code/clis/present"}, Action: ApplyAlready},
	}
	if !reflect.DeepEqual(plan, want) {
		t.Fatalf("PlanApply() = %#v, want %#v", plan, want)
	}
}

func TestPlanApplyTurnsInspectionErrorsIntoEntryFailures(t *testing.T) {
	manifest := &Manifest{Root: "/code", Groups: map[string][]Entry{"clis": {{URL: "https://example.com/a.git", Name: "a"}}}}
	plan, err := PlanApply(manifest, "", func(string) (PathKind, error) { return 0, errors.New("permission denied") })
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].Action != ApplyFail || !strings.Contains(plan[0].Reason, "permission denied") {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestRunApplyContinuesAfterCloneFailure(t *testing.T) {
	items := []ApplyItem{
		{Repo: Repo{Group: "clis", Entry: Entry{Name: "one"}}, Action: ApplyClone},
		{Repo: Repo{Group: "clis", Entry: Entry{Name: "two"}}, Action: ApplyClone},
		{Repo: Repo{Group: "clis", Entry: Entry{Name: "present"}}, Action: ApplyAlready},
		{Repo: Repo{Group: "clis", Entry: Entry{Name: "occupied"}}, Action: ApplyFail, Reason: "occupied"},
	}
	var mu sync.Mutex
	var called []string
	results := RunApply(items, 2, false, func(repo Repo) error {
		mu.Lock()
		called = append(called, repo.Name)
		mu.Unlock()
		if repo.Name == "one" {
			return errors.New("clone failed")
		}
		return nil
	})
	if len(called) != 2 {
		t.Fatalf("clone calls = %v", called)
	}
	wantStatuses := []ApplyResultStatus{ApplyFailed, ApplyCloned, ApplyPresent, ApplyFailed}
	for i, want := range wantStatuses {
		if results[i].Status != want {
			t.Fatalf("result %d status = %s, want %s", i, results[i].Status, want)
		}
	}
}

func TestRunApplyDryRunNeverClones(t *testing.T) {
	items := []ApplyItem{{Repo: Repo{Group: "clis", Entry: Entry{Name: "one"}}, Action: ApplyClone}}
	results := RunApply(items, 4, true, func(Repo) error {
		t.Fatal("clone called during dry-run")
		return nil
	})
	if results[0].Status != ApplyPlanned {
		t.Fatalf("status = %s, want planned", results[0].Status)
	}
}

func TestCloneArgsIncludesOptionalBranch(t *testing.T) {
	tests := []struct {
		name string
		repo Repo
		want []string
	}{
		{"remote head", Repo{Entry: Entry{URL: "u"}, Path: "/target"}, []string{"clone", "u", "/target"}},
		{"explicit branch", Repo{Entry: Entry{URL: "u", Branch: "dev"}, Path: "/target"}, []string{"clone", "-b", "dev", "u", "/target"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CloneArgs(tt.repo); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("CloneArgs() = %v, want %v", got, tt.want)
			}
		})
	}
}
