// Package llmstore gives each Git repository one durable context directory, its
// store, outside every checkout, and links it into each worktree as .llm.
// Worktrees come and go; stores outlive them. Grove never deletes from a store:
// aging out only moves idle top-level entries into the store's own archive/.
//
//	<worktree>/.llm ──symlink──▶ <root>/<key>/
//	                               ├── <entries agents write>
//	                               └── archive/   (entries idle past the cutoff)
package llmstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	gitx "grove/internal/git"
)

const (
	DefaultDir          = ".llm"
	DefaultArchiveAfter = 30 * 24 * time.Hour

	archiveDir = "archive"
	// marker identifies a store root. Keys mirror checkout paths, so stores can
	// nest: a repository at ~/code/a/b keeps its store inside ~/code/a's store.
	// Archive never moves an entry that contains a marker.
	marker        = ".grove-llm"
	markerContent = "This directory is a grove llm store. grove llm archive never moves an entry containing this file.\n"
)

// DefaultKeep names top-level entries that agent tooling expects to stay put.
var DefaultKeep = []string{"runs", "AGENTS.md", "CLAUDE.md", "README.md"}

var (
	ErrDisabled = errors.New("llm store is disabled for this repository")
	ErrBlocked  = errors.New("anchor is occupied")
)

type Settings struct {
	// Root holds every store, at Root/<key>.
	Root string
	// Dir is the anchor name inside each worktree. Empty means DefaultDir.
	Dir string
	// Aliases are extra anchor names, each its own symlink to the same store.
	Aliases []string
	// Keep lists top-level names Archive never moves. Nil means DefaultKeep.
	// archive/ itself is always kept.
	Keep []string
}

type Store struct {
	root string
	// anchors holds Dir first, then Aliases, in the order Link reports them.
	anchors []string
	keep    map[string]bool
	home    string
}

// Repo identifies a repository for store operations.
type Repo struct {
	Git *gitx.Repository
	// Key is an explicit store key from config; empty derives one from the
	// main checkout's path relative to $HOME.
	Key      string
	Disabled bool
}

// Target is where one repository's store lives.
type Target struct {
	Key  string `json:"key"`
	Path string `json:"path"`
}

// Action is what Link did to one anchor, or would do under dry run.
type Action string

const (
	Linked    Action = "linked"
	Created   Action = "created"
	Repointed Action = "repointed"
	Blocked   Action = "blocked"
)

// Link reports one worktree's anchors: Dir first, then each alias.
type Link struct {
	Store   Target   `json:"store"`
	Anchors []Anchor `json:"anchors"`
}

type Anchor struct {
	Path   string `json:"path"`
	Action Action `json:"action"`
	// Previous is the old target of a repointed symlink.
	Previous string `json:"previous,omitempty"`
}

// Archived is one top-level store entry that aged out.
type Archived struct {
	Name        string    `json:"name"`
	Destination string    `json:"destination"`
	LastChanged time.Time `json:"last_changed"`
}

