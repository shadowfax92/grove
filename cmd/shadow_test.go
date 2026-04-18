package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShadowDeleteKillsVimAndShellForCurrentPane(t *testing.T) {
	logPath := installFakeTmux(t)

	err := executeRootForTest(t, "shadow", "delete", "client-1", "work", "%7")
	if err != nil {
		t.Fatalf("shadow delete: %v", err)
	}

	log := readFakeTmuxLog(t, logPath)
	assertLogContains(t, log, "kill-session -t =gs/vim/7")
	assertLogContains(t, log, "kill-session -t =gs/sh/7")
}

func TestShadowDeleteFromShadowSessionClosesPopupAndDeletesParentShadows(t *testing.T) {
	logPath := installFakeTmux(t)

	err := executeRootForTest(t, "shadow", "delete", "fallback-client", "gs/vim/7", "%99")
	if err != nil {
		t.Fatalf("shadow delete from shadow session: %v", err)
	}

	log := readFakeTmuxLog(t, logPath)
	assertLogContains(t, log, "display-popup -C -c popup-client")
	assertLogContains(t, log, "kill-session -t =gs/vim/7")
	assertLogContains(t, log, "kill-session -t =gs/sh/7")
}

func executeRootForTest(t *testing.T, args ...string) error {
	t.Helper()

	rootCmd.SetArgs(args)
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(os.Stdout)
		rootCmd.SetErr(os.Stderr)
	})

	return rootCmd.Execute()
}

func installFakeTmux(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "tmux.log")
	scriptPath := filepath.Join(dir, "tmux")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_FAKE_LOG"

case "$1" in
  has-session|kill-session|display-popup)
    exit 0
    ;;
  show-options)
    case "$5" in
      @shadow_client_name)
        printf '%s\n' 'popup-client'
        ;;
      @shadow_parent_pane)
        printf '%s\n' '%7'
        ;;
    esac
    exit 0
    ;;
esac

exit 0
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}

	t.Setenv("TMUX_FAKE_LOG", logPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func readFakeTmuxLog(t *testing.T, logPath string) []string {
	t.Helper()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake tmux log: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func assertLogContains(t *testing.T, log []string, want string) {
	t.Helper()

	for _, line := range log {
		if line == want {
			return
		}
	}
	t.Fatalf("tmux log missing %q; got:\n%s", want, strings.Join(log, "\n"))
}
