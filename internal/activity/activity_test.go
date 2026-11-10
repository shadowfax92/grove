package activity

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	gitx "grove/internal/git"
)

func git(t *testing.T, path string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = path
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}
func stamp(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "-b", "main")
	git(t, root, "config", "user.name", "Fixture")
	git(t, root, "config", "user.email", "fixture@example.invalid")
	for name, content := range map[string]string{"source": "initial", ".gitignore": "output/\ncontext\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	path := filepath.Join(t.TempDir(), "linked")
	git(t, root, "worktree", "add", "-b", "topic", path)
	index, err := indexPath(path)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	for _, file := range []string{index, filepath.Join(path, "source"), filepath.Join(path, ".gitignore")} {
		stamp(t, file, old)
	}
	return path
}
func TestWorkingFileActivity(t *testing.T) {
	for _, name := range []string{"source", "new file\nwith newline"} {
		t.Run(name, func(t *testing.T) {
			path := fixture(t)
			baseline := Measure(context.Background(), path)
			if baseline.Error != "" {
				t.Fatal(baseline.Error)
			}
			file := filepath.Join(path, name)
			if err := os.WriteFile(file, []byte("edited"), 0600); err != nil {
				t.Fatal(err)
			}
			edited := time.Now().Add(-time.Hour).Truncate(time.Second)
			stamp(t, file, edited)
			got := Measure(context.Background(), path)
			if got.Error != "" || !got.LastActive.Equal(edited) || got.Source != "working_file" {
				t.Fatalf("got %+v want %v", got, edited)
			}
		})
	}
}
func TestIgnoredOutputAndMetadataDoNotMoveActivity(t *testing.T) {
	path := fixture(t)
	baseline := Measure(context.Background(), path)
	if err := os.Mkdir(filepath.Join(path, "output"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "output", "build"), []byte("build"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(path, "context")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "shared"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	index, _ := indexPath(path)
	admin := filepath.Dir(index)
	for _, name := range []string{"HEAD", "logs/HEAD"} {
		stamp(t, filepath.Join(admin, name), time.Now())
	}
	got := Measure(context.Background(), path)
	if got.Error != "" || !got.LastActive.Equal(baseline.LastActive) {
		t.Fatalf("ignored/metadata activity moved: %+v -> %+v", baseline, got)
	}
}
func TestStatusDoesNotRefreshIndexOrActivity(t *testing.T) {
	t.Setenv("GIT_OPTIONAL_LOCKS", "1") // Grove must override even an inherited opt-in.
	path := fixture(t)
	index, _ := indexPath(path)
	before, _ := os.Stat(index)
	baseline := Measure(context.Background(), path)
	repository := &gitx.Repository{MainPath: path}
	if _, err := repository.Dirty(path); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(index)
	got := Measure(context.Background(), path)
	if !after.ModTime().Equal(before.ModTime()) || !got.LastActive.Equal(baseline.LastActive) {
		t.Fatalf("read moved signal: index %v -> %v, activity %+v -> %+v", before.ModTime(), after.ModTime(), baseline, got)
	}
}
func TestIndexAndUnknown(t *testing.T) {
	path := fixture(t)
	index, _ := indexPath(path)
	now := time.Now().Truncate(time.Second)
	stamp(t, index, now)
	results := Scan(context.Background(), []string{path, filepath.Join(path, "missing")}, 2)
	if got := results[path]; got.Error != "" || !got.LastActive.Equal(now) || got.Source != "index" {
		t.Fatalf("index result %+v", got)
	}
	if got := results[filepath.Join(path, "missing")]; got.Error == "" {
		t.Fatalf("missing must be unknown: %+v", got)
	}
}
