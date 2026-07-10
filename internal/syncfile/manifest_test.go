package syncfile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseManifestEntryDefaultsAndTargets(t *testing.T) {
	data := []byte(`root: /code
groups:
  ".":
    - git@github.com:acme/root-repo.git
  teams/platform:
    - url: https://github.com/acme/api.git
      branch: trunk
  clis:
    - url: https://github.com/acme/tool.git
      name: nested/tool-copy
`)

	m, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	repos := m.Repos()
	want := []Repo{
		{Group: ".", Entry: Entry{URL: "git@github.com:acme/root-repo.git", Name: "root-repo"}, Path: "/code/root-repo"},
		{Group: "teams/platform", Entry: Entry{URL: "https://github.com/acme/api.git", Name: "api", Branch: "trunk"}, Path: "/code/teams/platform/api"},
		{Group: "clis", Entry: Entry{URL: "https://github.com/acme/tool.git", Name: "nested/tool-copy"}, Path: "/code/clis/nested/tool-copy"},
	}
	if !reflect.DeepEqual(repos, want) {
		t.Fatalf("Repos() = %#v, want %#v", repos, want)
	}
	if got := repos[0].Key(); got != "./root-repo" {
		t.Fatalf("root repo key = %q, want ./root-repo", got)
	}
}

func TestManifestRenderRoundTrip(t *testing.T) {
	want := &Manifest{
		Root:       "~/code",
		GroupOrder: []string{"clis"},
		Groups: map[string][]Entry{
			"clis": {
				{URL: "git@github.com:acme/grove.git", Name: "grove"},
				{URL: "https://github.com/acme/renamed.git", Name: "local-name", Branch: "dev"},
			},
		},
	}
	raw, err := Render(want)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "    - git@github.com:acme/grove.git") {
		t.Fatalf("default entry did not render as a bare URL:\n%s", raw)
	}
	got, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}

func TestParsePreservesAuthoredGroupAndEntryOrder(t *testing.T) {
	m, err := Parse([]byte(`root: /code
groups:
  z-last:
    - url: https://example.com/b.git
      name: b
    - url: https://example.com/a.git
      name: a
  a-first:
    - https://example.com/c.git
`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, repo := range m.Repos() {
		got = append(got, repo.Key())
	}
	want := []string{"z-last/b", "z-last/a", "a-first/c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repo order = %v, want %v", got, want)
	}
}

func TestRenderQuotesLeadingYAMLIndicators(t *testing.T) {
	m := &Manifest{Root: "~/code", Groups: map[string][]Entry{
		"%team": {{URL: "https://example.com/repo.git", Name: "\"repo"}},
	}}
	raw, err := Render(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(raw)
	if err != nil {
		t.Fatalf("rendered manifest is invalid: %v\n%s", err, raw)
	}
	if repos := got.Repos(); len(repos) != 1 || repos[0].Group != "%team" || repos[0].Name != "\"repo" {
		t.Fatalf("round trip repos = %#v", got.Repos())
	}
}

func TestParseManifestRejectsUnsafeAndDuplicateTargets(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"parent group", "root: /code\ngroups:\n  ../elsewhere:\n    - https://example.com/a.git\n", "invalid group"},
		{"absolute name", "root: /code\ngroups:\n  clis:\n    - url: https://example.com/a.git\n      name: /tmp/a\n", "invalid name"},
		{"duplicate target", "root: /code\ngroups:\n  clis:\n    - https://example.com/a.git\n    - https://elsewhere.test/a.git\n", "duplicate target"},
		{"same path through dot group", "root: /code\ngroups:\n  .:\n    - url: https://example.com/a.git\n      name: clis/a\n  clis:\n    - https://elsewhere.test/a.git\n", "duplicate target"},
		{"overlapping targets", "root: /code\ngroups:\n  clis:\n    - https://example.com/a.git\n    - url: https://example.com/b.git\n      name: a/nested\n", "overlapping targets"},
		{"missing url", "root: /code\ngroups:\n  clis:\n    - name: nope\n", "url is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Parse() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestAppendPreservesExistingManifestText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sync.yaml")
	original := `# hand-written header
root: ~/code
groups:
  clis:
    - https://example.com/old.git
    # keep this note

custom_key: keep-me
`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	additions := map[string][]Entry{
		"clis":  {{URL: "https://example.com/new.git", Name: "new"}},
		"hacks": {{URL: "https://example.com/fork.git", Name: "fork-copy"}},
	}
	if err := Append(path, "~/code", additions); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, preserved := range []string{"# hand-written header", "# keep this note", "custom_key: keep-me", "https://example.com/old.git"} {
		if !strings.Contains(string(raw), preserved) {
			t.Fatalf("append removed %q:\n%s", preserved, raw)
		}
	}
	m, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(m.Repos()); got != 3 {
		t.Fatalf("repo count = %d, want 3", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("manifest permissions = %o, want 600", got)
	}
}

func TestAppendCreatesManifestWithOnlyAdditions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "sync.yaml")
	err := Append(path, "~/code", map[string][]Entry{
		"clis": {{URL: "https://example.com/grove.git", Name: "grove"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Repos()) != 1 || filepath.Base(m.Repos()[0].Path) != "grove" {
		t.Fatalf("created manifest repos = %#v", m.Repos())
	}
}
