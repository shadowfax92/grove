package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"grove/internal/cleanup"
	"grove/internal/config"
	"grove/internal/inuse"
	"grove/internal/picker"
)

func cleanupFixture(t *testing.T) (string, string, commandDependencies) {
	t.Helper()
	repo := initV2Repo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	runV2Git(t, repo, "worktree", "add", "-b", "topic", linked)
	writeV2Config(t, repo, "")
	deps := commandDependencies{getwd: func() (string, error) { return repo, nil }, interactive: func() bool { return false }, inUse: func(string) inuse.Result { return inuse.Result{State: inuse.Free} }, startFootprintRefresh: func([]string) error { return nil }}
	return repo, canonicalV2Path(t, linked), deps
}
func ageCleanupFixture(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-10 * 24 * time.Hour)
	index := v2GitOutput(t, path, "rev-parse", "--path-format=absolute", "--git-path", "index")
	for _, p := range []string{index, filepath.Join(path, "README"), filepath.Join(path, ".git")} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
}
func TestCleanupSelectorsAndPreviewDefersSafetyWalks(t *testing.T) {
	_, linked, deps := cleanupFixture(t)
	ageCleanupFixture(t, linked)
	initV2RepoAt(t, filepath.Join(linked, "nested"))
	// The nested repo would make a full safety walk fail. Preview includes it as
	// provisional and declares both deferred checks, without asking for discard.
	for _, args := range [][]string{
		{"--repo", "app"}, {"--inactive-for", "3d"}, {"--merged"}, {"--older-than", "7d"},
		{"--repo", "app", "--inactive-for", "3d", "--merged", "--older-than", "7d"},
	} {
		// A nested untracked repo makes activity recent; only age its visible files
		// and index after creating it, so inactivity still means source activity.
		old := time.Now().Add(-10 * 24 * time.Hour)
		_ = os.Chtimes(filepath.Join(linked, "nested"), old, old)
		command := append([]string{"--json", "cleanup", "--dry-run", "--forge=false"}, args...)
		out, _, err := executeV2(newRootCommand(deps), command...)
		if err != nil {
			t.Fatal(args, err)
		}
		var result removeOutput
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.WouldRemove) != 1 || len(result.WouldRemove[0].Unchecked) != 2 {
			t.Fatalf("%v: %s", args, out)
		}
	}
	_, _, err := executeV2(newRootCommand(deps), "cleanup", "--repo", "unknown", "--dry-run")
	if err == nil {
		t.Fatal("unknown repository accepted")
	}
}
func TestBulkInUseAndUnknownProtection(t *testing.T) {
	for _, state := range []inuse.State{inuse.InUse, inuse.Unknown} {
		t.Run(string(state), func(t *testing.T) {
			_, linked, deps := cleanupFixture(t)
			deps.inUse = func(string) inuse.Result {
				return inuse.Result{State: state, Error: "collector failed", Processes: []inuse.Process{{PID: 123, Command: "worker"}}}
			}
			out, _, err := executeV2(newRootCommand(deps), "--json", "cleanup", "--repo", "app", "--forge=false")
			if state == inuse.Unknown && err == nil {
				t.Fatal("unknown allowed")
			}
			if state == inuse.InUse && err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(linked); err != nil {
				t.Fatal("protected tree removed", out)
			}
			if state == inuse.Unknown {
				_, _, err = executeV2(newRootCommand(deps), "cleanup", "--repo", "app", "--forge=false", "--allow-unknown-in-use")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(linked); !os.IsNotExist(err) {
					t.Fatal("explicit override ignored")
				}
			}
		})
	}
}
func TestExactInUseWarnsWithProcessNames(t *testing.T) {
	_, linked, deps := cleanupFixture(t)
	deps.inUse = func(string) inuse.Result {
		return inuse.Result{State: inuse.InUse, Processes: []inuse.Process{{PID: 123, Command: "worker"}}}
	}
	_, stderr, err := executeV2(newRootCommand(deps), "rm", "topic")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "worker (pid 123)") {
		t.Fatal(stderr)
	}
	if _, err := os.Stat(linked); !os.IsNotExist(err) {
		t.Fatal("exact selector ignored")
	}
}
func TestGuidedDirtyConfirmation(t *testing.T) {
	for _, accept := range []bool{false, true} {
		t.Run(map[bool]string{false: "decline", true: "accept"}[accept], func(t *testing.T) {
			_, linked, deps := cleanupFixture(t)
			if err := os.WriteFile(filepath.Join(linked, "README"), []byte("uncommitted"), 0600); err != nil {
				t.Fatal(err)
			}
			deps.interactive = func() bool { return true }
			calls := 0
			deps.pickMany = func(prompt string, items []picker.Item) ([]string, error) {
				calls++
				if calls == 1 {
					if prompt != "projects > " || !strings.Contains(items[0].Label, "worktrees") {
						t.Fatal(prompt, items)
					}
					return []string{items[0].Key}, nil
				}
				if !strings.Contains(items[0].Label, "uncommitted unchecked") || !strings.Contains(items[0].Label, "last active") {
					t.Fatal(items)
				}
				return []string{linked}, nil
			}
			confirmed := false
			deps.confirm = func(prompt string) (bool, error) {
				confirmed = true
				if !strings.Contains(prompt, "1 with uncommitted files") || !strings.Contains(prompt, "discards") {
					t.Fatal(prompt)
				}
				return accept, nil
			}
			_, _, err := executeV2(newRootCommand(deps), "cleanup", "--forge=false")
			if !confirmed {
				t.Fatal("no summary confirmation")
			}
			if !accept && !errors.Is(err, picker.ErrCancelled) {
				t.Fatal(err)
			}
			if accept && err != nil {
				t.Fatal(err)
			}
			_, statErr := os.Stat(linked)
			if accept != os.IsNotExist(statErr) {
				t.Fatal("confirmation not honored", statErr)
			}
		})
	}
}
func TestSweepConditionsAndCallerProtection(t *testing.T) {
	for _, condition := range []string{"eligible", "dirty", "in-use", "unknown", "recent", "locked", "current", "caller", "unmerged", "moved-tip", "disabled"} {
		t.Run(condition, func(t *testing.T) {
			repo, linked, deps := cleanupFixture(t)
			ageCleanupFixture(t, linked)
			caller := ""
			enabled := true
			switch condition {
			case "dirty":
				_ = os.WriteFile(filepath.Join(linked, "README"), []byte("dirty"), 0600)
			case "in-use":
				deps.inUse = func(string) inuse.Result { return inuse.Result{State: inuse.InUse} }
			case "unknown":
				deps.inUse = func(string) inuse.Result { return inuse.Result{State: inuse.Unknown} }
			case "recent":
				now := time.Now()
				_ = os.Chtimes(filepath.Join(linked, "README"), now, now)
			case "locked":
				runV2Git(t, repo, "worktree", "lock", linked)
			case "current":
				deps.getwd = func() (string, error) { return linked, nil }
			case "caller":
				caller = linked
			case "unmerged":
				_ = os.WriteFile(filepath.Join(linked, "new"), []byte("new"), 0600)
				runV2Git(t, linked, "add", "new")
				runV2Git(t, linked, "commit", "-m", "new")
				ageCleanupFixture(t, linked)
			case "moved-tip": // Change the tip after collection, when authoritative checks start.
				calls := 0
				deps.inUse = func(string) inuse.Result {
					calls++
					if calls == 2 {
						_ = os.WriteFile(filepath.Join(linked, "new"), []byte("new"), 0600)
						runV2Git(t, linked, "add", "new")
						runV2Git(t, linked, "commit", "-m", "new")
						ageCleanupFixture(t, linked)
					}
					return inuse.Result{State: inuse.Free}
				}
			case "disabled":
				enabled = false
			}
			path, _ := config.DefaultConfigPath()
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.WriteString("cleanup:\n  enabled: " + map[bool]string{true: "true", false: "false"}[enabled] + "\n  grace_period: 3d\n")
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"cleanup", "--sweep", "--automatic", "--forge=false"}
			if caller != "" {
				args = append(args, "--caller-directory", caller)
			}
			_, _, _ = executeV2(newRootCommand(deps), args...)
			_, statErr := os.Stat(linked)
			if (condition == "eligible") != os.IsNotExist(statErr) {
				t.Fatalf("condition=%s tree state=%v", condition, statErr)
			}
			runV2Git(t, repo, "show-ref", "--verify", "refs/heads/topic")
			schedule, _ := cleanup.DefaultSchedule()
			log, err := os.ReadFile(filepath.Join(schedule.Directory, "sweeps.jsonl"))
			if condition != "disabled" && err != nil {
				t.Fatal(err)
			}
			if condition == "eligible" && !strings.Contains(string(log), "ancestor of default branch") {
				t.Fatal(string(log))
			}
		})
	}
}
func TestAutomaticLaunchIsOptInAndThrottled(t *testing.T) {
	repo, _, deps := cleanupFixture(t)
	deps.interactive = func() bool { return false }
	launched := 0
	deps.startAutomaticCleanup = func(args []string) error {
		launched++
		if !strings.Contains(strings.Join(args, " "), "--caller-directory") {
			t.Fatal(args)
		}
		return nil
	}
	_, _, err := executeV2(newRootCommand(deps), "list")
	if err != nil || launched != 0 {
		t.Fatal(launched, err)
	}
	path, _ := config.DefaultConfigPath()
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	_, _ = f.WriteString("cleanup:\n  enabled: true\n  interval: 1h\n")
	f.Close()
	for range 3 {
		_, _, err = executeV2(newRootCommand(deps), "list")
		if err != nil {
			t.Fatal(err)
		}
	}
	if launched != 1 {
		t.Fatal("launches", launched)
	}
	_ = repo
}
func TestCleanupCanceledBeforeRemoval(t *testing.T) {
	_, linked, deps := cleanupFixture(t)
	root := newRootCommand(deps)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root.SetContext(ctx)
	_, _, err := executeV2(root, "cleanup", "--repo", "app", "--forge=false")
	if err == nil {
		t.Fatal("cancellation ignored")
	}
	if _, err := os.Stat(linked); err != nil {
		t.Fatal(err)
	}
}
func TestDUColdNeverWaitsForFootprint(t *testing.T) {
	_, _, deps := cleanupFixture(t)
	called := false
	deps.startFootprintRefresh = func([]string) error { called = true; return nil }
	out, stderr, err := executeV2(newRootCommand(deps), "du")
	if err != nil {
		t.Fatal(err)
	}
	if !called || strings.Contains(stderr, "Scanning worktree") || !strings.Contains(out, "size unknown") || strings.Contains(out, "just now ago") {
		t.Fatal(out, stderr)
	}
}

