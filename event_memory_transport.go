package doger

import (
	"context"
	"log"
	"maps"
	"slices"
	"sync"
)

// MemoryEventTransport is an [EventTransport] delivering within one process,
// for tests and single-process applications; see
// [NewMemoryEventTransport]. It keeps nothing across restarts: it is not
// crash-safe, and it has no outbox (it does not implement TxEventSender).
type MemoryEventTransport struct {
	maxAttempts int
	logf        func(format string, args ...any)

	mu     sync.Mutex
	groups map[string]map[string]*memoryGroup // by destination, then group name

	pending inflightCounter // envelopes queued for a subscription or being handled
}

var _ EventTransport = (*MemoryEventTransport)(nil)

// MemoryTransportOption configures a [MemoryEventTransport].
type MemoryTransportOption func(*MemoryEventTransport)

// MemoryMaxAttempts sets how many times an envelope is handed to a
// handler that keeps returning a non-permanent error before it is dropped
// and logged. Default: 3. NewMemoryEventTransport panics with "doger:
// invalid MemoryMaxAttempts <n>" for n < 1.
func MemoryMaxAttempts(n int) MemoryTransportOption {
	return func(t *MemoryEventTransport) {
		t.maxAttempts = n
	}
}

// MemoryLogger sets where dropped envelopes are logged. Default: the
// standard log package's Printf. A nil logf keeps the default.
func MemoryLogger(logf func(format string, args ...any)) MemoryTransportOption {
	return func(t *MemoryEventTransport) {
		if logf != nil {
			t.logf = logf
		}
	}
}

// defaultMemoryMaxAttempts is the default of MemoryMaxAttempts.
const defaultMemoryMaxAttempts = 3

// NewMemoryEventTransport returns an in-memory transport. Each envelope
// sent to a destination goes to one subscription of each group subscribed
// to it, round robin within a group. A new group starts at the latest
// event: envelopes sent before it existed never reach it. A group whose
// subscriptions all stopped keeps the envelopes sent meanwhile for its next
// subscription, unless it is ephemeral, in which case it ends. Each
// subscription handles its envelopes one at a time, on a goroutine of its
// own, with a context cancelled when its stop context ends; the envelope
// that cancellation interrupts is kept for the group like a queued one. A
// handler
// error is retried at once, up to MemoryMaxAttempts attempts; a permanent
// error (see [IsPermanentError]) is not retried. Either way the envelope is
// then dropped and logged.
func NewMemoryEventTransport(opts ...MemoryTransportOption) *MemoryEventTransport {
	t := &MemoryEventTransport{
		maxAttempts: defaultMemoryMaxAttempts,
		logf:        log.Printf,
		groups:      map[string]map[string]*memoryGroup{},
	}
	applyOptions(t, opts)
	if t.maxAttempts < 1 {
		panic(libErrorf("invalid MemoryMaxAttempts %d", t.maxAttempts))
	}

	return t
}

// memoryGroup is a group of subscriptions to one destination.
type memoryGroup struct {
	ephemeral bool
	members   []*memorySubscription
	cursor    int
	backlog   []Envelope // sent while the group had no member
}

// join adds member to the group and hands it the backlog.
func (g *memoryGroup) join(member *memorySubscription) {
	g.members = append(g.members, member)
	for _, env := range g.backlog {
		member.enqueue(env)
	}
	g.backlog = nil
}

// leave removes member from the group and hands its queued envelopes to
// the other members, or to the backlog of a durable group. It reports
// whether the group ended: ephemeral and with no member left.
func (g *memoryGroup) leave(member *memorySubscription, queued []Envelope) (ended bool) {
	g.members = slices.DeleteFunc(g.members, func(m *memorySubscription) bool { return m == member })
	for _, env := range queued {
		g.deliver(env)
	}

	return len(g.members) == 0 && g.ephemeral
}

// deliver hands env to the next member, or keeps it in the backlog while
// the group has no member; an ended ephemeral group drops it.
func (g *memoryGroup) deliver(env Envelope) {
	switch {
	case len(g.members) > 0:
		g.next().enqueue(env)
	case !g.ephemeral:
		g.backlog = append(g.backlog, env)
	}
}

// next returns the member the next envelope goes to, round robin.
func (g *memoryGroup) next() *memorySubscription {
	member := g.members[g.cursor%len(g.members)]
	g.cursor++

	return member
}

// memorySubscription is one subscription, with its queue and goroutine.
type memorySubscription struct {
	transport *MemoryEventTransport
	sub       Subscription
	handler   EnvelopeHandler
	ctx       context.Context //nolint:containedctx // the parent of the handler contexts
	cancel    context.CancelFunc

	mu       sync.Mutex
	queue    []Envelope
	stopping bool
	wake     chan struct{} // signalled when queue grows or stopping is set
	done     chan struct{} // closed when the goroutine returned
}

// Send hands a copy of env to one subscription of each group subscribed to
// env.Destination. It fails only when ctx is done.
func (t *MemoryEventTransport) Send(ctx context.Context, env Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	env.Payload = slices.Clone(env.Payload)
	env.Headers = maps.Clone(env.Headers)

	t.mu.Lock()
	defer t.mu.Unlock()
	for _, group := range t.groups[env.Destination] {
		group.deliver(env)
	}

	return nil
}