func Open(settings Settings, home string) (*Store, error) {
	if !filepath.IsAbs(settings.Root) {
		return nil, fmt.Errorf("llm.root must be absolute or start with ~/")
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return nil, fmt.Errorf("resolving home: %w", err)
	}
	root := filepath.Clean(settings.Root)
	for _, candidate := range []string{filepath.Clean(home), realHome} {
		if root == candidate || contains(root, candidate) {
			return nil, fmt.Errorf("llm.root must not be $HOME or one of its parents")
		}
	}
	dir := settings.Dir
	if dir == "" {
		dir = DefaultDir
	}
	anchors := append([]string{dir}, settings.Aliases...)
	seen := make(map[string]bool, len(anchors))
	for _, name := range anchors {
		// .git and .wt already exist at worktree roots; a link name there would
		// at best be blocked and at worst confuse Git or Grove's worktree root.
		if name == "" || name == "." || name == ".." || name == ".git" || name == ".wt" || strings.ContainsAny(name, `/\`) {
			return nil, fmt.Errorf("llm.dir and llm.aliases must be single directory names, got %q", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("llm anchor name %q is listed twice", name)
		}
		seen[name] = true
	}
	names := settings.Keep
	if names == nil {
		names = DefaultKeep
	}
	keep := map[string]bool{archiveDir: true}
	for _, name := range names {
		keep[name] = true
	}
	return &Store{root: root, anchors: anchors, keep: keep, home: realHome}, nil
}

// Target resolves a repository's store. An explicit key wins, which is how
// separate clones of one project share a store.
func (s *Store) Target(repo Repo) (Target, error) {
	if repo.Disabled {
		return Target{}, ErrDisabled
	}
	key := repo.Key
	if key == "" {
		rel, err := filepath.Rel(s.home, repo.Git.MainPath)
		if err != nil || rel == "." || !filepath.IsLocal(rel) {
			return Target{}, fmt.Errorf("%s is not inside $HOME; set llm: <key> on its config row", repo.Git.MainPath)
		}
		key = filepath.ToSlash(rel)
	}
	native := filepath.FromSlash(key)
	if native == "." || !filepath.IsLocal(native) || filepath.Clean(native) != native {
		return Target{}, fmt.Errorf("invalid llm store key %q: use a relative path such as code/project", key)
	}
	return Target{Key: key, Path: filepath.Join(s.root, native)}, nil
}

// Link makes each anchor (Dir, then every alias) in worktree a symlink to the
// repository's store. A missing, dangling or foreign symlink is (re)pointed
// because only the link changes; a real file or directory is never touched and
// yields ErrBlocked. One blocked anchor does not stop the others. Every non-dry
// run also ensures the store directory and the shared Git exclusions exist.
func (s *Store) Link(repo Repo, worktree string, dryRun bool) (Link, error) {
	target, err := s.Target(repo)
	if err != nil {
		return Link{}, err
	}
	link := Link{Store: target, Anchors: make([]Anchor, 0, len(s.anchors))}
	if !dryRun {
		if err := s.prepare(repo, target); err != nil {
			return link, err
		}
	}
	var errs []error
	for _, name := range s.anchors {
		anchor, err := linkAnchor(worktree, name, target.Path, dryRun)
		link.Anchors = append(link.Anchors, anchor)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return link, errors.Join(errs...)
}

func linkAnchor(worktree, name, storePath string, dryRun bool) (Anchor, error) {
	anchor := Anchor{Path: filepath.Join(worktree, name)}
	info, err := os.Lstat(anchor.Path)
	switch {
	case os.IsNotExist(err):
		anchor.Action = Created
	case err != nil:
		return anchor, err
	case info.Mode()&os.ModeSymlink != 0:
		previous, err := os.Readlink(anchor.Path)
		if err != nil {
			return anchor, err
		}
		if pointsAt(anchor.Path, previous, storePath) {
			anchor.Action = Linked
			return anchor, nil
		}
		anchor.Action, anchor.Previous = Repointed, previous
	default:
		kind := "file"
		if info.IsDir() {
			kind = "directory"
		}
		anchor.Action = Blocked
		return anchor, fmt.Errorf("%w: %s is a real %s; move it aside (mv %s %s.bak) and rerun", ErrBlocked, anchor.Path, kind, anchor.Path, anchor.Path)
	}
	// A branch that tracks the anchor would turn grove's link into a
	// working-tree change, so refuse before touching anything.
	tracked, err := gitx.Tracked(worktree, name)
	if err != nil {
		return anchor, err
	}
	if tracked {
		return anchor, fmt.Errorf("refusing to link %s: the branch tracks files at %s", anchor.Path, name)
	}
	if dryRun {
		return anchor, nil
	}
	if anchor.Action == Created {
		return anchor, os.Symlink(storePath, anchor.Path)
	}
	return anchor, replaceSymlink(anchor.Path, storePath)
}

// prepare runs even when every anchor is already correct, so moving the store
// root aside heals on the next link instead of leaving worktrees pointing at nothing.
func (s *Store) prepare(repo Repo, target Target) error {
	if err := os.MkdirAll(target.Path, 0755); err != nil {
		return fmt.Errorf("creating llm store: %w", err)
	}
	markerPath := filepath.Join(target.Path, marker)
	if _, err := os.Lstat(markerPath); os.IsNotExist(err) {
		if err := os.WriteFile(markerPath, []byte(markerContent), 0644); err != nil {
			return fmt.Errorf("marking llm store: %w", err)
		}
	}
	for _, name := range s.anchors {
		// No trailing slash: Git matches a symlink as a file, so "/.llm/" would
		// not ignore it. The leading slash limits the rule to each worktree root.
		if err := repo.Git.EnsureExcluded("/" + name); err != nil {
			return err
		}
	}
	return nil
}

// Archive moves top-level entries whose whole tree is older than cutoff into
// <store>/archive/, in name order. It skips dotfiles, kept names and entries that
// contain another store. A taken destination gets a date suffix; nothing is ever
// overwritten or deleted, so `mv archive/<name> .` restores an entry.
func (s *Store) Archive(target Target, cutoff time.Time, dryRun bool) ([]Archived, error) {
	if target.Path != filepath.Join(s.root, filepath.FromSlash(target.Key)) {
		return nil, fmt.Errorf("llm store %s is not under %s", target.Path, s.root)
	}
	entries, err := os.ReadDir(target.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading llm store: %w", err)
	}
	archive := filepath.Join(target.Path, archiveDir)
	var archived []Archived
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || s.keep[name] {
			continue
		}
		lastChanged, movable, err := idleBefore(filepath.Join(target.Path, name), cutoff)
		if err != nil {
			return archived, fmt.Errorf("inspecting %s: %w", name, err)
		}
		if !movable {
			continue
		}
		item := Archived{Name: name, LastChanged: lastChanged}
		if dryRun {
			item.Destination = freeDestination(archive, name)
		} else if item.Destination, err = moveIntoArchive(target.Path, name); err != nil {
			return archived, fmt.Errorf("archiving %s: %w", name, err)
		}
		archived = append(archived, item)
	}
	return archived, nil
}

// idleBefore reports the newest modification time in path's tree and whether
// the tree may move. It stops at the first change at or after cutoff, and at any
// nested store marker. WalkDir uses lstat, so symlinks are never followed.
func idleBefore(path string, cutoff time.Time) (time.Time, bool, error) {
	var newest time.Time
	movable := true
	err := filepath.WalkDir(path, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == marker && current != path {
			movable = false
			return fs.SkipAll
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		if !info.ModTime().Before(cutoff) {
			movable = false
			return fs.SkipAll
		}
		return nil
	})
	return newest, movable, err
}

func moveIntoArchive(store, name string) (string, error) {
	archive := filepath.Join(store, archiveDir)
	if err := os.Mkdir(archive, 0755); err != nil && !os.IsExist(err) {
		return "", err
	}
	// A symlinked archive/ would move entries out of the store.
	if info, err := os.Lstat(archive); err != nil || !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", archive)
	}
	for _, destination := range destinations(archive, name) {
		err := renameNoReplace(filepath.Join(store, name), destination)
		if err == nil {
			return destination, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("no free name for %s in %s", name, archive)
}

func freeDestination(archive, name string) string {
	for _, destination := range destinations(archive, name) {
		if _, err := os.Lstat(destination); os.IsNotExist(err) {
			return destination
		}
	}
	return ""
}

// destinations lists names to try in order: name, then name.YYYYMMDD, then
// name.YYYYMMDD-2 and up, so entries archived on different days stay apart.
func destinations(archive, name string) []string {
	dated := name + "." + time.Now().Format("20060102")
	candidates := []string{filepath.Join(archive, name), filepath.Join(archive, dated)}
	for suffix := 2; suffix <= 99; suffix++ {
		candidates = append(candidates, filepath.Join(archive, fmt.Sprintf("%s-%d", dated, suffix)))
	}
	return candidates
}

func pointsAt(anchor, linkTarget, storePath string) bool {
	if !filepath.IsAbs(linkTarget) {
		linkTarget = filepath.Join(filepath.Dir(anchor), linkTarget)
	}
	if filepath.Clean(linkTarget) == storePath {
		return true
	}
	resolved, err := filepath.EvalSymlinks(linkTarget)
	if err != nil {
		return false
	}
	want, err := filepath.EvalSymlinks(storePath)
	return err == nil && resolved == want
}

// replaceSymlink swaps the link atomically. rename(2) replaces the symlink entry
// itself and never follows it, so the old target directory is left untouched.
func replaceSymlink(anchor, target string) error {
	temporary := fmt.Sprintf("%s.grove-%d-%d", anchor, os.Getpid(), time.Now().UnixNano())
	if err := os.Symlink(target, temporary); err != nil {
		return err
	}
	if err := os.Rename(temporary, anchor); err != nil {
		os.Remove(temporary)
		return err
	}
	return nil
}

func contains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != "." && filepath.IsLocal(rel)
}
