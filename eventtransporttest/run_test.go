package eventtransporttest

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"sync"
	"testing"

	"github.com/kartaladev/doger"
)

// outcome is how a recorded subtest ended.
type outcome string

const (
	passed  outcome = "pass"
	failed  outcome = "fail"
	skipped outcome = "skip"
)

// recordingTB is a tester recording the outcome of each subtest by its
// path, instead of failing the real test.
type recordingTB struct {
	name     string
	outcomes *outcomes

	mu       sync.Mutex
	failed   bool
	skipped  bool
	cleanups []func()
}

// outcomes are the recorded outcomes, by subtest path.
type outcomes struct {
	mu sync.Mutex
	m  map[string]outcome
}

func newRecordingTB() *recordingTB {
	return &recordingTB{outcomes: &outcomes{m: map[string]outcome{}}}
}

func (r *recordingTB) outcome(path string) outcome {
	r.outcomes.mu.Lock()
	defer r.outcomes.mu.Unlock()
	return r.outcomes.m[path]
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Logf(string, ...any) {}

func (r *recordingTB) Errorf(string, ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed = true
}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
	runtime.Goexit()
}

func (r *recordingTB) Skipf(string, ...any) {
	r.mu.Lock()
	r.skipped = true
	r.mu.Unlock()
	runtime.Goexit()
}

func (r *recordingTB) Cleanup(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanups = append(r.cleanups, fn)
}

// Run runs f as a subtest on a goroutine of its own, so that Fatalf and
// Skipf end the subtest only, and records its outcome.
func (r *recordingTB) Run(name string, f func(tester)) bool {
	child := &recordingTB{name: r.name + "/" + name, outcomes: r.outcomes}
	if r.name == "" {
		child.name = name
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			for _, cleanup := range slices.Backward(child.cleanups) {
				cleanup()
			}
		}()
		f(child)
	}()
	<-done

	result := passed
	switch {
	case child.failed:
		result = failed
		r.Errorf("subtest %s failed", child.name)
	case child.skipped:
		result = skipped
	}
	r.outcomes.mu.Lock()
	r.outcomes.m[child.name] = result
	r.outcomes.mu.Unlock()

	return result != failed
}

// broadcastOnce delivers each event to every member of a DeliverOnce group:
// each subscription joins a group of its own.
type broadcastOnce struct{ *doger.MemoryEventTransport }

func (b *broadcastOnce) Subscribe(ctx context.Context, sub doger.Subscription, h doger.EnvelopeHandler) (func(context.Context) error, error) {
	sub.Group += "/" + newID()
	return b.MemoryEventTransport.Subscribe(ctx, sub, h)
}

// replayHistory starts a new group at the earliest event: it hands every
// envelope sent before to a new subscription.
type replayHistory struct {
	*doger.MemoryEventTransport
	mu   sync.Mutex
	sent []doger.Envelope
}

func (r *replayHistory) Send(ctx context.Context, env doger.Envelope) error {
	r.mu.Lock()
	r.sent = append(r.sent, env)
	r.mu.Unlock()
	return r.MemoryEventTransport.Send(ctx, env)
}

func (r *replayHistory) Subscribe(ctx context.Context, sub doger.Subscription, h doger.EnvelopeHandler) (func(context.Context) error, error) {
	r.mu.Lock()
	history := append([]doger.Envelope{}, r.sent...)
	r.mu.Unlock()
	for _, env := range history {
		if env.Destination == sub.Destination {
			_ = h(ctx, env)
		}
	}
	return r.MemoryEventTransport.Subscribe(ctx, sub, h)
}

// redeliverPermanent redelivers once after a permanent error.
type redeliverPermanent struct{ *doger.MemoryEventTransport }

func (r *redeliverPermanent) Subscribe(ctx context.Context, sub doger.Subscription, h doger.EnvelopeHandler) (func(context.Context) error, error) {
	return r.MemoryEventTransport.Subscribe(ctx, sub, func(ctx context.Context, env doger.Envelope) error {
		err := h(ctx, env)
		if doger.IsPermanentError(err) {
			return h(ctx, env)
		}
		return err
	})
}

