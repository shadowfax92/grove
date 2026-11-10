package cleanup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"grove/internal/activity"
	"grove/internal/catalog"
	gitx "grove/internal/git"
	"grove/internal/inuse"
	"grove/internal/inventory"
	"grove/internal/merged"
)

func TestSelectionAndPreselection(t *testing.T) {
	now := time.Now()
	inv := &inventory.Inventory{}
	base := Row{Entry: &inventory.Entry{Repository: &catalog.Repository{Name: "app"}, Worktree: gitx.WorktreeInfo{Path: "/fixture/worktree", Branch: "topic"}}, CreatedAt: now.Add(-10 * 24 * time.Hour), Activity: activity.Result{LastActive: now.Add(-5 * 24 * time.Hour)}, InUse: inuse.Result{State: inuse.Free}, Merged: merged.Result{Merged: true}}
	cases := []struct {
		name          string
		change        func(*Row)
		pre, selected bool
		error         bool
	}{
		{"idle merged", func(*Row) {}, true, true, false},
		{"in use", func(r *Row) { r.InUse.State = inuse.InUse }, false, false, false},
		{"unknown", func(r *Row) { r.InUse.State = inuse.Unknown }, false, false, true},
		{"locked", func(r *Row) { e := *r.Entry; e.Worktree.Locked = true; r.Entry = &e }, false, false, false},
		{"main", func(r *Row) { e := *r.Entry; e.Worktree.Main = true; r.Entry = &e }, false, false, false},
		{"recent unmerged", func(r *Row) { r.Activity.LastActive = now; r.Merged.Merged = false }, false, false, false},
		{"activity error", func(r *Row) { r.Activity.Error = "unavailable"; r.Merged.Merged = false }, false, false, false},
		{"recent merged", func(r *Row) { r.Activity.LastActive = now }, true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.change(&r)
			if got := r.Preselected(now, 72*time.Hour, "", inv); got != tc.pre {
				t.Fatalf("preselected=%v", got)
			}
			selected, err := Select([]Row{r}, inv, now, Selection{InactiveFor: 72 * time.Hour, Merged: true})
			if (err != nil) != tc.error || (len(selected) > 0) != tc.selected {
				t.Fatalf("selected=%v err=%v", selected, err)
			}
		})
	}
	if base.Preselected(now, time.Hour, base.Entry.Worktree.Path, inv) {
		t.Fatal("current preselected")
	}
	unknown := base
	unknown.InUse.State = inuse.Unknown
	got, err := Select([]Row{unknown}, inv, now, Selection{AllowUnknown: true})
	if err != nil || len(got) != 1 {
		t.Fatal(got, err)
	}
	live := base
	live.InUse.State = inuse.InUse
	got, err = Select([]Row{live}, inv, now, Selection{AllowUnknown: true})
	if err != nil || len(got) != 0 {
		t.Fatal("override selected known live worktree")
	}
	got, err = Select([]Row{base}, inv, now, Selection{OlderThan: 11 * 24 * time.Hour})
	if err != nil || len(got) != 0 {
		t.Fatal("creation selector ignored")
	}
}
func TestScheduleIntervalLeaseAndLog(t *testing.T) {
	s := &Schedule{Directory: t.TempDir()}
	now := time.Now()
	wins := make(chan bool, 12)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.Claim(now, time.Hour)
			if err != nil {
				t.Error(err)
			}
			wins <- ok
		}()
	}
	wg.Wait()
	close(wins)
	n := 0
	for ok := range wins {
		if ok {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("launches=%d", n)
	}
	if ok, err := s.Claim(now.Add(time.Hour), time.Hour); err != nil || !ok {
		t.Fatal(ok, err)
	}
	release, held, err := s.Lease()
	if err != nil || !held {
		t.Fatal(held, err)
	}
	_, held, err = s.Lease()
	if err != nil || held {
		t.Fatal("second worker held sweep lease", err)
	}
	release()
	bytes := int64(42)
	report := SweepReport{Started: now, Finished: now.Add(time.Second), Removed: []SweepRemoval{{Selector: "app:topic", Bytes: &bytes, Reason: "merged pull request at current tip"}}}
	if err := s.Record(report); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(s.Directory, "sweeps.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var logged SweepReport
	if err := json.Unmarshal(data, &logged); err != nil || len(logged.Removed) != 1 || *logged.Removed[0].Bytes != 42 {
		t.Fatal(string(data), err)
	}
	if _, ok := s.Notice(); !ok {
		t.Fatal("missing notice")
	}
	if _, ok := s.Notice(); ok {
		t.Fatal("duplicate notice")
	}
}
