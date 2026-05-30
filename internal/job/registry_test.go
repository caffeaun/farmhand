package job

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCancelRegistry_NewRegistry verifies that NewCancelRegistry returns a
// non-nil, ready-to-use registry.
func TestCancelRegistry_NewRegistry(t *testing.T) {
	r := NewCancelRegistry()
	require.NotNil(t, r)
	require.NotNil(t, r.cancels)
}

// TestCancelRegistry_CancelUnknown verifies that Cancel on an unknown ID
// returns false and does not panic or modify the registry.
func TestCancelRegistry_CancelUnknown(t *testing.T) {
	r := NewCancelRegistry()
	ok := r.Cancel("does-not-exist")
	assert.False(t, ok)
	assert.False(t, r.Has("does-not-exist"))
}

// TestCancelRegistry_CancelKnown verifies that Cancel on a registered ID
// returns true, invokes the cancel func exactly once, and removes the entry
// so a second Cancel returns false.
func TestCancelRegistry_CancelKnown(t *testing.T) {
	r := NewCancelRegistry()

	var calls atomic.Int32
	cancel := func() { calls.Add(1) }

	r.Register("job-1", cancel)
	assert.True(t, r.Has("job-1"))

	// First cancel: must return true and call cancel once.
	ok := r.Cancel("job-1")
	assert.True(t, ok)
	assert.Equal(t, int32(1), calls.Load(), "cancel func must be called exactly once")
	assert.False(t, r.Has("job-1"), "entry must be removed after Cancel")

	// Second cancel: must return false and NOT call cancel again.
	ok2 := r.Cancel("job-1")
	assert.False(t, ok2)
	assert.Equal(t, int32(1), calls.Load(), "cancel func must not be called a second time")
}

// TestCancelRegistry_Remove verifies that Remove deletes the entry and
// subsequent Cancel / Has return false.
func TestCancelRegistry_Remove(t *testing.T) {
	r := NewCancelRegistry()

	var called atomic.Bool
	r.Register("job-2", func() { called.Store(true) })
	require.True(t, r.Has("job-2"))

	r.Remove("job-2")
	assert.False(t, r.Has("job-2"))
	assert.False(t, r.Cancel("job-2"))
	assert.False(t, called.Load(), "cancel func must not be called after Remove")
}

// TestCancelRegistry_RegisterReplaces verifies that re-registering an ID
// atomically replaces the cancel func without calling the old one.
func TestCancelRegistry_RegisterReplaces(t *testing.T) {
	r := NewCancelRegistry()

	// Register a cancel func that panics — it must never be called.
	panicCancel := func() { panic("old cancel func must not be called") }
	r.Register("job-3", panicCancel)

	// Replace with a safe cancel func.
	var called atomic.Bool
	safeCancel := func() { called.Store(true) }
	r.Register("job-3", safeCancel) // must NOT invoke panicCancel

	// Now Cancel should invoke only the safe func.
	ok := r.Cancel("job-3")
	assert.True(t, ok)
	assert.True(t, called.Load())
}

// TestCancelRegistry_Has verifies that Has reflects the current state.
func TestCancelRegistry_Has(t *testing.T) {
	r := NewCancelRegistry()

	assert.False(t, r.Has("job-4"))

	r.Register("job-4", func() {})
	assert.True(t, r.Has("job-4"))

	r.Cancel("job-4")
	assert.False(t, r.Has("job-4"))

	r.Register("job-5", func() {})
	assert.True(t, r.Has("job-5"))

	r.Remove("job-5")
	assert.False(t, r.Has("job-5"))
}

// TestCancelRegistry_AtomicCancel verifies that concurrent calls to
// Cancel(sameID) result in the cancel func being called at most once.
func TestCancelRegistry_AtomicCancel(t *testing.T) {
	r := NewCancelRegistry()

	var calls atomic.Int32
	r.Register("job-atomic", func() { calls.Add(1) })

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			r.Cancel("job-atomic")
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), calls.Load(), "cancel func must be called exactly once despite concurrent cancels")
	assert.False(t, r.Has("job-atomic"))
}

// TestCancelRegistry_ConcurrentAccess stress-tests all methods across
// multiple goroutines to satisfy the data-race acceptance criterion.
// Run with: go test -race ./internal/job/... -run TestCancelRegistry
func TestCancelRegistry_ConcurrentAccess(t *testing.T) {
	r := NewCancelRegistry()

	const goroutines = 10
	const iterations = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := range goroutines {
		go func(id int) {
			defer wg.Done()
			jobID := "concurrent-job"
			for i := range iterations {
				switch (id + i) % 4 {
				case 0:
					_, cancel := context.WithCancel(context.Background())
					r.Register(jobID, cancel)
				case 1:
					r.Cancel(jobID)
				case 2:
					r.Remove(jobID)
				case 3:
					r.Has(jobID)
				}
			}
		}(g)
	}

	wg.Wait()
	// No assertions needed beyond reaching here without race detector firing.
}

// TestCancelRegistry_ContextCancelFunc ensures the registry integrates
// correctly with real context.CancelFuncs from context.WithCancel.
func TestCancelRegistry_ContextCancelFunc(t *testing.T) {
	r := NewCancelRegistry()

	ctx, cancel := context.WithCancel(context.Background())
	r.Register("ctx-job", cancel)

	assert.Nil(t, ctx.Err(), "context must not be cancelled yet")

	ok := r.Cancel("ctx-job")
	assert.True(t, ok)
	assert.ErrorIs(t, ctx.Err(), context.Canceled, "context must be cancelled after Cancel")
	assert.False(t, r.Has("ctx-job"))
}
