package cleanup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// Schedule coordinates detached sweeps across short-lived CLI processes. A
// reservation limits launches; a separate process lease serializes whole sweeps.
// Neither timestamp nor cached measurements are authority to remove a worktree.
type Schedule struct{ Directory string }
type SweepRemoval struct {
	Selector string `json:"selector"`
	Path     string `json:"path"`
	Bytes    *int64 `json:"bytes,omitempty"`
	Reason   string `json:"reason"`
}
type SweepReport struct {
	Started  time.Time      `json:"started"`
	Finished time.Time      `json:"finished"`
	Removed  []SweepRemoval `json:"removed"`
	Error    string         `json:"error,omitempty"`
}

func DefaultSchedule() (*Schedule, error) {
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
	return &Schedule{Directory: filepath.Join(root, "grove", "cleanup")}, nil
}
func (s *Schedule) lock(name string) (*flock.Flock, error) {
	if err := os.MkdirAll(s.Directory, 0700); err != nil {
		return nil, err
	}
	return flock.New(filepath.Join(s.Directory, name+".lock")), nil
}
func (s *Schedule) Claim(now time.Time, interval time.Duration) (bool, error) {
	lock, err := s.lock("schedule")
	if err != nil {
		return false, err
	}
	defer lock.Close()
	held, err := lock.TryLock()
	if err != nil || !held {
		return false, err
	}
	path := filepath.Join(s.Directory, "last-start")
	if data, err := os.ReadFile(path); err == nil {
		last, err := time.Parse(time.RFC3339Nano, string(data))
		if err == nil && now.Sub(last) < interval {
			return false, nil
		}
	}
	return true, os.WriteFile(path, []byte(now.Format(time.RFC3339Nano)), 0600)
}
func (s *Schedule) Lease() (func(), bool, error) {
	lock, err := s.lock("sweep")
	if err != nil {
		return func() {}, false, err
	}
	held, err := lock.TryLock()
	if err != nil || !held {
		lock.Close()
		return func() {}, false, err
	}
	return func() { _ = lock.Close() }, true, nil
}
func (s *Schedule) Record(report SweepReport) error {
	lock, err := s.lock("report")
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = lock.Lock(); err != nil {
		return err
	}
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(s.Directory, "sweeps.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = log.Write(append(data, '\n'))
	if err == nil {
		err = log.Sync()
	}
	closeErr := log.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	temp, err := os.CreateTemp(s.Directory, ".report-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), filepath.Join(s.Directory, "last-report.json"))
}

// Notice consumes a completed report only for an interactive caller. Holding the
// report lock makes two simultaneous shells mention it once, on stderr.
func (s *Schedule) Notice() (SweepReport, bool) {
	lock, err := s.lock("report")
	if err != nil {
		return SweepReport{}, false
	}
	defer lock.Close()
	held, err := lock.TryLock()
	if err != nil || !held {
		return SweepReport{}, false
	}
	data, err := os.ReadFile(filepath.Join(s.Directory, "last-report.json"))
	if err != nil {
		return SweepReport{}, false
	}
	var report SweepReport
	if json.Unmarshal(data, &report) != nil {
		return report, false
	}
	key := report.Finished.Format(time.RFC3339Nano)
	old, _ := os.ReadFile(filepath.Join(s.Directory, "noticed"))
	if string(old) == key {
		return report, false
	}
	if os.WriteFile(filepath.Join(s.Directory, "noticed"), []byte(key), 0600) != nil {
		return report, false
	}
	return report, true
}
