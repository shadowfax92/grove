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

func TestShadowToggleCleansStaleFocusGutters(t *testing.T) {
	logPath := installFakeTmux(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TMUX_FAKE_POPUP_MODE", shadowPopupModeMaximized)

	err := executeRootForTest(t, "shadow", "toggle", "vim", "client-1", "work", "%7")
	if err != nil {
		t.Fatalf("shadow toggle: %v", err)
	}

	log := readFakeTmuxLog(t, logPath)
	assertLogContains(t, log, "kill-pane -t %43")
	assertLogContains(t, log, "kill-pane -t %44")
	assertLogContains(t, log, "set-option -t gs/vim/7 @shadow_popup_left_gutter ")
	assertLogContains(t, log, "set-option -t gs/vim/7 @shadow_popup_right_gutter ")
	assertLogContains(t, log, "set-option -t gs/vim/7 @shadow_popup_mode normal")
	assertLogContains(t, log, "display-popup -c client-1 -w 320 -h 70 -E exec tmux attach-session -t '=gs/vim/7'")
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
  has-session)
    if [ "$3" = "=gm/7" ] && [ "$TMUX_FAKE_MAXIMIZE_EXISTS" != "1" ]; then
      exit 1
    fi
    exit 0
    ;;
  kill-session|display-popup|new-session|set-option|swap-pane|select-pane|kill-pane)
    exit 0
    ;;
  split-window)
    for arg in "$@"; do
      if [ "$arg" = "-b" ]; then
        printf '%s\n' '%43'
        exit 0
      fi
    done
    printf '%s\n' '%44'
    exit 0
    ;;
  list-panes)
    printf '%s\n' '%42'
    exit 0
    ;;
  display-message)
    case "$5" in
      '#{pane_current_path}')
        printf '%s\n' '/tmp/grove-test'
        ;;
      '#{client_width} #{client_height}')
        printf '%s\n' "${TMUX_FAKE_CLIENT_COLS:-512} ${TMUX_FAKE_CLIENT_ROWS:-100}"
        ;;
    esac
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
      @shadow_popup_mode)
        printf '%s\n' "$TMUX_FAKE_POPUP_MODE"
        ;;
      @shadow_cwd)
        printf '%s\n' '/tmp/grove-test'
        ;;
      @shadow_env_version)
        printf '%s\n' '1'
        ;;
      @shadow_popup_left_gutter)
        if [ "$TMUX_FAKE_POPUP_MODE" = "maximized" ]; then
          printf '%s\n' '%43'
        fi
        ;;
      @shadow_popup_right_gutter)
        if [ "$TMUX_FAKE_POPUP_MODE" = "maximized" ]; then
          printf '%s\n' '%44'
        fi
        ;;
      @maximize_origin_pane)
        printf '%s\n' '%7'
        ;;
      @maximize_placeholder_pane)
        printf '%s\n' '%42'
        ;;
      @maximize_popup_client)
        printf '%s\n' 'main-client'
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

func assertLogContainsSubstring(t *testing.T, log []string, want string) {
	t.Helper()

	for _, line := range log {
		if strings.Contains(line, want) {
			return
		}
	}
	t.Fatalf("tmux log missing substring %q; got:\n%s", want, strings.Join(log, "\n"))
}
