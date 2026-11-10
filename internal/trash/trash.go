// Package trash owns the journal between validated worktree removal and physical
// deletion. Git remains the registration authority; a prepared entry is undone
// after interruption unless Git has already removed its administrative record.
package trash

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

const marker = ".grove-entry.json"

// Manager indexes trash roots so external worktrees remain discoverable after
// their Git registration disappears. The index authorizes no deletion by itself:
// root and entry markers and the moved directory identity must all match.
type Manager struct {
	State  string
	rename func(string, string) error
}

// Entry is a durable detach journal and the additive pending-deletion API.
// TreeID and StubID bind recovery to the directories this transaction created.
type Entry struct {
	Version  int       `json:"version"`
	ID       string    `json:"id"`
	Root     string    `json:"root"`
	RootID   string    `json:"root_id"`
	Original string    `json:"original_path"`
	Main     string    `json:"main_path"`
	Admin    string    `json:"admin_path"`
	AdminID  string    `json:"admin_id"`
	GitFile  string    `json:"git_file"`
	TreeID   string    `json:"tree_id"`
	StubID   string    `json:"stub_id"`
	Ready    bool      `json:"ready"`
	Created  time.Time `json:"created_at"`
}

type rootRecord struct {
	Version int    `json:"version"`
	Path    string `json:"path"`
	ID      string `json:"id"`
}

func Default() (*Manager, error) {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		state = filepath.Join(home, ".local", "state")
	}
	if !filepath.IsAbs(state) {
		return nil, fmt.Errorf("XDG_STATE_HOME must be absolute")
	}
	return New(filepath.Join(state, "grove", "trash")), nil
}

func New(state string) *Manager { return &Manager{State: state, rename: renameNoReplace} }

// RootFor keeps managed trash out of the branch namespace (Git ref components
// cannot start with a dot). External worktrees use a sibling on their own volume.
func RootFor(main, original string) string {
	managed := filepath.Join(main, ".wt")
	if within(managed, original) {
		return filepath.Join(managed, ".grove-trash")
	}
	return filepath.Join(filepath.Dir(original), ".grove-trash")
}

func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func token() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func validToken(s string) bool    { b, e := hex.DecodeString(s); return e == nil && len(b) == 16 }
func (e Entry) directory() string { return filepath.Join(e.Root, "entry-"+e.ID) }
func (e Entry) TreePath() string  { return filepath.Join(e.directory(), "tree") }

func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// Git removes the worktrees directory itself when the last linked tree goes.
// Sync the nearest surviving ancestor so that case does not report a failed
// removal after Git has already committed it.
func syncExistingParent(path string) error {
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		err := syncDir(parent)
		if !os.IsNotExist(err) || parent == filepath.Dir(parent) {
			return err
		}
	}
}

func readJSON(path string, value any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing nonregular marker %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
func realDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing non-directory or symlink %s", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if resolved != filepath.Clean(path) {
		return fmt.Errorf("refusing symlink components in %s", path)
	}
	return nil
}
func (m *Manager) rootIndex(path string) string {
	sum := sha256.Sum256([]byte(path))
	return filepath.Join(m.State, "roots", fmt.Sprintf("%x.json", sum))
}

func (m *Manager) ensureRoot(path string) (rootRecord, error) {
	var record rootRecord
	if err := os.MkdirAll(path, 0700); err != nil {
		return record, err
	}
	if err := realDirectory(path); err != nil {
		return record, err
	}
	lock := flock.New(filepath.Join(path, ".lock"))
	if err := lock.Lock(); err != nil {
		return record, err
	}
	defer lock.Close()
	markerPath := filepath.Join(path, ".grove-trash.json")
	err := readJSON(markerPath, &record)
	if os.IsNotExist(err) {
		// Never adopt a pre-existing directory containing someone else's files.
		entries, e := os.ReadDir(path)
		if e != nil {
			return record, e
		}
		for _, entry := range entries {
			if entry.Name() != ".lock" {
				return record, fmt.Errorf("refusing unrecognized trash root %s", path)
			}
		}
		record = rootRecord{Version: 1, Path: path, ID: token()}
		err = writeJSON(markerPath, record)
	}
	if err != nil {
		return record, err
	}
	if record.Version != 1 || record.Path != path || !validToken(record.ID) {
		return record, fmt.Errorf("unrecognized trash root %s", path)
	}
	if err = os.MkdirAll(filepath.Join(m.State, "roots"), 0700); err != nil {
		return record, err
	}
	return record, writeJSON(m.rootIndex(path), record)
}

