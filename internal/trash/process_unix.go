//go:build darwin || linux

package trash

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gofrs/flock"
	"golang.org/x/sys/unix"
)

// Resume persists a wakeup under the lifecycle lock, then starts a child only
// when the process lease is free. The already-locked descriptor crosses exec:
// there is no startup gap in which another command can launch a second reaper.
func (m *Manager) Resume() error {
	if !m.hasEntries() {
		return nil
	}
	lifecycle := flock.New(filepath.Join(m.State, "lifecycle.lock"))
	if err := lifecycle.Lock(); err != nil {
		return err
	}
	defer lifecycle.Close()
	if err := writeJSON(filepath.Join(m.State, "request.json"), token()); err != nil {
		return err
	}
	lease, err := m.tryLease()
	if err != nil {
		return err
	}
	if lease == nil {
		return nil
	}
	defer lease.Close()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(executable, "__grove-reap")
	cmd.Dir = "/"
	cmd.Env = append(os.Environ(), "GROVE_REAPER_STATE="+m.State, "GROVE_REAPER_INHERITED=1")
	cmd.ExtraFiles = []*os.File{lease}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// hasEntries is only a cheap wakeup hint; it neither trusts journals nor waits
// on root locks held by a detach. The child validates everything before deletion.
func (m *Manager) hasEntries() bool {
	indexes, err := os.ReadDir(filepath.Join(m.State, "roots"))
	if err != nil {
		return false
	}
	for _, index := range indexes {
		var root rootRecord
		if readJSON(filepath.Join(m.State, "roots", index.Name()), &root) != nil {
			continue
		}
		entries, err := os.ReadDir(root.Path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if len(entry.Name()) > 6 && entry.Name()[:6] == "entry-" {
				return true
			}
		}
	}
	return false
}
func (m *Manager) tryLease() (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(m.State, "reaper.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, nil
		}
		return nil, err
	}
	return f, nil
}

// Reap waits for the same lease used by background workers. A waiter can report
// errors directly; failed background attempts retain journals for the next wakeup.
func (m *Manager) Reap(ctx context.Context) error {
	if err := os.MkdirAll(m.State, 0700); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		lease, err := m.tryLease()
		if err != nil {
			return err
		}
		if lease != nil {
			return m.drain(ctx, lease)
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// ReapInherited takes ownership of descriptor 3 passed by Resume. Closing the
// parent's copy never unlocks it; the child keeps the lease across binary exec.
func (m *Manager) ReapInherited(ctx context.Context) error {
	return m.drain(ctx, os.NewFile(3, "grove-reaper-lease"))
}

func (m *Manager) request() string {
	var request string
	_ = readJSON(filepath.Join(m.State, "request.json"), &request)
	return request
}
func (m *Manager) drain(ctx context.Context, lease *os.File) error {
	defer lease.Close()
	var failures []error
	for {
		before := m.request()
		if err := m.reapOnce(ctx); err != nil {
			failures = append(failures, err)
		}
		lifecycle := flock.New(filepath.Join(m.State, "lifecycle.lock"))
		if err := lifecycle.Lock(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		// Exit and wakeup use the same lock. A caller either changes the generation
		// before this check, or observes the released lease and starts the next child.
		// Thus one process drains new work without the usual lost-wakeup race.
		if before == m.request() || ctx.Err() != nil {
			closeErr := lease.Close()
			_ = lifecycle.Close()
			return errors.Join(append(failures, closeErr, ctx.Err())...)
		}
		_ = lifecycle.Close()
	}
}
