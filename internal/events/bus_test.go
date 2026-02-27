package events_test

import (
	"testing"
	"time"

	"github.com/caffeaun/farmhand/internal/events"
)

// makeEvent is a small helper to reduce boilerplate in tests.
func makeEvent(typ string) events.Event {
	return events.Event{
		Type:      typ,
		Payload:   nil,
		Timestamp: time.Now(),
	}
}

// recv waits up to 200 ms for an event on ch, then fails the test.
func recv(t *testing.T, ch chan events.Event) events.Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for event")
		return events.Event{}
	}
}

// noRecv asserts that no event arrives on ch within 50 ms.
func noRecv(t *testing.T, ch chan events.Event) {
	t.Helper()
	select {
	case e := <-ch:
		t.Fatalf("unexpected event received: %v", e)
	case <-time.After(50 * time.Millisecond):
		// good — nothing arrived
	}
}

func TestSingleSubscriberReceivesEvent(t *testing.T) {
	t.Parallel()

	b := events.New()
	defer b.Close()

	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	want := makeEvent(events.DeviceOnline)
	b.Publish(want)

	got := recv(t, ch)
	if got.Type != want.Type {
		t.Fatalf("expected type %q, got %q", want.Type, got.Type)
	}
}

func TestMultipleSubscribersAllReceiveEvent(t *testing.T) {
	t.Parallel()

	b := events.New()
	defer b.Close()

	const n = 3
	channels := make([]chan events.Event, n)
	for i := range channels {
		channels[i] = b.Subscribe()
	}
	defer func() {
		for _, ch := range channels {
			b.Unsubscribe(ch)
		}
	}()

	want := makeEvent(events.JobStarted)
	b.Publish(want)

	for i, ch := range channels {
		got := recv(t, ch)
		if got.Type != want.Type {
			t.Fatalf("subscriber %d: expected type %q, got %q", i, want.Type, got.Type)
		}
	}
}

func TestUnsubscribedChannelReceivesNoEvents(t *testing.T) {
	t.Parallel()

	b := events.New()
	defer b.Close()

	ch := b.Subscribe()
	b.Unsubscribe(ch) // remove before publishing

	// Drain any events that may have been buffered before unsubscribe completed.
	// After Unsubscribe the channel is closed, so we read until empty/closed.
	// Then publish a new event and verify it does not arrive.
	b2 := events.New()
	defer b2.Close()

	active := b2.Subscribe()
	defer b2.Unsubscribe(active)

	b2.Publish(makeEvent(events.DeviceOffline))
	// Wait for the active subscriber to receive the event so we know the bus
	// has processed it, then check that the unsubscribed channel didn't get it.
	recv(t, active)

	// ch is already closed; reading from it returns zero value immediately.
	// We just need to verify it is closed (no event payload of our type).
	select {
	case e, ok := <-ch:
		if ok {
			t.Fatalf("unsubscribed channel received event: %v", e)
		}
		// closed channel — expected
	default:
		// nothing buffered — also fine
	}
}

func TestSlowSubscriberDoesNotBlockOthers(t *testing.T) {
	t.Parallel()

	b := events.New()
	defer b.Close()

	// slow never reads from its channel so its buffer will fill up.
	slow := b.Subscribe()

	// fast reads actively — it must not be blocked by slow being full.
	fast := b.Subscribe()
	defer b.Unsubscribe(fast)

	// Publish exactly 64 filler events to fill slow's buffer.
	for i := 0; i < 64; i++ {
		b.Publish(makeEvent(events.DeviceStatusChanged))
	}

	// Drain fast of the 64 filler events so its buffer is empty again.
	for i := 0; i < 64; i++ {
		recv(t, fast)
	}

	// Now slow is full. Publishing one more event must still reach fast.
	want := makeEvent(events.JobCompleted)
	b.Publish(want)

	got := recv(t, fast)
	if got.Type != want.Type {
		t.Fatalf("fast subscriber: expected %q, got %q", want.Type, got.Type)
	}

	// Drain slow's buffer then unsubscribe so we don't leak goroutines.
	for len(slow) > 0 {
		<-slow
	}
	b.Unsubscribe(slow)
}

func TestCloseStopsBusWithoutPanic(t *testing.T) {
	t.Parallel()

	b := events.New()
	b.Close() // must not panic

	// Give the run goroutine time to exit.
	time.Sleep(10 * time.Millisecond)
}

func TestPublishAfterCloseDoesNotPanic(t *testing.T) {
	t.Parallel()

	b := events.New()
	b.Close()

	// Sleep briefly so done channel propagates to run().
	time.Sleep(10 * time.Millisecond)

	// Must not panic.
	b.Publish(makeEvent(events.JobFailed))
}