// Detach runs validate again while holding the trash-root lock, then journals
// before moving anything. A rename failure returns detached=false for Git's slow
// path. Once moved, every error attempts rollback; the journal survives if undo
// needs a later command. unregister must remove only this worktree through Git.
func (m *Manager) Detach(original, main, admin string, validate func() error, unregister func() error) (detached bool, err error) {
	root := RootFor(main, original)
	record, err := m.ensureRoot(root)
	if err != nil {
		return false, nil
	} // Optional fast path; Git still owns fallback validation.
	lock := flock.New(filepath.Join(root, ".lock"))
	if err = lock.Lock(); err != nil {
		return false, err
	}
	defer lock.Close()
	if err = validate(); err != nil {
		return false, err
	}
	if within(original, root) || original == root {
		return false, nil
	}
	treeID, err := directoryID(original)
	if err != nil {
		return false, nil
	}
	gitFile, err := os.ReadFile(filepath.Join(original, ".git"))
	if err != nil {
		return false, nil
	}
	// Only a verified administrative record may be used in a forced stub removal.
	if strings.TrimSpace(string(gitFile)) != "gitdir: "+admin {
		return false, nil
	}
	adminID, err := directoryID(admin)
	if err != nil {
		return false, nil
	}
	entry := Entry{AdminID: adminID, Version: 1, ID: token(), Root: root, RootID: record.ID, Original: original, Main: main, Admin: admin, GitFile: string(gitFile), TreeID: treeID, Created: time.Now().UTC()}
	directory := entry.directory()
	if err = os.Mkdir(directory, 0700); err != nil {
		return false, err
	}
	stub := filepath.Join(directory, "stub")
	cleanup := func() {
		_ = os.Remove(filepath.Join(stub, ".git"))
		_ = os.Remove(stub)
		_ = os.Remove(filepath.Join(directory, marker))
		_ = os.Remove(directory)
	}
	if err = os.Mkdir(stub, 0700); err != nil {
		cleanup()
		return false, err
	}
	if err = os.WriteFile(filepath.Join(stub, ".git"), gitFile, 0600); err != nil {
		cleanup()
		return false, err
	}
	entry.StubID, err = directoryID(stub)
	if err != nil {
		cleanup()
		return false, err
	}
	if err = writeJSON(filepath.Join(directory, marker), entry); err != nil {
		cleanup()
		return false, err
	}
	if err = syncDir(root); err != nil {
		cleanup()
		return false, err
	}
	if err = m.rename(original, entry.TreePath()); err != nil {
		cleanup()
		return false, nil
	}
	// From here a crash is recoverable: prepared + admin present restores the tree;
	// admin absent means Git completed the detach, even if Ready was not persisted.
	undo := func(cause error) (bool, error) {
		recovery := m.recover(&entry)
		return false, errors.Join(cause, recovery)
	}
	if err = syncDir(filepath.Dir(original)); err != nil {
		return undo(err)
	}
	if err = renameNoReplace(stub, original); err != nil {
		return undo(err)
	}
	if err = syncDir(directory); err != nil {
		return undo(err)
	}
	if err = syncDir(filepath.Dir(original)); err != nil {
		return undo(err)
	}
	if err = unregister(); err != nil {
		return undo(err)
	}
	// Persist Git's targeted unregistration before publishing a reapable journal.
	// Recovery can infer this same commit point from the absent admin directory.
	if err = syncExistingParent(admin); err != nil {
		return true, err
	}
	if err = syncDir(filepath.Dir(original)); err != nil {
		return true, err
	}
	entry.Ready = true
	if err = writeJSON(filepath.Join(directory, marker), entry); err != nil {
		return true, err
	}
	return true, nil
}

