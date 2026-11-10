package activity

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"time"
)

func workingFiles(ctx context.Context, root string, data []byte) (time.Time, error) {
	groups := make(map[string]map[string]bool)
	for _, raw := range bytes.Split(data, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		name := string(raw)
		dir := filepath.Dir(name)
		if groups[dir] == nil {
			groups[dir] = make(map[string]bool)
		}
		groups[dir][filepath.Base(name)] = true
	}
	// Parent checks are shared by all files in a directory. A tracked directory
	// replaced by a symlink must not redirect measurement into a context store.
	directories := map[string]bool{".": true}
	var inTree func(string) (bool, error)
	inTree = func(dir string) (bool, error) {
		if ok, known := directories[dir]; known {
			return ok, nil
		}
		parent, err := inTree(filepath.Dir(dir))
		if err != nil || !parent {
			return false, err
		}
		info, err := os.Lstat(filepath.Join(root, dir))
		if os.IsNotExist(err) {
			directories[dir] = false
			return false, nil
		}
		if err != nil {
			return false, err
		}
		ok := info.IsDir() && info.Mode()&os.ModeSymlink == 0
		directories[dir] = ok
		return ok, nil
	}
	var newest time.Time
	for dir, names := range groups {
		if err := ctx.Err(); err != nil {
			return newest, err
		}
		inside, err := inTree(dir)
		if err != nil {
			return newest, err
		}
		if !inside {
			continue
		}
		timestamp, err := directoryTime(ctx, filepath.Join(root, dir), names)
		if err != nil {
			return newest, err
		}
		if timestamp.After(newest) {
			newest = timestamp
		}
	}
	return newest, nil
}

func statTime(ctx context.Context, dir string, names map[string]bool) (time.Time, error) {
	var newest time.Time
	for name := range names {
		if err := ctx.Err(); err != nil {
			return newest, err
		}
		info, err := os.Lstat(filepath.Join(dir, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return newest, err
		}
		if !info.IsDir() && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest, nil
}
