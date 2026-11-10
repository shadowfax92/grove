package footprint

import (
	"context"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0644); err != nil {
		t.Fatal(err)
	}
}
func scan(t *testing.T, path string) Entry {
	t.Helper()
	entry, err := Scan(context.Background(), Request{Path: path}, 4)
	if err != nil {
		t.Fatal(err)
	}
	return entry
}
func TestScanMatchesDUAndReportsLargestAndNewest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "packages", "tool", "target", "object"), 131072)
	writeFile(t, filepath.Join(root, "source"), 100)
	newest := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := os.Chtimes(filepath.Join(root, "source"), newest, newest); err != nil {
		t.Fatal(err)
	}
	entry := scan(t, root)
	output, err := exec.Command("du", "-sk", root).Output()
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := strconv.ParseInt(strings.Fields(string(output))[0], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if got := (entry.Bytes + 1023) / 1024; got != blocks {
		t.Fatalf("scan = %d KiB, du = %d", got, blocks)
	}
	if entry.Files != 2 || !entry.NewestModTime.Equal(newest) {
		t.Fatalf("files/time = %d %v", entry.Files, entry.NewestModTime)
	}
	if len(entry.Largest) == 0 || entry.Largest[0].Path != filepath.Join("packages", "tool", "target") {
		t.Fatalf("largest = %#v", entry.Largest)
	}
}
func TestHardLinksCountOnce(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a", "one"), 65536)
	if err := os.Mkdir(filepath.Join(root, "b"), 0755); err != nil {
		t.Fatal(err)
	}
	before := scan(t, root)
	if err := os.Link(filepath.Join(root, "a", "one"), filepath.Join(root, "b", "two")); err != nil {
		t.Fatal(err)
	}
	after := scan(t, root)
	// Directory allocation can change on some filesystems; du is the authority.
	output, err := exec.Command("du", "-sk", root).Output()
	if err != nil {
		t.Fatal(err)
	}
	blocks, _ := strconv.ParseInt(strings.Fields(string(output))[0], 10, 64)
	if (after.Bytes+1023)/1024 != blocks || after.Files != before.Files {
		t.Fatalf("hardlink result: %#v, du %d", after, blocks)
	}
}
func TestSymlinkTargetsAreNeverCounted(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(outside, "large"), 1024*1024)
	before := scan(t, root)
	if err := os.Symlink(outside, filepath.Join(root, ".llm")); err != nil {
		t.Fatal(err)
	}
	after := scan(t, root)
	if after.Bytes-before.Bytes >= 1024*1024 || after.Files != 1 {
		t.Fatalf("followed external link: %#v", after)
	}
	if _, err := Scan(context.Background(), Request{Path: filepath.Join(root, ".llm")}, 2); err == nil {
		t.Fatal("accepted symlink root")
	}
}
func TestUnreadableDirectoryIsPartial(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	writeFile(t, filepath.Join(blocked, "secret"), 1024)
	writeFile(t, filepath.Join(root, "visible"), 1024)
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(blocked, 0755)
	entry := scan(t, root)
	if os.Geteuid() == 0 {
		t.Skip("root can read mode-000 directories")
	}
	if entry.IssueCount != 1 || len(entry.Issues) != 1 || entry.Issues[0].Path != "blocked" || entry.Files != 1 {
		t.Fatalf("partial scan = %#v", entry)
	}
}
func TestExcludesNestedWorktrees(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".wt", "child", "object"), 65536)
	entry, err := Scan(context.Background(), Request{Path: root, Exclude: []string{filepath.Join(".wt", "child")}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Files != 0 || entry.Bytes >= 65536 {
		t.Fatalf("included child: %#v", entry)
	}
}
func TestCancelledScanDoesNotReturnPartialSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, Request{Path: t.TempDir()}, 4); err != context.Canceled {
		t.Fatalf("error = %v", err)
	}
}
func TestPrivateSizeAndExternalHardlink(t *testing.T) {
	if !PrivateSizeSupported() {
		t.Skip("private size unavailable")
	}
	root := t.TempDir()
	path := filepath.Join(root, "file")
	writeFile(t, path, 65536)
	entry, err := Scan(context.Background(), Request{Path: root, PrivateSize: true}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if entry.PrivateBytes == nil || *entry.PrivateBytes != 65536 {
		t.Fatalf("private size = %#v", entry)
	}
	if err := os.Link(path, filepath.Join(t.TempDir(), "external")); err != nil {
		t.Fatal(err)
	}
	entry, err = Scan(context.Background(), Request{Path: root, PrivateSize: true}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if entry.PrivateBytes == nil || *entry.PrivateBytes != 0 {
		t.Fatalf("external hardlink charged as freeable: %#v", entry)
	}
}
func TestCacheReuseInvalidationCorruptionAndPrune(t *testing.T) {
	cache := NewCache(t.TempDir())
	root := filepath.Join(t.TempDir(), "tree")
	writeFile(t, filepath.Join(root, "deep", "file"), 4096)
	request := Request{Path: root}
	entry, err := cache.Measure(context.Background(), request, false, DefaultMaxAge, 2)
	if err != nil {
		t.Fatal(err)
	}
	again, err := cache.Measure(context.Background(), request, false, DefaultMaxAge, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !again.ScannedAt.Equal(entry.ScannedAt) {
		t.Fatal("fresh cache rescanned")
	}
	writeFile(t, filepath.Join(root, "deep", "file"), 8192)
	cached, ok := cache.Load(request)
	if !ok || cached.Bytes != entry.Bytes {
		t.Fatal("deep edits should rely on TTL")
	}
	if !cached.Stale(cached.ScannedAt.Add(DefaultMaxAge), DefaultMaxAge) {
		t.Fatal("TTL did not expire")
	}
	refreshed, err := cache.Measure(context.Background(), request, true, DefaultMaxAge, 2)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Bytes <= entry.Bytes {
		t.Fatal("forced refresh did not see edit")
	}
	if err := os.WriteFile(cache.entryPath(root), []byte("not JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Load(request); ok {
		t.Fatal("corrupt cache accepted")
	}
	if _, err := cache.Measure(context.Background(), request, false, DefaultMaxAge, 2); err != nil {
		t.Fatal(err)
	}
	old := root + "-old"
	if err := os.Rename(root, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Load(request); ok {
		t.Fatal("replaced root accepted")
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	cache.Prune()
	if _, err := os.Stat(cache.entryPath(root)); !os.IsNotExist(err) {
		t.Fatalf("cache not pruned: %v", err)
	}
}
func TestCacheProcesses(t *testing.T) {
	if os.Getenv("GROVE_FOOTPRINT_HELPER") == "1" {
		cache := NewCache(os.Getenv("GROVE_FOOTPRINT_CACHE"))
		entry, err := cache.Measure(context.Background(), Request{Path: os.Getenv("GROVE_FOOTPRINT_ROOT")}, false, DefaultMaxAge, 2)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println(entry.ScannedAt.Format(time.RFC3339Nano))
		return
	}
	root, cacheDir := t.TempDir(), t.TempDir()
	for i := 0; i < 50; i++ {
		writeFile(t, filepath.Join(root, fmt.Sprint(i), "file"), 4096)
	}
	commands := make([]*exec.Cmd, 2)
	outputs := make([]strings.Builder, 2)
	for i := range commands {
		commands[i] = exec.Command(os.Args[0], "-test.run=^TestCacheProcesses$")
		commands[i].Env = append(os.Environ(), "GROVE_FOOTPRINT_HELPER=1", "GROVE_FOOTPRINT_ROOT="+root, "GROVE_FOOTPRINT_CACHE="+cacheDir)
		commands[i].Stdout = &outputs[i]
		commands[i].Stderr = &outputs[i]
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for i, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("child %d: %v %s", i, err, outputs[i].String())
		}
	}
	first := strings.Split(outputs[0].String(), "\n")[0]
	second := strings.Split(outputs[1].String(), "\n")[0]
	if first != second {
		t.Fatalf("concurrent processes scanned twice: %q, %q", first, second)
	}
	if _, ok := NewCache(cacheDir).Load(Request{Path: root}); !ok {
		t.Fatal("concurrent cache unreadable")
	}
}

func TestBackgroundLeaseDoesNotSuppressOtherScopes(t *testing.T) {
	cache := NewCache(t.TempDir())
	release, ok := cache.BackgroundLease("first")
	if !ok {
		t.Fatal("first lease unavailable")
	}
	defer release()
	again, ok := cache.BackgroundLease("first")
	again()
	if ok {
		t.Fatal("duplicate scope acquired")
	}
	other, ok := cache.BackgroundLease("second")
	if !ok {
		t.Fatal("unrelated refresh suppressed")
	}
	other()
}

func TestDescriptorBudget(t *testing.T) {
	if os.Getenv("GROVE_FOOTPRINT_FD_HELPER") == "1" {
		var limit unix.Rlimit
		if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &limit); err != nil {
			t.Fatal(err)
		}
		limit.Cur = 64
		if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &limit); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for _, root := range strings.Split(os.Getenv("GROVE_FOOTPRINT_FD_ROOTS"), string(os.PathListSeparator)) {
			wg.Add(1)
			go func(root string) {
				defer wg.Done()
				entry, err := Scan(context.Background(), Request{Path: root}, 4)
				if err != nil || entry.IssueCount != 0 || entry.Files != 8 {
					t.Errorf("deep scan: files=%d issues=%v err=%v", entry.Files, entry.Issues, err)
				}
			}(root)
		}
		wg.Wait()
		return
	}
	var roots []string
	for i := 0; i < 4; i++ {
		root := t.TempDir()
		roots = append(roots, root)
		for j := 0; j < 8; j++ {
			path := filepath.Join(root, fmt.Sprint(j))
			for depth := 0; depth < 26; depth++ {
				path = filepath.Join(path, "d")
			}
			writeFile(t, filepath.Join(path, "file"), 1)
		}
	}
	command := exec.Command(os.Args[0], "-test.run=^TestDescriptorBudget$")
	command.Env = append(os.Environ(), "GROVE_FOOTPRINT_FD_HELPER=1", "GROVE_FOOTPRINT_FD_ROOTS="+strings.Join(roots, string(os.PathListSeparator)))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("bounded descriptor child: %v\n%s", err, output)
	}
}

func TestBrokenCacheLockStillMeasures(t *testing.T) {
	cache := NewCache(t.TempDir())
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "file"), 4096)
	if err := os.MkdirAll(filepath.Join(cache.directory, "locks"), 0700); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(cache.directory, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(blocker, "lock"), filepath.Join(cache.directory, "locks", cache.key(root)+".lock")); err != nil {
		t.Fatal(err)
	}
	entry, err := cache.Measure(context.Background(), Request{Path: root}, false, DefaultMaxAge, 2)
	if err == nil || entry.Bytes < 4096 || entry.Files != 1 {
		t.Fatalf("broken cache hid worktree: entry=%#v err=%v", entry, err)
	}
	if _, ok := cache.Load(Request{Path: root}); ok {
		t.Fatal("published without a lock")
	}
}
