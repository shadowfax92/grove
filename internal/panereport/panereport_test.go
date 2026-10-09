package panereport

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorktreeReportingRequiresCallerOptIn(t *testing.T) {
	for _, test := range []struct {
		name, env, pane, report string
	}{
		{name: "outside Herdr", pane: "w1:p1"},
		{name: "Herdr disabled", env: "0", pane: "w1:p1"},
		{name: "exact environment gate", env: "true", pane: "w1:p1"},
		{name: "no caller pane", env: "1"},
		{name: "launcher opted out", env: "1", pane: "w1:p1", report: "0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary, record := reporterScript(t, "printf invoked > \"$GROVE_TEST_REPORT\"\n")
			t.Setenv("HERDR_ENV", test.env)
			t.Setenv("HERDR_PANE_ID", test.pane)
			t.Setenv("GROVE_PANE_REPORT", test.report)
			t.Setenv("HERDR_BIN_PATH", binary)
			Worktree(context.Background(), "/worktree")
			if _, err := os.Stat(record); !os.IsNotExist(err) {
				t.Fatalf("reporter ran despite caller gate: %v", err)
			}
			if _, err := os.Stat(filepath.Join(os.Getenv("XDG_STATE_HOME"), "grove")); !os.IsNotExist(err) {
				t.Fatalf("disabled reporting unexpectedly wrote handle state: %v", err)
			}
		})
	}
}

func TestWorktreeReportingUsesPathFallbackAndOpaquePaths(t *testing.T) {
	// Resolve the handle inside the reporter to prove the file exists before
	// publication, including for roots that cannot safely be split into lines.
	binary, record := reporterScript(t, "handle=${7#grove_worktree=}\ncat \"$XDG_STATE_HOME/grove/worktrees/$handle\" > \"$GROVE_TEST_RESOLVED\" || exit 1\nprintf '%s\\0' \"$@\" > \"$GROVE_TEST_REPORT\"\n")
	resolved := filepath.Join(t.TempDir(), "resolved")
	t.Setenv("GROVE_TEST_RESOLVED", resolved)
	t.Setenv("HERDR_BIN_PATH", "")
	t.Setenv("PATH", filepath.Dir(binary)+string(os.PathListSeparator)+"/bin")
	Worktree(context.Background(), "/worktree with spaces/line\nbreak=$(literal)")
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	want := "pane\x00report-metadata\x00w1:p1\x00--source\x00grove\x00--token\x00grove_worktree=c8e1187faac5ced5f70ec0902fa30ae3bd9e46e5d66a29cea406c56cd99ba78f\x00"
	if string(got) != want {
		t.Fatalf("report arguments = %q, want %q", got, want)
	}
	content, err := os.ReadFile(resolved)
	if err != nil || string(content) != "/worktree with spaces/line\nbreak=$(literal)\n" {
		t.Fatalf("published handle resolved to %q, error = %v", content, err)
	}
}

func TestWorktreeReportingUsesHomeStateFallback(t *testing.T) {
	binary, record := reporterScript(t, "printf '%s' \"${7#grove_worktree=}\" > \"$GROVE_TEST_REPORT\"\n")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HERDR_BIN_PATH", binary)
	Worktree(context.Background(), "/worktree")
	handle, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(home, ".local", "state", "grove", "worktrees", string(handle)))
	if err != nil || string(content) != "/worktree\n" {
		t.Fatalf("default handle contains %q, error = %v", content, err)
	}
}

func TestFailedHandleCommitDoesNotPublish(t *testing.T) {
	for _, failure := range []string{"state is a file", "relative state root", "rename blocked"} {
		t.Run(failure, func(t *testing.T) {
			binary, record := reporterScript(t, "printf invoked > \"$GROVE_TEST_REPORT\"\n")
			t.Setenv("HERDR_BIN_PATH", binary)
			state := os.Getenv("XDG_STATE_HOME")
			switch failure {
			case "state is a file":
				if err := os.WriteFile(state, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "relative state root":
				t.Setenv("XDG_STATE_HOME", "relative-state")
			case "rename blocked":
				// A directory at the final filename lets the temporary write succeed
				// but rejects its commit. Publication must still be skipped.
				if err := os.MkdirAll(filepath.Join(state, "grove", "worktrees", "45d9b1d347d496cecef4c2008975d7b9247fc8a133a4ba98252f6e0086dc4ba1"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			Worktree(context.Background(), "/worktree")
			if _, err := os.Stat(record); !os.IsNotExist(err) {
				t.Fatalf("reporter ran despite failed handle commit: %v", err)
			}
			if failure == "rename blocked" {
				entries, err := os.ReadDir(filepath.Join(state, "grove", "worktrees"))
				if err != nil || len(entries) != 1 || !entries[0].IsDir() {
					t.Fatalf("temporary handle was not cleaned up: %v, %v", entries, err)
				}
			}
		})
	}
}

func TestWorktreeReportingHasBoundedLifetime(t *testing.T) {
	binary, _ := reporterScript(t, "exec /bin/sleep 30\n")
	t.Setenv("HERDR_BIN_PATH", binary)
	started := time.Now()
	Worktree(context.Background(), "/worktree")
	if elapsed := time.Since(started); elapsed < 800*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("reporting took %s, want approximately one second", elapsed)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started = time.Now()
	Worktree(ctx, "/worktree")
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("reporting ignored command cancellation for %s", elapsed)
	}
}

func reporterScript(t *testing.T, body string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	binary, record := filepath.Join(dir, "herdr"), filepath.Join(dir, "report")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"+body), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("GROVE_PANE_REPORT", "")
	t.Setenv("GROVE_TEST_REPORT", record)
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	return binary, record
}