// stopEarly returns from stop at once, while handlers still run; the real
// stop finishes in the background.
type stopEarly struct {
	*doger.MemoryEventTransport
	background sync.WaitGroup
}

func (s *stopEarly) Subscribe(ctx context.Context, sub doger.Subscription, h doger.EnvelopeHandler) (func(context.Context) error, error) {
	stop, err := s.MemoryEventTransport.Subscribe(ctx, sub, h)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		s.background.Go(func() { _ = stop(ctx) })
		return nil
	}, nil
}

// stopHangs never returns from a stop whose context has no deadline until
// released is closed; a stop with a deadline, as at cleanup, returns.
type stopHangs struct {
	*doger.MemoryEventTransport
	released chan struct{}
}

func (s *stopHangs) Subscribe(ctx context.Context, sub doger.Subscription, h doger.EnvelopeHandler) (func(context.Context) error, error) {
	stop, err := s.MemoryEventTransport.Subscribe(ctx, sub, h)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		if _, bounded := ctx.Deadline(); !bounded {
			<-s.released
		}
		return stop(ctx)
	}, nil
}

// noDeliverAll rejects DeliverAll subscriptions.
type noDeliverAll struct{ *doger.MemoryEventTransport }

func (n *noDeliverAll) Subscribe(ctx context.Context, sub doger.Subscription, h doger.EnvelopeHandler) (func(context.Context) error, error) {
	if sub.Mode == doger.DeliverAll {
		return nil, doger.ErrSubscriptionNotSupported
	}
	return n.MemoryEventTransport.Subscribe(ctx, sub, h)
}

// quietMemory returns an in-memory transport that does not log.
func quietMemory() *doger.MemoryEventTransport {
	return doger.NewMemoryEventTransport(doger.MemoryLogger(func(string, ...any) {}))
}

func TestRun_CatchesDefects(t *testing.T) {
	t.Parallel()

	released := make(chan struct{}) // releases the stops of stop_hangs
	t.Cleanup(func() { close(released) })

	tests := []struct {
		name      string
		transport func() doger.EventTransport
		subtest   string
	}{
		{name: "broadcast_once", transport: func() doger.EventTransport { return &broadcastOnce{quietMemory()} }, subtest: "DeliverOnceGroup"},
		{name: "replay_history", transport: func() doger.EventTransport { return &replayHistory{MemoryEventTransport: quietMemory()} }, subtest: "NewGroupAtLatest"},
		{name: "redeliver_permanent", transport: func() doger.EventTransport { return &redeliverPermanent{quietMemory()} }, subtest: "PermanentError"},
		{name: "stop_early", transport: func() doger.EventTransport { return &stopEarly{MemoryEventTransport: quietMemory()} }, subtest: "StopWaitsHandlers"},
		{name: "stop_hangs", transport: func() doger.EventTransport {
			return &stopHangs{MemoryEventTransport: quietMemory(), released: released}
		}, subtest: "StopWaitsHandlers"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var transports []doger.EventTransport
			rec := newRecordingTB()
			run(rec, func(tester) doger.EventTransport {
				transport := tt.transport()
				transports = append(transports, transport)
				return transport
			})

			if got := rec.outcome(tt.subtest); got != failed {
				t.Errorf("subtest %s: got %q, want %q", tt.subtest, got, failed)
			}
			for _, transport := range transports {
				if early, ok := transport.(*stopEarly); ok {
					early.background.Wait()
				}
			}
		})
	}
}

// fakeTxKey binds a *fakeTx to a context.
type fakeTxKey struct{}

// fakeTx runs its commit callbacks when it commits.
type fakeTx struct {
	mu      sync.Mutex
	ended   bool
	commits []func(context.Context)
}