func (m *Manager) recover(e *Entry) error {
	if e.Ready {
		return nil
	}
	adminID, adminErr := directoryID(e.Admin)
	if os.IsNotExist(adminErr) || (adminErr == nil && adminID != e.AdminID) {
		// Git removal was committed. Never look at a new tree reusing Original.
		e.Ready = true
		return writeJSON(filepath.Join(e.directory(), marker), e)
	}
	if adminErr != nil {
		return adminErr
	}
	if id, err := directoryID(e.TreePath()); err == nil {
		if id != e.TreeID {
			return fmt.Errorf("trash tree identity changed: %s", e.TreePath())
		}
		if id, err = directoryID(e.Original); err == nil {
			if id != e.StubID {
				return fmt.Errorf("refusing to overwrite worktree during recovery: %s", e.Original)
			}
			if err = removeStub(e.Original, e.GitFile); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := renameNoReplace(e.TreePath(), e.Original); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	} else if id, err := directoryID(e.Original); err != nil || id != e.TreeID {
		return fmt.Errorf("cannot locate original tree for %s", e.ID)
	}
	// Only tiny protocol-owned files are removed here, never recursive user data.
	stub := filepath.Join(e.directory(), "stub")
	if _, err := os.Lstat(stub); err == nil {
		if id, err := directoryID(stub); err != nil || id != e.StubID {
			return fmt.Errorf("stub identity changed")
		}
		if err := removeStub(stub, e.GitFile); err != nil {
			return err
		}
	}
	if err := os.Remove(filepath.Join(e.directory(), marker)); err != nil {
		return err
	}
	return os.Remove(e.directory())
}
func removeStub(path, gitFile string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	// Git may have unlinked .git before failing or being killed. The caller
	// already verified StubID, so an empty stub can safely be removed on retry.
	if len(entries) == 0 {
		return os.Remove(path)
	}
	if len(entries) != 1 || entries[0].Name() != ".git" {
		return fmt.Errorf("refusing modified detach stub %s", path)
	}
	info, err := os.Lstat(filepath.Join(path, ".git"))
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("refusing modified stub pointer")
	}
	data, err := os.ReadFile(filepath.Join(path, ".git"))
	if err != nil {
		return err
	}
	if string(data) != gitFile {
		return fmt.Errorf("refusing modified stub pointer")
	}
	if err = os.Remove(filepath.Join(path, ".git")); err != nil {
		return err
	}
	return os.Remove(path)
}