// Subscribe joins sub.Group on sub.Destination, creating the group at the
// latest event when it does not exist, and starts handling its envelopes
// with h. ctx is the parent of the handler contexts.
func (t *MemoryEventTransport) Subscribe(ctx context.Context, sub Subscription, h EnvelopeHandler) (func(context.Context) error, error) {
	if sub.Group == "" || sub.Destination == "" {
		return nil, libErrorMatching(ErrSubscriptionNotSupported, "memory transport: a subscription needs a destination and a group")
	}
	subCtx, cancel := context.WithCancel(ctx)
	s := &memorySubscription{
		transport: t,
		sub:       sub,
		handler:   h,
		ctx:       subCtx,
		cancel:    cancel,
		wake:      make(chan struct{}, 1),
		done:      make(chan struct{}),
	}

	t.mu.Lock()
	groups := t.groups[sub.Destination]
	if groups == nil {
		groups = map[string]*memoryGroup{}
		t.groups[sub.Destination] = groups
	}
	group := groups[sub.Group]
	if group == nil {
		group = &memoryGroup{ephemeral: sub.Ephemeral}
		groups[sub.Group] = group
	}
	group.join(s)
	t.mu.Unlock()

	go s.run()

	return s.stop, nil
}

// waitIdle waits until every envelope queued for a subscription was
// handled, bounded by ctx, and reports whether it was.
func (t *MemoryEventTransport) waitIdle(ctx context.Context) bool {
	return awaitDone(t.pending.atMost(0), ctx)
}

// enqueue queues env for s; the transport's mu is held.
func (s *memorySubscription) enqueue(env Envelope) {
	s.transport.pending.add(1)
	s.mu.Lock()
	s.queue = append(s.queue, env)
	s.mu.Unlock()
	s.signal()
}

// signal wakes the goroutine of s.
func (s *memorySubscription) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// run handles the queue of s until it is stopping with nothing queued.
func (s *memorySubscription) run() {
	defer close(s.done)
	for {
		s.mu.Lock()
		if len(s.queue) == 0 {
			stopping := s.stopping
			s.mu.Unlock()
			if stopping {
				return
			}
			<-s.wake
			continue
		}
		env := s.queue[0]
		s.queue[0] = Envelope{} // release the handled payload
		s.queue = s.queue[1:]
		s.mu.Unlock()

		s.deliverWithRetry(env)
		s.transport.pending.add(-1)
	}
}

// deliverWithRetry hands env to the handler until it succeeds, fails
// permanently, the attempts are exhausted or the subscription context
// ends; a failed envelope is logged as dropped.
func (s *memorySubscription) deliverWithRetry(env Envelope) {
	var err error
	for attempt := 1; attempt <= s.transport.maxAttempts; attempt++ {
		err = recoverListenerError(func() error { return s.handler(s.ctx, env) })
		switch {
		case err == nil:
			return
		case IsPermanentError(err):
			s.transport.logf("doger: memory transport dropped event %s (%s) for group %q: permanent error: %v", env.ID, env.EventType, s.sub.Group, err)
			return
		case s.ctx.Err() != nil:
			s.requeue(env, err)
			return
		}
	}
	s.transport.logf("doger: memory transport dropped event %s (%s) for group %q after %d attempts: %v", env.ID, env.EventType, s.sub.Group, s.transport.maxAttempts, err)
}

// requeue hands env, whose handling the end of the subscription context
// interrupted, back to the group s left: to another member, or to the
// backlog of a durable group. When s did not leave its group, or the group
// ended, env is dropped and logged.
func (s *memorySubscription) requeue(env Envelope, err error) {
	t := s.transport
	t.mu.Lock()
	s.mu.Lock()
	left := s.stopping
	s.mu.Unlock()
	group := t.groups[s.sub.Destination][s.sub.Group]
	if left && group != nil {
		group.deliver(env)
		t.mu.Unlock()
		return
	}
	t.mu.Unlock()
	t.logf("doger: memory transport dropped event %s (%s) for group %q: subscription context ended: %v", env.ID, env.EventType, s.sub.Group, err)
}

// stop leaves the group, handing the envelopes still queued to the other
// members, or keeping them for the next one, and waits for the envelope
// being handled. When ctx ends first, the handler context is cancelled and
// stop returns ctx's error without waiting further; the envelope whose
// handling that interrupts goes back to the group, as a queued one.
func (s *memorySubscription) stop(ctx context.Context) error {
	s.leave()

	defer s.cancel()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// leave removes s from its group and moves its queue.
func (s *memorySubscription) leave() {
	t := s.transport
	t.mu.Lock()
	defer t.mu.Unlock()

	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return
	}
	s.stopping = true
	queued := s.queue
	s.queue = nil
	s.mu.Unlock()
	s.signal()

	// moved before uncounted, so the count never drops to zero meanwhile
	defer t.pending.add(-len(queued))
	groups := t.groups[s.sub.Destination]
	if group := groups[s.sub.Group]; group != nil && group.leave(s, queued) {
		delete(groups, s.sub.Group)
	}
}
