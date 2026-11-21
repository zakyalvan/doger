package doger

import (
	"context"
	"sync"
	"sync/atomic"
)

// Shared fakes of the event transport tests: a fake transaction and the
// TxSource finding it in contexts marked with it.

// fakeTxKey marks a context with its *fakeTx.
type fakeTxKey struct{}

// fakeTx is a transaction whose commit and rollback the test drives. Its
// callbacks run, in registration order, on the goroutine ending it.
type fakeTx struct {
	name string

	mu        sync.Mutex
	ended     bool
	commits   []func(context.Context)
	rollbacks []func(context.Context)
}

var _ Tx = (*fakeTx)(nil)

func newFakeTx(name string) *fakeTx { return &fakeTx{name: name} }

func (tx *fakeTx) OnCommit(fn func(context.Context)) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	tx.commits = append(tx.commits, fn)
}

func (tx *fakeTx) OnRollback(fn func(context.Context)) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	tx.rollbacks = append(tx.rollbacks, fn)
}

// isEnded reports whether the transaction committed or rolled back.
func (tx *fakeTx) isEnded() bool {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.ended
}

// commit ends the transaction and runs its commit callbacks.
func (tx *fakeTx) commit() { tx.end(true) }

// rollback ends the transaction and runs its rollback callbacks.
func (tx *fakeTx) rollback() { tx.end(false) }

func (tx *fakeTx) end(committed bool) {
	tx.mu.Lock()
	tx.ended = true
	callbacks := tx.rollbacks
	if committed {
		callbacks = tx.commits
	}
	tx.mu.Unlock()

	for _, fn := range callbacks {
		fn(context.Background())
	}
}

// withFakeTx returns ctx marked with tx.
func withFakeTx(ctx context.Context, tx *fakeTx) context.Context {
	return context.WithValue(ctx, fakeTxKey{}, tx)
}

// fakeTxSource finds the *fakeTx a context is marked with, until it ended,
// and counts its lookups.
type fakeTxSource struct {
	lookups atomic.Int32
}

var _ TxSource = (*fakeTxSource)(nil)

func (s *fakeTxSource) Lookup(ctx context.Context) (Tx, bool) {
	s.lookups.Add(1)
	tx, _ := ctx.Value(fakeTxKey{}).(*fakeTx)
	if tx == nil || tx.isEnded() {
		return nil, false
	}

	return tx, true
}

func (s *fakeTxSource) Detach(ctx context.Context) context.Context {
	return context.WithValue(context.WithoutCancel(ctx), fakeTxKey{}, (*fakeTx)(nil))
}

// recordingTransport records what the container hands it. Without inner,
// Send returns onSend's result (nil when unset) and Subscribe opens a
// subscription that never delivers; with inner, both are forwarded after
// recording, and the results of the handlers inner calls are recorded.
type recordingTransport struct {
	onSend      func(ctx context.Context, env Envelope) error
	onSubscribe func(sub Subscription) error
	onStop      func(sub Subscription) error
	inner       EventTransport

	mu       sync.Mutex
	calls    []string // "send:<destination>", "subscribe:<group>", "unsubscribe:<group>"
	sends    []Envelope
	sendCtxs []context.Context
	subs     []Subscription
	handlers []EnvelopeHandler
	results  []error
}

var _ EventTransport = (*recordingTransport)(nil)

func (r *recordingTransport) record(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

func (r *recordingTransport) Send(ctx context.Context, env Envelope) error {
	r.mu.Lock()
	r.calls = append(r.calls, "send:"+env.Destination)
	r.sends = append(r.sends, env)
	r.sendCtxs = append(r.sendCtxs, ctx)
	r.mu.Unlock()

	switch {
	case r.onSend != nil:
		return r.onSend(ctx, env)
	case r.inner != nil:
		return r.inner.Send(ctx, env)
	default:
		return nil
	}
}

func (r *recordingTransport) Subscribe(ctx context.Context, sub Subscription, h EnvelopeHandler) (func(context.Context) error, error) {
	r.mu.Lock()
	r.calls = append(r.calls, "subscribe:"+sub.Group)
	r.subs = append(r.subs, sub)
	r.handlers = append(r.handlers, h)
	r.mu.Unlock()

	if r.onSubscribe != nil {
		if err := r.onSubscribe(sub); err != nil {
			return nil, err
		}
	}
	stop := func(context.Context) error { return nil }
	if r.inner != nil {
		var err error
		stop, err = r.inner.Subscribe(ctx, sub, func(ctx context.Context, env Envelope) error {
			err := h(ctx, env)
			r.mu.Lock()
			r.results = append(r.results, err)
			r.mu.Unlock()
			return err
		})
		if err != nil {
			return nil, err
		}
	}

	return func(ctx context.Context) error {
		err := stop(ctx)
		r.record("unsubscribe:" + sub.Group)
		if r.onStop != nil {
			return r.onStop(sub)
		}
		return err
	}, nil
}

func (r *recordingTransport) sent() []Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Envelope{}, r.sends...)
}

