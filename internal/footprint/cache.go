package footprint

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

// Cache publishes independent files by atomic rename. The per-path lock spans
// recheck, scan and publication: overlapping Grove processes reuse the winning
// scan instead of overwriting a newer result or multiplying expensive walks.
type Cache struct{ directory string }

func Default() (*Cache, error) {
	root := os.Getenv("XDG_STATE_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		root = filepath.Join(home, ".local", "state")
	}
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("XDG_STATE_HOME must be absolute")
	}
	return NewCache(filepath.Join(root, "grove", "footprint")), nil
}
func NewCache(directory string) *Cache { return &Cache{directory: directory} }
func (c *Cache) key(path string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(filepath.Clean(path))))
}
func (c *Cache) entryPath(path string) string { return filepath.Join(c.directory, c.key(path)+".json") }

// Load treats missing, corrupt, obsolete and replaced-root entries as misses.
// The cheap root fingerprint supplements a TTL; it cannot detect deep edits.
func (c *Cache) Load(request Request) (Entry, bool) {
	data, err := os.ReadFile(c.entryPath(request.Path))
	if err != nil {
		return Entry{}, false
	}
	var entry Entry
	if json.Unmarshal(data, &entry) != nil || entry.Version != Version || entry.ScannedAt.IsZero() || entry.Bytes < 0 || entry.Files < 0 {
		return Entry{}, false
	}
	request.Path = filepath.Clean(request.Path)
	if entry.Path != request.Path || entry.PrivateSize != request.PrivateSize || normalizeExclusions(entry.Exclude) != normalizeExclusions(request.Exclude) {
		return Entry{}, false
	}
	id, err := identify(request.Path)
	if err != nil || id.Device != entry.Identity.Device || id.Inode != entry.Identity.Inode || !id.Modified.Equal(entry.Identity.Modified) || !id.Created.Equal(entry.Identity.Created) {
		return Entry{}, false
	}
	return entry, true
}
func normalizeExclusions(paths []string) string {
	paths = append([]string(nil), paths...)
	sort.Strings(paths)
	return strings.Join(paths, "\x00")
}

// Measure waits for the per-root scan lease. A force request may reuse a scan
// completed since it started, so two simultaneous --refresh calls still coalesce.
func (c *Cache) Measure(ctx context.Context, request Request, force bool, maxAge time.Duration, workers int) (Entry, error) {
	requestedAt := time.Now()
	if !force {
		if entry, ok := c.Load(request); ok && !entry.Stale(requestedAt, maxAge) {
			return entry, nil
		}
	}
	if err := os.MkdirAll(filepath.Join(c.directory, "locks"), 0700); err != nil {
		return scanUncached(ctx, request, workers, fmt.Errorf("opening footprint cache: %w", err))
	}
	lock := flock.New(filepath.Join(c.directory, "locks", c.key(request.Path)+".lock"))
	locked, err := lock.TryLockContext(ctx, 20*time.Millisecond)
	if err != nil || !locked {
		_ = lock.Close()
		if ctx.Err() != nil {
			return Entry{}, ctx.Err()
		}
		if err == nil {
			err = fmt.Errorf("lock unavailable")
		}
		return scanUncached(ctx, request, workers, fmt.Errorf("locking footprint cache: %w", err))
	}
	defer lock.Close()
	if entry, ok := c.Load(request); ok && ((!force && !entry.Stale(time.Now(), maxAge)) || !entry.ScannedAt.Before(requestedAt)) {
		return entry, nil
	}
	entry, err := Scan(ctx, request, workers)
	if err != nil {
		return Entry{}, err
	}
	if err := c.store(entry); err != nil {
		return entry, fmt.Errorf("saving footprint: %w", err)
	}
	return entry, nil
}

// A broken optional cache must not hide a readable worktree. Without a lease,
// return a live result and warning but never race another writer's publication.
func scanUncached(ctx context.Context, request Request, workers int, warning error) (Entry, error) {
	entry, err := Scan(ctx, request, workers)
	if err != nil {
		return entry, err
	}
	return entry, warning
}

func (c *Cache) store(entry Entry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(c.directory, ".scan-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), c.entryPath(entry.Path))
}

// Prune only removes cache entries whose roots are confirmed absent, never
// entries merely outside the caller's selected repositories or inaccessible.
// Lock files stay put: replacing their inodes would split the process lease.
func (c *Cache) Prune() {
	entries, err := os.ReadDir(c.directory)
	if err != nil {
		return
	}
	for _, file := range entries {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		path := filepath.Join(c.directory, file.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var entry Entry
		if json.Unmarshal(data, &entry) != nil || !filepath.IsAbs(entry.Path) || filepath.Base(c.entryPath(entry.Path)) != file.Name() {
			continue
		}
		if _, err := os.Lstat(entry.Path); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		lock := flock.New(filepath.Join(c.directory, "locks", c.key(entry.Path)+".lock"))
		held, err := lock.TryLock()
		if held && err == nil {
			if _, err := os.Lstat(entry.Path); errors.Is(err, os.ErrNotExist) {
				_ = os.Remove(path)
			}
		}
		_ = lock.Close()
	}
}

// BackgroundLease deduplicates identical detached refresh scopes across shells.
// Different scopes retain their own lease so a scan of one repository cannot
// suppress another repository's refresh. Per-root locks still coalesce overlap.
// The caller owns release for its whole refresh, not just for process startup.
func (c *Cache) BackgroundLease(scope string) (release func(), acquired bool) {
	if os.MkdirAll(c.directory, 0700) != nil {
		return func() {}, false
	}
	lock := flock.New(filepath.Join(c.directory, "refresh-"+c.key(scope)+".lock"))
	held, err := lock.TryLock()
	if err != nil || !held {
		_ = lock.Close()
		return func() {}, false
	}
	return func() { _ = lock.Close() }, true
}
