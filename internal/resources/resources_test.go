package resources

import "testing"

func TestBuildWindowUsagesAggregatesPaneProcessTrees(t *testing.T) {
	panes := []Pane{
		{Target: "g/app:0.0", Session: "g/app", WindowIndex: 0, WindowName: "editor", PaneIndex: 0, PID: 10},
		{Target: "g/app:0.1", Session: "g/app", WindowIndex: 0, WindowName: "editor", PaneIndex: 1, PID: 20},
		{Target: "g/app:1.0", Session: "g/app", WindowIndex: 1, WindowName: "tests", PaneIndex: 0, PID: 30},
	}
	processes := []Process{
		{PID: 10, PPID: 1, CPU: 1.5, RSSKB: 100, Command: "zsh"},
		{PID: 11, PPID: 10, CPU: 2.0, RSSKB: 200, Command: "node"},
		{PID: 12, PPID: 11, CPU: 3.0, RSSKB: 300, Command: "vite"},
		{PID: 20, PPID: 1, CPU: 4.0, RSSKB: 400, Command: "zsh"},
		{PID: 21, PPID: 20, CPU: 5.0, RSSKB: 500, Command: "go test"},
		{PID: 30, PPID: 1, CPU: 0.5, RSSKB: 50, Command: "zsh"},
		{PID: 99, PPID: 1, CPU: 50.0, RSSKB: 9000, Command: "unrelated"},
	}

	got := BuildWindowUsages(panes, processes)

	if len(got) != 2 {
		t.Fatalf("len(BuildWindowUsages()) = %d, want 2", len(got))
	}
	if got[0].Target != "g/app:0" {
		t.Fatalf("first target = %q, want g/app:0", got[0].Target)
	}
	if got[0].RSSKB != 1500 {
		t.Fatalf("RSSKB = %d, want 1500", got[0].RSSKB)
	}
	if got[0].CPU != 15.5 {
		t.Fatalf("CPU = %v, want 15.5", got[0].CPU)
	}
	if got[0].ProcessCount != 5 {
		t.Fatalf("ProcessCount = %d, want 5", got[0].ProcessCount)
	}
	if got[1].Target != "g/app:1" {
		t.Fatalf("second target = %q, want g/app:1", got[1].Target)
	}
}

func TestBuildWindowUsagesDeduplicatesSharedPaneRoots(t *testing.T) {
	panes := []Pane{
		{Target: "g/app:0.0", Session: "g/app", WindowIndex: 0, WindowName: "editor", PaneIndex: 0, PID: 10},
		{Target: "g/app:0.1", Session: "g/app", WindowIndex: 0, WindowName: "editor", PaneIndex: 1, PID: 10},
	}
	processes := []Process{
		{PID: 10, PPID: 1, CPU: 1.0, RSSKB: 100, Command: "zsh"},
		{PID: 11, PPID: 10, CPU: 2.0, RSSKB: 200, Command: "node"},
	}

	got := BuildWindowUsages(panes, processes)

	if len(got) != 1 {
		t.Fatalf("len(BuildWindowUsages()) = %d, want 1", len(got))
	}
	if got[0].RSSKB != 300 {
		t.Fatalf("RSSKB = %d, want 300", got[0].RSSKB)
	}
	if got[0].CPU != 3.0 {
		t.Fatalf("CPU = %v, want 3.0", got[0].CPU)
	}
	if got[0].ProcessCount != 2 {
		t.Fatalf("ProcessCount = %d, want 2", got[0].ProcessCount)
	}
}

func TestBuildWindowUsagesSortsByMemoryThenCPU(t *testing.T) {
	panes := []Pane{
		{Target: "g/app:0.0", Session: "g/app", WindowIndex: 0, WindowName: "editor", PID: 10},
		{Target: "g/app:1.0", Session: "g/app", WindowIndex: 1, WindowName: "server", PID: 20},
		{Target: "gs/sh/2:0.0", Session: "gs/sh/2", WindowIndex: 0, WindowName: "zsh", PID: 30},
	}
	processes := []Process{
		{PID: 10, PPID: 1, CPU: 2.0, RSSKB: 100},
		{PID: 20, PPID: 1, CPU: 1.0, RSSKB: 200},
		{PID: 30, PPID: 1, CPU: 9.0, RSSKB: 200},
	}

	got := BuildWindowUsages(panes, processes)

	wantTargets := []string{"gs/sh/2:0", "g/app:1", "g/app:0"}
	for i, want := range wantTargets {
		if got[i].Target != want {
			t.Fatalf("target[%d] = %q, want %q", i, got[i].Target, want)
		}
	}
	if !got[0].Shadow {
		t.Fatal("shadow window was not marked Shadow")
	}
}

func TestParsePSOutputPreservesCommandsWithSpaces(t *testing.T) {
	got, err := ParsePSOutput("123 1 0.2 4096 /bin/zsh -l\n124 123 5.5 8192 node dev server\n")
	if err != nil {
		t.Fatalf("ParsePSOutput() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(ParsePSOutput()) = %d, want 2", len(got))
	}
	if got[1].Command != "node dev server" {
		t.Fatalf("Command = %q, want %q", got[1].Command, "node dev server")
	}
}