// recordingTxTransport is a recordingTransport with an outbox: SendTx is
// recorded as "sendtx:<destination>" and returns onSendTx's result, or,
// with inner, registers inner.Send with the commit of the transaction
// source finds in ctx.
type recordingTxTransport struct {
	*recordingTransport
	source   TxSource
	onSendTx func(ctx context.Context, env Envelope) error
}

var _ TxEventSender = (*recordingTxTransport)(nil)

func (r *recordingTxTransport) SendTx(ctx context.Context, env Envelope) error {
	r.record("sendtx:" + env.Destination)
	if r.onSendTx != nil {
		return r.onSendTx(ctx, env)
	}
	if r.inner != nil {
		tx, ok := r.source.Lookup(ctx)
		if !ok {
			return ErrNoTransaction
		}
		tx.OnCommit(func(ctx context.Context) { _ = r.inner.Send(ctx, env) })
	}

	return nil
}

func (r *recordingTxTransport) TxSource() TxSource { return r.source }

func (r *recordingTransport) callList() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.calls...)
}

// loopbackTransport delivers each envelope during Send, on the sending
// goroutine, to one subscription of each group of its destination, round
// robin within a group. A group starts at the latest event.
type loopbackTransport struct {
	mu     sync.Mutex
	subs   map[string][]*loopbackSub // by destination, in subscription order
	cursor map[string]int            // by destination and group
}

// loopbackSub is a subscription of a loopbackTransport.
type loopbackSub struct {
	ctx     context.Context //nolint:containedctx // the subscription context, handed to its handler
	sub     Subscription
	handler EnvelopeHandler
}

var _ EventTransport = (*loopbackTransport)(nil)

func (l *loopbackTransport) Send(_ context.Context, env Envelope) error {
	l.mu.Lock()
	groups := map[string][]*loopbackSub{}
	var order []string
	for _, s := range l.subs[env.Destination] {
		if _, seen := groups[s.sub.Group]; !seen {
			order = append(order, s.sub.Group)
		}
		groups[s.sub.Group] = append(groups[s.sub.Group], s)
	}
	targets := make([]*loopbackSub, 0, len(order))
	for _, group := range order {
		key := env.Destination + "\x00" + group
		members := groups[group]
		targets = append(targets, members[l.cursor[key]%len(members)])
		l.cursor[key]++
	}
	l.mu.Unlock()

	for _, target := range targets {
		_ = target.handler(target.ctx, env)
	}

	return nil
}

func (l *loopbackTransport) Subscribe(ctx context.Context, sub Subscription, h EnvelopeHandler) (func(context.Context) error, error) {
	s := &loopbackSub{ctx: ctx, sub: sub, handler: h}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.subs == nil {
		l.subs = map[string][]*loopbackSub{}
		l.cursor = map[string]int{}
	}
	l.subs[sub.Destination] = append(l.subs[sub.Destination], s)

	return func(context.Context) error {
		l.mu.Lock()
		defer l.mu.Unlock()
		subs := l.subs[sub.Destination]
		for i, candidate := range subs {
			if candidate == s {
				l.subs[sub.Destination] = append(subs[:i:i], subs[i+1:]...)
				break
			}
		}
		return nil
	}, nil
}

func (r *recordingTransport) subscriptions() []Subscription {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Subscription{}, r.subs...)
}

// waitingTransport delivers only what the test hands to deliver, each
// envelope on a goroutine of its own whose context derives from the
// subscription's. Its stop function waits for the deliveries in flight;
// once the stop context ends, it cancels them and returns its error.
type waitingTransport struct {
	mu   sync.Mutex
	subs []*waitingSub
}

// waitingSub is a subscription of a waitingTransport.
type waitingSub struct {
	ctx      context.Context //nolint:containedctx // the parent of the delivery contexts
	cancel   context.CancelFunc
	handler  EnvelopeHandler
	inflight sync.WaitGroup
}

var _ EventTransport = (*waitingTransport)(nil)

func (w *waitingTransport) Send(context.Context, Envelope) error { return nil }

func (w *waitingTransport) Subscribe(ctx context.Context, _ Subscription, h EnvelopeHandler) (func(context.Context) error, error) {
	subCtx, cancel := context.WithCancel(ctx)
	sub := &waitingSub{ctx: subCtx, cancel: cancel, handler: h}
	w.mu.Lock()
	w.subs = append(w.subs, sub)
	w.mu.Unlock()

	return func(ctx context.Context) error {
		done := make(chan struct{})
		go func() {
			sub.inflight.Wait()
			close(done)
		}()
		select {
		case <-done:
			cancel()
			return nil
		case <-ctx.Done():
			cancel()
			<-done
			return ctx.Err()
		}
	}, nil
}

// deliver delivers env to the n-th subscription on a goroutine of its own.
func (w *waitingTransport) deliver(n int, env Envelope) {
	w.mu.Lock()
	sub := w.subs[n]
	w.mu.Unlock()
	sub.inflight.Go(func() { _ = sub.handler(sub.ctx, env) })
}

func (r *recordingTransport) handlerResults() []error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]error{}, r.results...)
}

// handler returns the handler of the n-th subscription.
func (r *recordingTransport) handler(n int) EnvelopeHandler {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.handlers[n]
}