// Pending reads journals without repairing, scanning trees, or changing Git.
// Unrecognized entries are errors, never candidates for recursive deletion.
func (m *Manager) Pending() ([]Entry, error) { return m.scan(false) }
func (m *Manager) scan(recover bool) ([]Entry, error) {
	pending := []Entry{}
	indexes, err := os.ReadDir(filepath.Join(m.State, "roots"))
	if os.IsNotExist(err) {
		return pending, nil
	}
	if err != nil {
		return pending, err
	}
	var failures []error
	for _, index := range indexes {
		var record rootRecord
		if err = readJSON(filepath.Join(m.State, "roots", index.Name()), &record); err != nil {
			failures = append(failures, err)
			continue
		}
		entries, e := m.scanRoot(record, recover)
		pending = append(pending, entries...)
		if e != nil {
			failures = append(failures, e)
		}
	}
	return pending, errors.Join(failures...)
}
func (m *Manager) scanRoot(record rootRecord, recover bool) ([]Entry, error) {
	pending := []Entry{}
	if err := realDirectory(record.Path); os.IsNotExist(err) {
		return pending, nil
	} else if err != nil {
		return pending, err
	}
	var actual rootRecord
	if err := readJSON(filepath.Join(record.Path, ".grove-trash.json"), &actual); err != nil {
		return pending, err
	}
	if actual != record || record.Version != 1 || !validToken(record.ID) {
		return pending, fmt.Errorf("unrecognized trash root %s", record.Path)
	}
	lock := flock.New(filepath.Join(record.Path, ".lock"))
	if err := lock.Lock(); err != nil {
		return pending, err
	}
	defer lock.Close()
	dirs, err := os.ReadDir(record.Path)
	if err != nil {
		return pending, err
	}
	var failures []error
	for _, dir := range dirs {
		if dir.Name() == ".lock" || dir.Name() == ".grove-trash.json" {
			continue
		}
		directory := filepath.Join(record.Path, dir.Name())
		entry, e := readEntry(record, directory)
		if e != nil {
			failures = append(failures, e)
			continue
		}
		if recover && !entry.Ready {
			if e = m.recover(&entry); e != nil {
				failures = append(failures, e)
				continue
			}
			if !entry.Ready {
				continue
			}
		}
		pending = append(pending, entry)
	}
	return pending, errors.Join(failures...)
}
func readEntry(root rootRecord, directory string) (Entry, error) {
	var entry Entry
	if err := realDirectory(directory); err != nil {
		return entry, err
	}
	if err := readJSON(filepath.Join(directory, marker), &entry); err != nil {
		return entry, fmt.Errorf("refusing unrecognized trash entry %s: %w", directory, err)
	}
	if entry.Version != 1 || !validToken(entry.ID) || entry.Root != root.Path || entry.RootID != root.ID || entry.directory() != directory || !filepath.IsAbs(entry.Original) || !filepath.IsAbs(entry.Main) || !filepath.IsAbs(entry.Admin) || within(root.Path, entry.Original) || entry.Original == root.Path || entry.TreeID == "" || entry.StubID == "" || entry.AdminID == "" {
		return entry, fmt.Errorf("refusing unrecognized trash entry %s", directory)
	}
	return entry, nil
}

// reapOnce recovers prepared journals and deletes up to four detached trees.
// process_unix owns the process lease and durable wakeup handoff around this pass.
func (m *Manager) reapOnce(ctx context.Context) error {
	entries, scanErr := m.scan(true)
	jobs := make(chan Entry)
	errs := make(chan error, len(entries))
	done := make(chan struct{}, 4)
	for range 4 {
		go func() {
			defer func() { done <- struct{}{} }()
			for entry := range jobs {
				if err := m.reapEntry(ctx, entry); err != nil {
					errs <- err
				}
			}
		}()
	}
	for _, entry := range entries {
		jobs <- entry
	}
	close(jobs)
	for range 4 {
		<-done
	}
	close(errs)
	failures := []error{scanErr}
	for err := range errs {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
func (m *Manager) reapEntry(ctx context.Context, e Entry) error {
	// A writer can keep extending a directory without letting ReadDir reach EOF.
	// Bound an individual attempt as well as the number of directory passes.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if !e.Ready {
		return fmt.Errorf("entry is not detached: %s", e.ID)
	}
	if id, err := directoryID(e.TreePath()); err == nil {
		if id != e.TreeID {
			return fmt.Errorf("trash tree identity changed: %s", e.ID)
		}
		if err = deleteTree(ctx, e.TreePath()); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	// Keep the journal until only our marker remains. Unexpected container content
	// is refused, even though the validated tree itself has already been deleted.
	names, err := os.ReadDir(e.directory())
	if err != nil {
		return err
	}
	for _, name := range names {
		if name.Name() != marker {
			return fmt.Errorf("unrecognized file in trash entry: %s", name.Name())
		}
	}
	if err = os.Remove(filepath.Join(e.directory(), marker)); err != nil {
		return err
	}
	return os.Remove(e.directory())
}
