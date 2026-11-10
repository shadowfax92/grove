// Package footprint measures allocated worktree space without following links.
// Its cache is disposable presentation state; Git remains the inventory authority.
package footprint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const Version = 1
const DefaultMaxAge = 15 * time.Minute

// Request defines one accounting boundary. Exclude contains relative paths of
// nested registered worktrees, so their bytes belong to their own measurements.
type Request struct {
	Path        string   `json:"path"`
	Exclude     []string `json:"exclude,omitempty"`
	PrivateSize bool     `json:"private_size"`
}

type Directory struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Identity invalidates a reused path or changed root without traversing it.
// Deep edits cannot be detected this way; MaxAge bounds their cached lifetime.
type Identity struct {
	Device   uint64    `json:"device"`
	Inode    uint64    `json:"inode"`
	Modified time.Time `json:"modified"`
	Created  time.Time `json:"created"`
}

// Entry carries a complete scan, including partial-read diagnostics. PrivateBytes
// is an estimate from filesystem metadata, not a promise of reclaimed volume
// space (snapshots and concurrent clone changes are outside this walk).
type Entry struct {
	Version int `json:"version"`
	Request
	Identity           Identity      `json:"identity"`
	ScannedAt          time.Time     `json:"scanned_at"`
	Duration           time.Duration `json:"duration_ns"`
	Bytes              int64         `json:"bytes"`
	Files              int64         `json:"files"`
	NewestModTime      time.Time     `json:"newest_mod_time"`
	Largest            []Directory   `json:"largest_directories"`
	PrivateBytes       *int64        `json:"private_bytes,omitempty"`
	PrivateUnavailable int64         `json:"private_unavailable,omitempty"`
	IssueCount         int64         `json:"issue_count"`
	Issues             []Issue       `json:"issues"`
}

func (e Entry) Stale(now time.Time, maxAge time.Duration) bool {
	return e.ScannedAt.After(now) || now.Sub(e.ScannedAt) >= maxAge
}

type inode struct{ device, number uint64 }
type linkCount struct {
	seen, total uint64
	private     int64
}
type directoryNode struct {
	path     string
	file     *os.File
	bytes    int64
	files    int64
	newest   time.Time
	children []*directoryNode
}

type walker struct {
	rootFD      int
	ctx         context.Context
	request     Request
	jobs        chan *directoryNode
	pending     sync.WaitGroup
	mu          sync.Mutex
	links       map[inode]*linkCount
	issues      []Issue
	issueCount  int64
	private     int64
	unavailable int64
}

// Scan runs a bounded directory work queue anchored to one root descriptor.
// Jobs carry paths, not open handles, and close a directory before scheduling
// children. This bounds descriptors by active workers, independent of tree depth.
// Every component is opened without following links, including during renames.
func Scan(ctx context.Context, request Request, workers int) (Entry, error) {
	started := time.Now()
	request.Path = filepath.Clean(request.Path)
	request.Exclude = append([]string(nil), request.Exclude...)
	sort.Strings(request.Exclude)
	id, err := identify(request.Path)
	if err != nil {
		return Entry{}, err
	}
	fd, err := unix.Open(request.Path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return Entry{}, err
	}
	rootFile := os.NewFile(uintptr(fd), request.Path)
	defer rootFile.Close()
	var rootStat unix.Stat_t
	if err := unix.Fstat(fd, &rootStat); err != nil {
		return Entry{}, err
	}
	id.Device, id.Inode, id.Modified = uint64(rootStat.Dev), uint64(rootStat.Ino), statModTime(&rootStat)
	root := &directoryNode{path: "."}
	if workers < 1 {
		workers = 4
	}
	w := &walker{rootFD: fd, ctx: ctx, request: request, jobs: make(chan *directoryNode, workers*2), links: make(map[inode]*linkCount)}
	w.account(root, ".", &rootStat, true, nil)
	w.pending.Add(1)
	w.jobs <- root
	var running sync.WaitGroup
	for i := 0; i < workers; i++ {
		running.Add(1)
		go func() {
			defer running.Done()
			for node := range w.jobs {
				w.walk(node)
				w.pending.Done()
			}
		}()
	}
	w.pending.Wait()
	close(w.jobs)
	running.Wait()
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	entry := Entry{Version: Version, Request: request, Identity: id, ScannedAt: time.Now().UTC(), Duration: time.Since(started), Largest: []Directory{}, Issues: w.issues, IssueCount: w.issueCount, PrivateUnavailable: w.unavailable}
	var directories []Directory
	var sum func(*directoryNode)
	sum = func(node *directoryNode) {
		for _, child := range node.children {
			sum(child)
			node.bytes += child.bytes
			node.files += child.files
			if child.newest.After(node.newest) {
				node.newest = child.newest
			}
		}
		// Suppress wrapper directories almost entirely occupied by one child. This
		// exposes packages/tool/target instead of using all five slots on its parents.
		dominated := false
		for _, child := range node.children {
			if child.bytes > 0 && float64(child.bytes) >= float64(node.bytes)*0.9 {
				dominated = true
				break
			}
		}
		if node.path != "." && !dominated {
			directories = append(directories, Directory{node.path, node.bytes})
		}
	}
	sum(root)
	entry.Bytes, entry.Files, entry.NewestModTime = root.bytes, root.files, root.newest
	sort.Slice(directories, func(i, j int) bool {
		if directories[i].Bytes == directories[j].Bytes {
			return directories[i].Path < directories[j].Path
		}
		return directories[i].Bytes > directories[j].Bytes
	})
	if len(directories) > 5 {
		directories = directories[:5]
	}
	entry.Largest = directories
	if entry.Largest == nil {
		entry.Largest = []Directory{}
	}
	if entry.Issues == nil {
		entry.Issues = []Issue{}
	}
	sort.Slice(entry.Issues, func(i, j int) bool { return entry.Issues[i].Path < entry.Issues[j].Path })
	if request.PrivateSize && PrivateSizeSupported() && w.unavailable == 0 {
		// External hardlinks survive deletion of this root. Only charge private data
		// when every link to that inode was encountered inside the accounting boundary.
		for _, link := range w.links {
			if link.seen == link.total {
				w.private += link.private
			}
		}
		entry.PrivateBytes = &w.private
	}
	return entry, nil
}

