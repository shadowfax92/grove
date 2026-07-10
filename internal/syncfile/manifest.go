// Package syncfile owns Grove's portable repository manifest and the safe
// local operations driven by it. It is deliberately independent from config.
package syncfile

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const DefaultRoot = "~/code"

type Manifest struct {
	Root       string             `yaml:"root"`
	Groups     map[string][]Entry `yaml:"groups"`
	GroupOrder []string           `yaml:"-"`
}

type Entry struct {
	URL    string `yaml:"url"`
	Name   string `yaml:"name,omitempty"`
	Branch string `yaml:"branch,omitempty"`
}

// UnmarshalYAML accepts the compact URL scalar and the expanded mapping form.
func (e *Entry) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		e.URL = strings.TrimSpace(node.Value)
	case yaml.MappingNode:
		var raw struct {
			URL    string `yaml:"url"`
			Name   string `yaml:"name"`
			Branch string `yaml:"branch"`
		}
		if err := node.Decode(&raw); err != nil {
			return err
		}
		e.URL = strings.TrimSpace(raw.URL)
		e.Name = strings.TrimSpace(raw.Name)
		e.Branch = strings.TrimSpace(raw.Branch)
	default:
		return fmt.Errorf("repo entry must be a URL string or mapping")
	}
	return nil
}

type Repo struct {
	Group string
	Entry
	Path string
}

func (r Repo) Key() string {
	return path.Clean(r.Group) + "/" + path.Clean(r.Name)
}

func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "grove", "sync.yaml"), nil
}

func ExpandRoot(root string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return expandTilde(root, home), nil
}

func Parse(data []byte) (*Manifest, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parsing sync manifest: %w", err)
	}
	mapping, err := rootMapping(&document)
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := document.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("parsing sync manifest: %w", err)
	}
	_, groupsNode := mappingPair(mapping, "groups")
	if groupsNode != nil && groupsNode.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(groupsNode.Content); i += 2 {
			manifest.GroupOrder = append(manifest.GroupOrder, groupsNode.Content[i].Value)
		}
	}
	if err := normalizeAndValidate(&manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func Load(file string) (*Manifest, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("reading sync manifest: %w", err)
	}
	manifest, err := Parse(data)
	if err != nil {
		return nil, err
	}
	manifest.Root, err = ExpandRoot(manifest.Root)
	if err != nil {
		return nil, err
	}
	return manifest, nil
}

func Ensure(file string) error {
	if _, err := os.Stat(file); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat sync manifest: %w", err)
	}
	return writeNew(file, &Manifest{Root: DefaultRoot, Groups: map[string][]Entry{}})
}

func Render(manifest *Manifest) ([]byte, error) {
	copyManifest := cloneManifest(manifest)
	if err := normalizeAndValidate(copyManifest); err != nil {
		return nil, err
	}

	var b strings.Builder
	b.WriteString("# Grove repository sync manifest\n\n")
	b.WriteString("root: " + yamlScalar(copyManifest.Root) + "\n")
	if len(copyManifest.Groups) == 0 {
		b.WriteString("groups: {}\n")
		return []byte(b.String()), nil
	}
	b.WriteString("groups:\n")
	groups := manifestGroupNames(copyManifest)
	for _, group := range groups {
		b.WriteString("  " + yamlScalar(group) + ":\n")
		for _, entry := range copyManifest.Groups[group] {
			b.WriteString(renderEntry(entry, 4))
		}
	}
	return []byte(b.String()), nil
}

// Append adds entries without re-marshalling an existing manifest, preserving
// comments, ordering, formatting, and unrelated hand-written keys.
func Append(file, root string, additions map[string][]Entry) error {
	if len(additions) == 0 {
		return nil
	}
	additionManifest := &Manifest{Root: root, Groups: cloneGroups(additions)}
	if err := normalizeAndValidate(additionManifest); err != nil {
		return err
	}

	data, err := os.ReadFile(file)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("reading sync manifest: %w", err)
		}
		return writeNew(file, additionManifest)
	}
	existing, err := Parse(data)
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, repo := range existing.Repos() {
		seen[repo.Key()] = true
	}
	for group, entries := range additionManifest.Groups {
		for _, entry := range entries {
			key := Repo{Group: group, Entry: entry}.Key()
			if seen[key] {
				return fmt.Errorf("target %s already exists in sync manifest", key)
			}
			seen[key] = true
		}
	}

	for _, group := range sortedGroupNames(additionManifest.Groups) {
		data, err = appendGroup(data, group, additionManifest.Groups[group])
		if err != nil {
			return err
		}
	}
	if _, err := Parse(data); err != nil {
		return fmt.Errorf("validating updated sync manifest: %w", err)
	}
	if err := os.WriteFile(file, data, 0644); err != nil {
		return fmt.Errorf("writing sync manifest: %w", err)
	}
	return nil
}

