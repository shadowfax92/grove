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
	assertLogContainsSubstring(t, log, "unbind-key -n C-S-M")
	assertLogContainsSubstring(t, log, "unbind-key -n C-S-Y")
	assertLogContainsSubstring(t, log, "bind-key -n M-y run-shell -b")
	assertLogContainsSubstring(t, log, " maximize \"#{client_name}\" \"#{session_name}\" \"#{pane_id}\"")
}
