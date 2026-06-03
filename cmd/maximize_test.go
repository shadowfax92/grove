package cmd

import (
	"testing"
)

func TestShadowMaximizeOpensCenteredPopup(t *testing.T) {
	logPath := installFakeTmux(t)
	t.Setenv("HOME", t.TempDir())

	err := executeRootForTest(t, "maximize", "fallback-client", "gs/vim/7", "%99")
	if err != nil {
		t.Fatalf("shadow maximize: %v", err)
	}

	log := readFakeTmuxLog(t, logPath)
	assertLogContains(t, log, "display-popup -C -c popup-client")
	assertLogContains(t, log, "split-window -h -b -t %99 -p 18 -c /tmp/grove-test -P -F #{pane_id} while :; do sleep 3600; done")
	assertLogContains(t, log, "split-window -h -t %99 -p 22 -c /tmp/grove-test -P -F #{pane_id} while :; do sleep 3600; done")
	assertLogContains(t, log, "set-option -t gs/vim/7 @shadow_popup_left_gutter %43")
	assertLogContains(t, log, "set-option -t gs/vim/7 @shadow_popup_right_gutter %44")
	assertLogContains(t, log, "select-pane -t %99")
	assertLogContains(t, log, "set-option -t gs/vim/7 @shadow_popup_mode maximized")
	assertLogContains(t, log, "display-popup -c popup-client -w 100% -h 100% -E exec tmux attach-session -t '=gs/vim/7'")
}

func TestShadowMaximizeRestoresNormalPopupSize(t *testing.T) {
	logPath := installFakeTmux(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TMUX_FAKE_POPUP_MODE", shadowPopupModeMaximized)

	err := executeRootForTest(t, "maximize", "fallback-client", "gs/vim/7", "%99")
	if err != nil {
		t.Fatalf("shadow maximize restore: %v", err)
	}

	log := readFakeTmuxLog(t, logPath)
	assertLogContains(t, log, "kill-pane -t %43")
	assertLogContains(t, log, "kill-pane -t %44")
	assertLogContains(t, log, "set-option -t gs/vim/7 @shadow_popup_left_gutter ")
	assertLogContains(t, log, "set-option -t gs/vim/7 @shadow_popup_right_gutter ")
	assertLogContains(t, log, "set-option -t gs/vim/7 @shadow_popup_mode normal")
	// On a 512-col / 100-row client (fake), target_cols=320 / target_rows=70 win.
	assertLogContains(t, log, "display-popup -c popup-client -w 320 -h 70 -E exec tmux attach-session -t '=gs/vim/7'")
}

func TestNormalPaneMaximizeSwapsPaneIntoCenteredPopup(t *testing.T) {
	logPath := installFakeTmux(t)
	t.Setenv("HOME", t.TempDir())

	err := executeRootForTest(t, "maximize", "main-client", "work", "%7")
	if err != nil {
		t.Fatalf("normal pane maximize: %v", err)
	}

	log := readFakeTmuxLog(t, logPath)
	assertLogContains(t, log, "new-session -d -s gm/7 -c /tmp/grove-test while :; do sleep 3600; done")
	assertLogContains(t, log, "split-window -h -b -t %42 -p 18 -c /tmp/grove-test -P -F #{pane_id} while :; do sleep 3600; done")
	assertLogContains(t, log, "split-window -h -t %42 -p 22 -c /tmp/grove-test -P -F #{pane_id} while :; do sleep 3600; done")
	assertLogContains(t, log, "select-pane -t %42")
	assertLogContains(t, log, "set-option -t gm/7 @maximize_origin_pane %7")
	assertLogContains(t, log, "set-option -t gm/7 @maximize_placeholder_pane %42")
	assertLogContains(t, log, "set-option -t gm/7 @maximize_popup_client main-client")
	assertLogContains(t, log, "swap-pane -s %7 -t %42")
	assertLogContains(t, log, "select-pane -t %7")
	assertLogContains(t, log, "display-popup -c main-client -w 100% -h 100% -E exec tmux attach-session -t '=gm/7'")
}

func TestNormalPaneMaximizeRestoreSwapsPaneBack(t *testing.T) {
	logPath := installFakeTmux(t)
	t.Setenv("HOME", t.TempDir())

	err := executeRootForTest(t, "maximize", "fallback-client", "gm/7", "%7")
	if err != nil {
		t.Fatalf("normal pane maximize restore: %v", err)
	}

	log := readFakeTmuxLog(t, logPath)
	assertLogContains(t, log, "display-popup -C -c main-client")
	assertLogContains(t, log, "swap-pane -s %7 -t %42")
	assertLogContains(t, log, "select-pane -t %7")
	assertLogContains(t, log, "kill-session -t =gm/7")
	assertLogOrder(t, log, "swap-pane -s %7 -t %42", "display-popup -C -c main-client")
	assertLogOrder(t, log, "select-pane -t %7", "display-popup -C -c main-client")
}

func TestNormalPaneMaximizeRestoreDoesNotKillRealPaneWhenPlaceholderMissing(t *testing.T) {
	logPath := installFakeTmux(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TMUX_FAKE_MISSING_PANE", "%42")

	err := executeRootForTest(t, "maximize", "fallback-client", "gm/7", "%7")
	if err == nil {
		t.Fatal("normal pane maximize restore should fail when placeholder is missing")
	}

	log := readFakeTmuxLog(t, logPath)
	assertLogNotContains(t, log, "display-popup -C -c main-client")
	assertLogNotContains(t, log, "kill-session -t =gm/7")
}