func (tx *fakeTx) OnCommit(fn func(context.Context)) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	tx.commits = append(tx.commits, fn)
}

func (tx *fakeTx) OnRollback(func(context.Context)) {}

func (tx *fakeTx) end(commit bool) {
	tx.mu.Lock()
	tx.ended = true
	commits := tx.commits
	tx.mu.Unlock()
	if commit {
		for _, fn := range commits {
			fn(context.Background())
		}
	}
}

// fakeTxSource finds the *fakeTx bound to a context until it ended.
type fakeTxSource struct{}

func (fakeTxSource) Lookup(ctx context.Context) (doger.Tx, bool) {
	tx, _ := ctx.Value(fakeTxKey{}).(*fakeTx)
	if tx == nil {
		return nil, false
	}
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx, !tx.ended
}

func (fakeTxSource) Detach(ctx context.Context) context.Context {
	return context.WithValue(context.WithoutCancel(ctx), fakeTxKey{}, (*fakeTx)(nil))
}

// txMemory is an in-memory transport with an outbox over fakeTx.
type txMemory struct{ *doger.MemoryEventTransport }

func (m txMemory) SendTx(ctx context.Context, env doger.Envelope) error {
	tx, ok := fakeTxSource{}.Lookup(ctx)
	if !ok {
		return doger.ErrNoTransaction
	}
	tx.OnCommit(func(ctx context.Context) { _ = m.Send(ctx, env) })
	return nil
}

func (txMemory) TxSource() doger.TxSource { return fakeTxSource{} }

// fakeHarness begins, commits and rolls back fakeTx transactions.
type fakeHarness struct{}

func (fakeHarness) Begin(ctx context.Context) (context.Context, error) {
	return context.WithValue(ctx, fakeTxKey{}, &fakeTx{}), nil
}

func (fakeHarness) Commit(ctx context.Context) error {
	tx, ok := ctx.Value(fakeTxKey{}).(*fakeTx)
	if !ok {
		return errors.New("no transaction")
	}
	tx.end(true)
	return nil
}

func (fakeHarness) Rollback(ctx context.Context) error {
	tx, ok := ctx.Value(fakeTxKey{}).(*fakeTx)
	if !ok {
		return errors.New("no transaction")
	}
	tx.end(false)
	return nil
}

var _ TxHarness = fakeHarness{}

func TestRun_WithTx(t *testing.T) {
	t.Parallel()

	rec := newRecordingTB()
	run(rec, func(tester) doger.EventTransport { return txMemory{quietMemory()} }, WithTx(fakeHarness{}))

	for _, subtest := range []string{"OutboxCommitDelivers", "OutboxRollbackDiscards"} {
		if got := rec.outcome(subtest); got != passed {
			t.Errorf("subtest %s: got %q, want %q", subtest, got, passed)
		}
	}
	if rec.failed {
		t.Error("the suite failed")
	}
}

func TestRun_WithoutDeliverAll(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		transport func() doger.EventTransport
		rejected  outcome
	}{
		{name: "rejects", transport: func() doger.EventTransport { return &noDeliverAll{quietMemory()} }, rejected: passed},
		{name: "accepts", transport: func() doger.EventTransport { return quietMemory() }, rejected: failed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := newRecordingTB()
			run(rec, func(tester) doger.EventTransport { return tt.transport() }, WithoutDeliverAll())

			if got := rec.outcome("DeliverAllGroups"); got != skipped {
				t.Errorf("DeliverAllGroups: got %q, want %q", got, skipped)
			}
			if got := rec.outcome("DeliverAllRejected"); got != tt.rejected {
				t.Errorf("DeliverAllRejected: got %q, want %q", got, tt.rejected)
			}
		})
	}
}

func TestRun_Memory(t *testing.T) {
	t.Parallel()

	Run(t, func(t *testing.T) doger.EventTransport {
		return doger.NewMemoryEventTransport(doger.MemoryLogger(t.Logf))
	})
}