func TestAutomaticDryRunNeverLaunchesOrConsumesNotice(t *testing.T) {
	_, _, deps := cleanupFixture(t)
	path, _ := config.DefaultConfigPath()
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	_, _ = f.WriteString("cleanup:\n  enabled: true\n")
	f.Close()
	deps.interactive = func() bool { return true }
	deps.startAutomaticCleanup = func([]string) error { t.Fatal("dry run launched sweep"); return nil }
	schedule, _ := cleanup.DefaultSchedule()
	if err := schedule.Record(cleanup.SweepReport{Finished: time.Now(), Removed: []cleanup.SweepRemoval{}}); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeV2(newRootCommand(deps), "cleanup", "--sweep", "--dry-run", "--forge=false")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := schedule.Notice(); !ok {
		t.Fatal("dry run consumed notice")
	}
}

func TestBulkUnknownAtFinalCheckFailsBeforeDetach(t *testing.T) {
	_, linked, deps := cleanupFixture(t)
	calls := 0
	deps.inUse = func(string) inuse.Result {
		calls++
		if calls == 1 {
			return inuse.Result{State: inuse.Free}
		}
		return inuse.Result{State: inuse.Unknown}
	}
	_, _, err := executeV2(newRootCommand(deps), "cleanup", "--repo", "app", "--forge=false")
	if err == nil || !strings.Contains(err.Error(), "in-use check unavailable") {
		t.Fatal(err)
	}
	if _, err := os.Stat(linked); err != nil {
		t.Fatal(err)
	}
}

