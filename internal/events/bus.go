package events

const subscriberBufSize = 64

// Bus is a publish/subscribe event bus using channels.
// A single run() goroutine owns the subscribers map so no mutex is needed.
type Bus struct {
	subscribers map[chan Event]struct{}
	register    chan chan Event
	unregister  chan chan Event
	broadcast   chan Event
	done        chan struct{}
}

// New creates and starts a new event Bus.
func New() *Bus {
	b := &Bus{
		subscribers: make(map[chan Event]struct{}),
		register:    make(chan chan Event),
		unregister:  make(chan chan Event),
		broadcast:   make(chan Event),
		done:        make(chan struct{}),
	}
	go b.run()
	return b
}

// run is the main loop that serialises register, unregister, and broadcast
// operations so the subscribers map never needs a mutex.
func (b *Bus) run() {
	for {
		select {
		case ch := <-b.register:
			b.subscribers[ch] = struct{}{}

		case ch := <-b.unregister:
			if _, ok := b.subscribers[ch]; ok {
				delete(b.subscribers, ch)
				close(ch)
			}

		case event := <-b.broadcast:
			for ch := range b.subscribers {
				// Non-blocking send: a slow or full subscriber is dropped for
				// this event rather than blocking delivery to others.
				select {
				case ch <- event:
				default:
				}
			}

		case <-b.done:
			return
		}
	}
}

// Publish sends an event to all current subscribers.
// It blocks only until run() picks up the event, which is very fast.
func (b *Bus) Publish(event Event) {
	select {
	case b.broadcast <- event:
	case <-b.done:
		// Bus is closed; discard the event rather than panic or deadlock.
	}
}

// Subscribe returns a buffered channel that receives every published event.
// The caller must call Unsubscribe when done to avoid leaking resources.
func (b *Bus) Subscribe() chan Event {
	ch := make(chan Event, subscriberBufSize)
	b.register <- ch
	return ch
}

// Unsubscribe removes ch from the bus and closes it.
// The caller must not send to ch after this call.
func (b *Bus) Unsubscribe(ch chan Event) {
	b.unregister <- ch
}

// Close stops the bus's run goroutine. Subsequent Publish calls are silently
// dropped. Close must be called exactly once.
func (b *Bus) Close() {
	close(b.done)
}
