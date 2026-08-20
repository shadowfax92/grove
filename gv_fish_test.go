package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFishWrapperRoutesFlags(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish is not installed")
	}
	for _, test := range []struct {
		name   string
		args   string
		direct bool
	}{
		{name: "json true", args: "new --json=true", direct: true},
		{name: "json false", args: "new --json=false", direct: false},
		{name: "merged true", args: "rm --merged=true --dry-run=true", direct: true},
		{name: "merged false", args: "rm --merged=false .", direct: false},
		{name: "older than", args: "rm --older-than 14d", direct: true},
		{name: "older than equals", args: "rm --older-than=14d", direct: true},
		{name: "missing true", args: "rm --missing=true", direct: true},
		{name: "missing false", args: "rm --missing=false .", direct: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "line\nbreak")
			if err := os.Mkdir(target, 0755); err != nil {
				t.Fatal(err)
			}
			script := strings.Join([]string{
				"function grove",
				"    if contains -- --null $argv",
				"        printf '%s\\0' $FISH_GROVE_TARGET",
				"    else",
				"        printf '<%s>\\n' $argv",
				"    end",
				"end",
				"source ./gv.fish",
				"gv " + test.args,
				"string escape -- $PWD",
			}, "\n")
			command := exec.Command(fish, "--no-config", "-c", script)
			command.Env = append(os.Environ(), "FISH_GROVE_TARGET="+target)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("fish error = %v\n%s", err, output)
			}
			if test.direct {
				if strings.Contains(string(output), "<--null>") || !strings.Contains(string(output), "<"+strings.Fields(test.args)[1]+">") {
					t.Fatalf("direct output = %q", output)
				}
				return
			}
			if got, want := strings.TrimSpace(string(output)), strings.ReplaceAll(target, "\n", `\n`); got != want {
				t.Fatalf("PWD output = %q, want %q", got, want)
			}
		})
	}
}