func TestDetachedSweepProtectsLaunchingWorktree(t *testing.T) {
	// Build before HOME isolation; only the fixture binary's commands can remove.
	binary := filepath.Join(t.TempDir(), "grove")
	build := exec.Command("go", "build", "-o", binary, "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	repo, linked, _ := cleanupFixture(t)
	ageCleanupFixture(t, linked)
	path, _ := config.DefaultConfigPath()
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	_, _ = f.WriteString("cleanup:\n  enabled: true\n  grace_period: 3d\n")
	f.Close()
	// -C targets main, while the command is actually launched from a linked tree.
	// The detached child must retain the real cwd even after the parent exits.
	parent := exec.Command(binary, "-C", repo, "list")
	parent.Dir = linked
	if out, err := parent.CombinedOutput(); err != nil {
		t.Fatalf("launch: %v %s", err, out)
	}
	schedule, _ := cleanup.DefaultSchedule()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(schedule.Directory, "last-report.json")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("detached sweep did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(linked); err != nil {
		t.Fatal("launching tree removed", err)
	}
	data, err := os.ReadFile(filepath.Join(schedule.Directory, "sweeps.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var report cleanup.SweepReport
	if err := json.Unmarshal(data, &report); err != nil || report.Error != "" || len(report.Removed) != 0 {
		t.Fatal(string(data), err)
	}
}

func TestCleanupRechecksPolicyInsideDetachGuard(t *testing.T) {
	for _, late := range []string{"process", "tip", "activity"} {
		t.Run(late, func(t *testing.T) {
			repo, linked, deps := cleanupFixture(t)
			ageCleanupFixture(t, linked)
			calls := 0
			deps.inUse = func(string) inuse.Result {
				calls++
				// Collection and whole-selection final check precede the locked guard.
				if calls >= 3 {
					switch late {
					case "process":
						return inuse.Result{State: inuse.InUse, Processes: []inuse.Process{{PID: 1, Command: "late-worker"}}}
					case "tip":
						_ = os.WriteFile(filepath.Join(linked, "new"), []byte("unmerged"), 0600)
						runV2Git(t, linked, "add", "new")
						runV2Git(t, linked, "commit", "-m", "new")
					case "activity":
						now := time.Now()
						_ = os.Chtimes(filepath.Join(linked, "README"), now, now)
					}
				}
				return inuse.Result{State: inuse.Free}
			}
			_, _, err := executeV2(newRootCommand(deps), "cleanup", "--sweep", "--forge=false")
			if err == nil {
				t.Fatal("late policy change allowed")
			}
			if _, err := os.Stat(linked); err != nil {
				t.Fatal("worktree removed", err)
			}
			runV2Git(t, repo, "show-ref", "--verify", "refs/heads/topic")
		})
	}
}
