package cleanup

import (
	"sync"
	"time"
)

// CollectionState is presentation-only progress for independent signal stages.
// ActivityDuration and MergeDuration overlap; their sum is not elapsed time.
type CollectionState struct {
	ProcessesDone                                    bool
	Worktrees, WorktreesDone                         int
	Repositories, RepositoriesDone                   int
	ProcessDuration, ActivityDuration, MergeDuration time.Duration
}

// Progress separates synchronized counters from Row ownership. The command can
// poll Snapshot while collectors work, without workers writing terminal streams.
// A nil tracker disables observation for background/other consumers.
type Progress struct {
	mu    sync.Mutex
	state CollectionState
}

func (p *Progress) update(change func(*CollectionState)) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	change(&p.state)
}
func (p *Progress) Snapshot() CollectionState {
	if p == nil {
		return CollectionState{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}
