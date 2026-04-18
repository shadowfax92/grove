package cmd

import "testing"

func TestStartBindsMaximizeKey(t *testing.T) {
	logPath := installFakeTmux(t)
	t.Setenv("HOME", t.TempDir())

	err := executeRootForTest(t, "start")
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	log := readFakeTmuxLog(t, logPath)
	assertLogContainsSubstring(t, log, "bind-key -n C-S-M run-shell -b")
	assertLogContainsSubstring(t, log, " maximize \"#{client_name}\" \"#{session_name}\" \"#{pane_id}\"")
}