func (m *Manifest) Repos() []Repo {
	groups := manifestGroupNames(m)
	var repos []Repo
	for _, group := range groups {
		for _, entry := range m.Groups[group] {
			parts := []string{m.Root}
			if group != "." {
				parts = append(parts, filepath.FromSlash(group))
			}
			parts = append(parts, filepath.FromSlash(entry.Name))
			repos = append(repos, Repo{
				Group: group,
				Entry: entry,
				Path:  filepath.Clean(filepath.Join(parts...)),
			})
		}
	}
	return repos
}

func MatchOnly(key, pattern string) (bool, error) {
	if pattern == "" {
		return true, nil
	}
	matched, err := path.Match(pattern, key)
	if err != nil {
		return false, fmt.Errorf("invalid --only glob %q: %w", pattern, err)
	}
	return matched, nil
}

func NameFromURL(rawURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if i := strings.LastIndexAny(trimmed, "/:"); i >= 0 {
		trimmed = trimmed[i+1:]
	}
	return strings.TrimSuffix(trimmed, ".git")
}

func normalizeAndValidate(manifest *Manifest) error {
	manifest.Root = strings.TrimSpace(manifest.Root)
	if manifest.Root == "" {
		return fmt.Errorf("sync manifest root is required")
	}
	if manifest.Groups == nil {
		return fmt.Errorf("sync manifest groups is required")
	}

	seen := make(map[string]bool)
	for group, entries := range manifest.Groups {
		if !validRelative(group, true) {
			return fmt.Errorf("invalid group %q: must be a relative path", group)
		}
		for i := range entries {
			entry := &entries[i]
			entry.URL = strings.TrimSpace(entry.URL)
			entry.Name = strings.TrimSpace(entry.Name)
			entry.Branch = strings.TrimSpace(entry.Branch)
			if entry.URL == "" {
				return fmt.Errorf("group %s entry %d: url is required", group, i+1)
			}
			if entry.Name == "" {
				entry.Name = NameFromURL(entry.URL)
			}
			if !validRelative(entry.Name, false) {
				return fmt.Errorf("invalid name %q in group %s: must be a relative path", entry.Name, group)
			}
			key := Repo{Group: group, Entry: *entry}.Key()
			if seen[key] {
				return fmt.Errorf("duplicate target %s", key)
			}
			seen[key] = true
		}
		manifest.Groups[group] = entries
	}
	return nil
}

func validRelative(value string, allowDot bool) bool {
	if value == "." {
		return allowDot
	}
	if value == "" || path.IsAbs(value) || path.Clean(value) != value {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func expandTilde(value, home string) string {
	if value == "~" {
		return home
	}
	if strings.HasPrefix(value, "~/") {
		return filepath.Join(home, filepath.FromSlash(value[2:]))
	}
	return value
}

func writeNew(file string, manifest *Manifest) error {
	data, err := Render(manifest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		return fmt.Errorf("creating sync manifest directory: %w", err)
	}
	if err := os.WriteFile(file, data, 0644); err != nil {
		return fmt.Errorf("writing sync manifest: %w", err)
	}
	return nil
}

func appendGroup(data []byte, group string, entries []Entry) ([]byte, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parsing sync manifest: %w", err)
	}
	mapping, err := rootMapping(&document)
	if err != nil {
		return nil, err
	}
	groupsKey, groupsNode := mappingPair(mapping, "groups")
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	entryBlock := renderEntries(entries, 4)

	if groupsNode == nil {
		block := "groups:\n  " + yamlScalar(group) + ":\n" + entryBlock
		return insertBlock(lines, len(lines), block), nil
	}
	if groupsNode.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("sync manifest groups must be a mapping")
	}
	groupsLine := groupsKey.Line - 1
	if groupsNode.Style&yaml.FlowStyle != 0 {
		if len(groupsNode.Content) != 0 {
			return nil, fmt.Errorf("cannot append to flow-style groups; use block YAML")
		}
		lines[groupsLine] = strings.Replace(lines[groupsLine], "{}", "", 1)
		block := "  " + yamlScalar(group) + ":\n" + entryBlock
		return insertBlock(lines, groupsLine+1, block), nil
	}

	blockEnd := nextTopLevelLine(lines, groupsLine+1)
	for i := 0; i+1 < len(groupsNode.Content); i += 2 {
		key, sequence := groupsNode.Content[i], groupsNode.Content[i+1]
		if key.Value != group {
			continue
		}
		if sequence.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("sync manifest group %s must be a list", group)
		}
		keyLine := key.Line - 1
		if sequence.Style&yaml.FlowStyle != 0 {
			if len(sequence.Content) != 0 {
				return nil, fmt.Errorf("cannot append to flow-style group %s; use a block list", group)
			}
			lines[keyLine] = strings.Replace(lines[keyLine], "[]", "", 1)
			return insertBlock(lines, keyLine+1, entryBlock), nil
		}
		insertAt := blockEnd
		if i+2 < len(groupsNode.Content) {
			insertAt = groupsNode.Content[i+2].Line - 1
		}
		return insertBlock(lines, insertAt, entryBlock), nil
	}

	block := "  " + yamlScalar(group) + ":\n" + entryBlock
	return insertBlock(lines, blockEnd, block), nil
}

