package cmd

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"grove/internal/picker"
)

func TestMain(m *testing.M) {
	// Command tests create disposable repositories, so they must never publish
	// those paths to the developer's live pane. Reporting tests opt in explicitly.
	os.Setenv("HERDR_ENV", "0")
	os.Exit(m.Run())
}

func TestWorktreeCommandsReportCallingPane(t *testing.T) {
	for _, test := range []struct {
		name  string
		args  []string
		new   bool
		reuse bool
	}{
		{name: "root selector", args: []string{"."}},
		{name: "cd selector", args: []string{"cd", "."}},
		{name: "root picker"},
		{name: "cd picker", args: []string{"cd"}},
		{name: "new", args: []string{"new", "feat/report"}, new: true},
		{name: "new reuse", args: []string{"new", "feat/report"}, new: true, reuse: true},
		{name: "json cd", args: []string{"--json", "cd", "."}},
		{name: "json new", args: []string{"--json", "new", "feat/report"}, new: true},
		{name: "null cd", args: []string{"-0", "cd", "."}},
		{name: "null new", args: []string{"--null", "new", "feat/report"}, new: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := initV2Repo(t)
			writeV2Config(t, repo, "")
			path := canonicalV2Path(t, repo)
			if test.new {
				path = filepath.Join(path, ".wt", "feat", "report")
			}
			if test.reuse {
				runV2Git(t, repo, "worktree", "add", "-b", "feat/report", path)
			}
			report := fakePaneReporter(t)
			root := newRootCommand(commandDependencies{
				getwd:       func() (string, error) { return repo, nil },
				interactive: func() bool { return true },
				pick:        func(string, []picker.Item) (string, error) { return path, nil },
			})
			stdout, stderr, err := executeV2(root, test.args...)
			if err != nil || stderr != "" {
				t.Fatalf("command returned %v, stderr = %q", err, stderr)
			}
			if !bytes.Contains([]byte(stdout), []byte(path)) {
				t.Fatalf("stdout = %q, want worktree %q", stdout, path)
			}
			got, err := os.ReadFile(report)
			if err != nil {
				t.Fatalf("worktree was printed but not reported: %v", err)
			}
			handle := fmt.Sprintf("%x", sha256.Sum256([]byte(path)))
			want := "pane\x00report-metadata\x00w1:p1\x00--source\x00grove\x00--token\x00grove_worktree=" + handle + "\x00"
			if string(got) != want {
				t.Fatalf("report args = %q, want %q", got, want)
			}
			content, err := os.ReadFile(filepath.Join(os.Getenv("XDG_STATE_HOME"), "grove", "worktrees", handle))
			if err != nil || string(content) != path+"\n" {
				t.Fatalf("handle contains %q, error = %v", content, err)
			}
		})
	}
}

func TestOtherCommandsAndCanceledNavigationDoNotReport(t *testing.T) {
	for _, test := range []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "list", args: []string{"list"}},
		{name: "json list", args: []string{"--json", "list"}},
		{name: "remove", args: []string{"rm", "feat/removable"}},
		{name: "config path", args: []string{"config", "--path"}},
		{name: "missing selector", args: []string{"cd", "missing"}, wantErr: true},
		{name: "canceled picker", args: []string{"cd"}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := initV2Repo(t)
			writeV2Config(t, repo, "")
			if test.name == "remove" {
				runV2Git(t, repo, "worktree", "add", "-b", "feat/removable", filepath.Join(repo, ".wt", "feat", "removable"))
			}
			report := fakePaneReporter(t)
			root := newRootCommand(commandDependencies{
				getwd:       func() (string, error) { return repo, nil },
				interactive: func() bool { return true },
				pick:        func(string, []picker.Item) (string, error) { return "", picker.ErrCancelled },
			})
			_, _, err := executeV2(root, test.args...)
			if (err != nil) != test.wantErr {
				t.Fatalf("command error = %v, wantErr = %v", err, test.wantErr)
			}
			if _, err := os.Stat(report); !os.IsNotExist(err) {
				t.Fatalf("command unexpectedly reported pane metadata: %v", err)
			}
		})
	}
}

func TestFailedWorktreeOutputDoesNotReport(t *testing.T) {
	for _, args := range [][]string{
		{"cd", "."}, {"-0", "cd", "."}, {"--json", "cd", "."},
		{"new", "feat/report"}, {"--json", "new", "feat/report"},
	} {
		t.Run(args[0]+args[1], func(t *testing.T) {
			repo := initV2Repo(t)
			writeV2Config(t, repo, "")
			report := fakePaneReporter(t)
			root := newRootCommand(commandDependencies{getwd: func() (string, error) { return repo, nil }})
			root.SetOut(failedPaneOutput{})
			root.SetArgs(args)
			if err := root.Execute(); err == nil {
				t.Fatal("expected output failure")
			}
			if _, err := os.Stat(report); !os.IsNotExist(err) {
				t.Fatalf("failed output unexpectedly reported pane metadata: %v", err)
			}
		})
	}
}

type failedPaneOutput struct{}

func (failedPaneOutput) Write([]byte) (int, error) {
	return 0, errors.New("output unavailable")
}

func TestHandleStoreFailurePreservesNavigationOutput(t *testing.T) {
	repo := initV2Repo(t)
	writeV2Config(t, repo, "")
	report := fakePaneReporter(t)
	blockedState := filepath.Join(t.TempDir(), "state-is-a-file")
	if err := os.WriteFile(blockedState, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", blockedState)
	dependencies := commandDependencies{getwd: func() (string, error) { return repo, nil }}
	t.Setenv("HERDR_ENV", "0")
	wantOut, wantErr, wantResult := executeV2(newRootCommand(dependencies), "cd", ".")
	t.Setenv("HERDR_ENV", "1")
	gotOut, gotErr, gotResult := executeV2(newRootCommand(dependencies), "cd", ".")
	if gotOut != wantOut || gotErr != wantErr || gotResult != wantResult {
		t.Fatalf("handle failure changed command output: (%q, %q, %v), want (%q, %q, %v)", gotOut, gotErr, gotResult, wantOut, wantErr, wantResult)
	}
	if _, err := os.Stat(report); !os.IsNotExist(err) {
		t.Fatalf("unresolvable handle was published: %v", err)
	}
}

func fakePaneReporter(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "herdr with spaces")
	report := filepath.Join(dir, "report")
	script := "#!/bin/sh\nprintf '%s\\0' \"$@\" >> \"$GROVE_TEST_REPORT\"\nprintf 'herdr stdout noise\\n'\nprintf 'herdr stderr noise\\n' >&2\nexit 1\n"
	if err := os.WriteFile(binary, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("HERDR_BIN_PATH", binary)
	t.Setenv("GROVE_PANE_REPORT", "")
	t.Setenv("GROVE_TEST_REPORT", report)
	return report
}