func (w *walker) issue(path string, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.issueCount++
	if len(w.issues) < 32 {
		w.issues = append(w.issues, Issue{path, err.Error()})
	}
}

func (w *walker) account(node *directoryNode, name string, st *unix.Stat_t, isDir bool, private *int64) {
	modified := statModTime(st)
	if modified.After(node.newest) {
		node.newest = modified
	}
	key := inode{uint64(st.Dev), uint64(st.Ino)}
	if !isDir && st.Nlink > 1 {
		w.mu.Lock()
		if link, seen := w.links[key]; seen {
			link.seen++
			w.mu.Unlock()
			return
		}
		w.links[key] = &linkCount{seen: 1, total: uint64(st.Nlink)}
		w.mu.Unlock()
	}
	node.bytes += st.Blocks * 512
	if !isDir {
		node.files++
	}
	if w.request.PrivateSize && PrivateSizeSupported() && st.Mode&unix.S_IFMT == unix.S_IFREG {
		var size int64
		var err error
		if private != nil {
			size = *private
		} else {
			size, err = privateSizeAt(int(node.file.Fd()), name)
		}
		w.mu.Lock()
		if err != nil {
			w.unavailable++
		} else if st.Nlink > 1 {
			w.links[key].private = size
		} else {
			w.private += size
		}
		w.mu.Unlock()
	}
}

func (w *walker) walk(node *directoryNode) {
	if w.ctx.Err() != nil {
		return
	}
	fd, err := openDirectoryAt(w.rootFD, node.path)
	if err != nil {
		w.issue(node.path, err)
		return
	}
	node.file = os.NewFile(uintptr(fd), node.path)
	err = readEntries(w.ctx, node.file, w.request.PrivateSize, func(name string, childStat *unix.Stat_t, private *int64) {
		path := filepath.Join(node.path, name)
		for _, skip := range w.request.Exclude {
			if path == skip {
				return
			}
		}
		if childStat == nil {
			w.issue(path, fmt.Errorf("could not read entry metadata"))
			return
		}
		if childStat.Mode&unix.S_IFMT != unix.S_IFDIR {
			w.account(node, name, childStat, false, private)
			return
		}
		child := &directoryNode{path: path}
		w.account(child, ".", childStat, true, nil)
		node.children = append(node.children, child)
	})
	// Inline processing of a full queue must happen after close: retaining the
	// parent here would turn a deep tree into one open descriptor per ancestor.
	_ = node.file.Close()
	node.file = nil
	if err != nil && w.ctx.Err() == nil {
		w.issue(node.path, err)
	}
	for _, child := range node.children {
		w.pending.Add(1)
		select {
		case w.jobs <- child:
		default:
			w.walk(child)
			w.pending.Done()
		}
	}
}

func identify(path string) (Identity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Identity{}, err
	}
	if !info.IsDir() {
		return Identity{}, fmt.Errorf("worktree root is not a directory: %s", path)
	}
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return Identity{}, err
	}
	id := Identity{Device: uint64(st.Dev), Inode: uint64(st.Ino), Modified: info.ModTime()}
	if git, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
		id.Created = git.ModTime()
	}
	return id, nil
}

// Exclusions returns nested root paths relative to path, with stable ordering.
func Exclusions(path string, roots []string) []string {
	var result []string
	for _, root := range roots {
		rel, err := filepath.Rel(path, root)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			result = append(result, rel)
		}
	}
	sort.Strings(result)
	return result
}