func rootMapping(document *yaml.Node) (*yaml.Node, error) {
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("sync manifest must be a YAML mapping")
	}
	return document.Content[0], nil
}

func mappingPair(mapping *yaml.Node, wanted string) (*yaml.Node, *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == wanted {
			return mapping.Content[i], mapping.Content[i+1]
		}
	}
	return nil, nil
}

func nextTopLevelLine(lines []string, start int) int {
	for i := start; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") && lines[i] == strings.TrimLeft(lines[i], " \t") {
			return i
		}
	}
	return len(lines)
}

func insertBlock(lines []string, index int, block string) []byte {
	blockLines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	out := make([]string, 0, len(lines)+len(blockLines)+1)
	out = append(out, lines[:index]...)
	out = append(out, blockLines...)
	out = append(out, lines[index:]...)
	return []byte(strings.Join(out, "\n") + "\n")
}

func renderEntries(entries []Entry, indent int) string {
	var b strings.Builder
	for _, entry := range entries {
		b.WriteString(renderEntry(entry, indent))
	}
	return b.String()
}

func renderEntry(entry Entry, indent int) string {
	spaces := strings.Repeat(" ", indent)
	if entry.Name == NameFromURL(entry.URL) && entry.Branch == "" {
		return spaces + "- " + yamlScalar(entry.URL) + "\n"
	}
	var b strings.Builder
	b.WriteString(spaces + "- url: " + yamlScalar(entry.URL) + "\n")
	if entry.Name != NameFromURL(entry.URL) {
		b.WriteString(spaces + "  name: " + yamlScalar(entry.Name) + "\n")
	}
	if entry.Branch != "" {
		b.WriteString(spaces + "  branch: " + yamlScalar(entry.Branch) + "\n")
	}
	return b.String()
}

func yamlScalar(value string) string {
	if plainYAMLScalar(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func plainYAMLScalar(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\n\r\t") || strings.Contains(value, ": ") || strings.Contains(value, " #") {
		return false
	}
	if strings.ContainsRune("-?:,[]{}#&*!|>'\"%@`", rune(value[0])) || value == "..." {
		return false
	}
	switch strings.ToLower(value) {
	case "null", "~", "true", "false", "yes", "no", "on", "off":
		return false
	}
	return true
}

func manifestGroupNames(manifest *Manifest) []string {
	if manifest == nil {
		return nil
	}
	seen := make(map[string]bool, len(manifest.Groups))
	names := make([]string, 0, len(manifest.Groups))
	for _, group := range manifest.GroupOrder {
		if _, ok := manifest.Groups[group]; ok && !seen[group] {
			names = append(names, group)
			seen[group] = true
		}
	}
	var remaining []string
	for group := range manifest.Groups {
		if !seen[group] {
			remaining = append(remaining, group)
		}
	}
	sort.Strings(remaining)
	return append(names, remaining...)
}

func sortedGroupNames(groups map[string][]Entry) []string {
	names := make([]string, 0, len(groups))
	for group := range groups {
		names = append(names, group)
	}
	sort.Strings(names)
	return names
}

func cloneManifest(manifest *Manifest) *Manifest {
	if manifest == nil {
		return &Manifest{}
	}
	return &Manifest{
		Root:       manifest.Root,
		Groups:     cloneGroups(manifest.Groups),
		GroupOrder: append([]string(nil), manifest.GroupOrder...),
	}
}

func cloneGroups(groups map[string][]Entry) map[string][]Entry {
	if groups == nil {
		return nil
	}
	copyGroups := make(map[string][]Entry, len(groups))
	for group, entries := range groups {
		copyGroups[group] = append([]Entry(nil), entries...)
	}
	return copyGroups
}
