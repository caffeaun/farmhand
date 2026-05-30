package job

import (
	"context"
	"sync"
)

// CancelRegistry tracks the cancel functions of in-flight jobs so that an
// external caller (the DELETE /jobs/:id handler) can stop them mid-run.
//
// The registry is in-memory only — entries do NOT persist across process
// restarts. createJob registers an entry, deleteJob cancels it, and the
// runner goroutine wrapper removes the entry when the run ends naturally.
type CancelRegistry struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

// NewCancelRegistry returns an initialised *CancelRegistry ready for use.
func NewCancelRegistry() *CancelRegistry {
	return &CancelRegistry{
		cancels: make(map[string]context.CancelFunc),
	}
}

// Register associates jobID with cancel. If jobID is already registered the
// old cancel func is replaced atomically without calling it.
func (r *CancelRegistry) Register(jobID string, cancel context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancels[jobID] = cancel
}

// Cancel calls the cancel func registered for jobID and removes the entry,
// both under the same lock to guarantee at-most-once invocation.
// It returns true if an entry existed, false otherwise.
func (r *CancelRegistry) Cancel(jobID string) bool {
	r.mu.Lock()
	cancel, ok := r.cancels[jobID]
	if ok {
		delete(r.cancels, jobID)
	}
	r.mu.Unlock()

	if ok {
		cancel()
	}
	return ok
}

// Remove deletes the entry for jobID without calling the cancel func.
// It is a no-op if jobID is not registered.
func (r *CancelRegistry) Remove(jobID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cancels, jobID)
}

// Has reports whether jobID has a registered cancel func.
func (r *CancelRegistry) Has(jobID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.cancels[jobID]
	return ok
}
