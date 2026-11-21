// Package eventtransporttest is the conformance suite of doger event
// transports: every [doger.EventTransport] implementation runs [Run] in its
// own tests to prove it honors the contract core relies on. It imports only
// the standard library and doger, so a transport module inherits no test
// dependency.
//
//	func TestConformance(t *testing.T) {
//		eventtransporttest.Run(t, func(t *testing.T) doger.EventTransport {
//			return mytransport.New(...)
//		})
//	}
package eventtransporttest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kartaladev/doger"
)

// TxHarness drives the transactions the outbox subtests of [WithTx] write
// in: Begin returns a context holding a new transaction that the
// transport's TxSource finds, and Commit and Rollback end the transaction
// of such a context.
type TxHarness interface {
	Begin(ctx context.Context) (context.Context, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// Option configures [Run].
type Option func(*config)

// config is the configuration of one Run.
type config struct {
	tx           TxHarness
	noDeliverAll bool
}

// WithTx adds the outbox subtests, OutboxCommitDelivers and
// OutboxRollbackDiscards, for a transport implementing
// [doger.TxEventSender], with harness driving its transactions.
func WithTx(harness TxHarness) Option {
	return func(c *config) {
		c.tx = harness
	}
}

// WithoutDeliverAll is for a transport that cannot broadcast: the
// DeliverAll subtests are skipped, and DeliverAllRejected checks that it
// rejects a DeliverAll subscription with an error matching
// [doger.ErrSubscriptionNotSupported].
func WithoutDeliverAll() Option {
	return func(c *config) {
		c.noDeliverAll = true
	}
}

// Timing of the suite: how long a subtest waits for an expected delivery,
// and how long it watches for a delivery that must not happen.
const (
	waitTimeout = 10 * time.Second
	quietPeriod = 200 * time.Millisecond
)

// Run runs the conformance subtests against the transports factory
// returns, one per subtest: SendReceive, DeliverOnceGroup,
// DeliverAllGroups, NewGroupAtLatest, Redelivery, PermanentError,
// StopWaitsHandlers and Cancellation, with the options' additions. Each
// subtest uses destinations of its own and stops its subscriptions.
func Run(t *testing.T, factory func(t *testing.T) doger.EventTransport, opts ...Option) {
	t.Helper()
	run(testingT{t}, func(tt tester) doger.EventTransport {
		return factory(tt.(testingT).T) //nolint:errcheck,forcetypeassert // run only hands out the testers it was given
	}, opts...)
}

// tester is what the suite needs of a *testing.T, so that its own tests
// can record outcomes instead of failing.
type tester interface {
	Helper()
	Logf(format string, args ...any)
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	Skipf(format string, args ...any)
	Cleanup(fn func())
	Run(name string, f func(tester)) bool
}

// testingT is a *testing.T as a tester.
type testingT struct{ *testing.T }

func (t testingT) Run(name string, f func(tester)) bool {
	return t.T.Run(name, func(t *testing.T) { f(testingT{t}) })
}

// run runs the subtests on t.
func run(t tester, factory func(tester) doger.EventTransport, opts ...Option) {
	t.Helper()
	cfg := config{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	s := suite{factory: factory, cfg: cfg}
	t.Run("SendReceive", s.sendReceive)
	t.Run("DeliverOnceGroup", s.deliverOnceGroup)
	t.Run("DeliverAllGroups", s.deliverAllGroups)
	if cfg.noDeliverAll {
		t.Run("DeliverAllRejected", s.deliverAllRejected)
	}
	t.Run("NewGroupAtLatest", s.newGroupAtLatest)
	t.Run("Redelivery", s.redelivery)
	t.Run("PermanentError", s.permanentError)
	t.Run("StopWaitsHandlers", s.stopWaitsHandlers)
	t.Run("Cancellation", s.cancellation)
	if cfg.tx != nil {
		t.Run("OutboxCommitDelivers", s.outboxCommitDelivers)
		t.Run("OutboxRollbackDiscards", s.outboxRollbackDiscards)
	}
}

// suite holds what the subtests share.
type suite struct {
	factory func(tester) doger.EventTransport
	cfg     config
}

// newID returns a random hex ID.
func newID() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])

	return hex.EncodeToString(raw[:])
}

// fixture is the transport of one subtest, with a destination of its own.
type fixture struct {
	t           tester
	transport   doger.EventTransport
	destination string
}

// newFixture returns the fixture of a subtest.
func (s suite) newFixture(t tester) *fixture {
	t.Helper()

	return &fixture{t: t, transport: s.factory(t), destination: "eventtransporttest." + newID()}
}

// envelope returns an envelope for the fixture's destination.
func (f *fixture) envelope() doger.Envelope {
	return doger.Envelope{
		ID:          newID(),
		Destination: f.destination,
		EventType:   "eventtransporttest.event",
		ContentType: "application/json",
		Version:     1,
		Payload:     []byte(`{"n":1}`),
		Headers:     map[string]string{"h": "v"},
		PublishedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
}

// subscribe subscribes h to group in mode, failing the subtest on error,
// and returns the stop function, also called at cleanup.
func (f *fixture) subscribe(group string, mode doger.EventDelivery, h doger.EnvelopeHandler) func(context.Context) error {
	f.t.Helper()
	sub := doger.Subscription{Destination: f.destination, Group: group, Mode: mode, Ephemeral: mode == doger.DeliverAll}
	stop, err := f.transport.Subscribe(context.Background(), sub, h)
	if err != nil {
		f.t.Fatalf("Subscribe(%+v): %v", sub, err)
	}
	var stopped atomic.Bool // set on the goroutine calling stop, read at cleanup
	f.t.Cleanup(func() {
		if !stopped.Load() {
			ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
			defer cancel()
			_ = stop(ctx)
		}
	})

	return func(ctx context.Context) error {
		stopped.Store(true)
		return stop(ctx)
	}
}

// send sends env, failing the subtest on error.
func (f *fixture) send(env doger.Envelope) {
	f.t.Helper()
	if err := f.transport.Send(context.Background(), env); err != nil {
		f.t.Fatalf("Send: %v", err)
	}
}

// collector records the IDs of the envelopes a handler receives.
type collector struct {
	ids chan string
}

func newCollector() *collector { return &collector{ids: make(chan string, 1024)} }

// handler records each envelope and acknowledges it.
func (c *collector) handler(_ context.Context, env doger.Envelope) error {
	c.ids <- env.ID
	return nil
}

// expect waits for n envelopes, failing t when they do not arrive in
// time, and returns their IDs.
func (c *collector) expect(t tester, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	timeout := time.After(waitTimeout)
	for len(ids) < n {
		select {
		case id := <-c.ids:
			ids = append(ids, id)
		case <-timeout:
			t.Fatalf("received %d envelopes, want %d", len(ids), n)
		}
	}

	return ids
}

// quiet fails t when an envelope arrives during the quiet period.
func (c *collector) quiet(t tester) {
	t.Helper()
	select {
	case id := <-c.ids:
		t.Errorf("unexpected envelope %s", id)
	case <-time.After(quietPeriod):
	}
}

// sendReceive: a sent envelope reaches a subscription unchanged.
func (s suite) sendReceive(t tester) {
	f := s.newFixture(t)
	received := make(chan doger.Envelope, 1)
	f.subscribe("g", doger.DeliverOnce, func(_ context.Context, env doger.Envelope) error {
		received <- env
		return nil
	})
	env := f.envelope()
	f.send(env)

	select {
	case got := <-received:
		if got.ID != env.ID || got.EventType != env.EventType || got.ContentType != env.ContentType ||
			got.Version != env.Version || string(got.Payload) != string(env.Payload) || got.Headers["h"] != "v" ||
			got.Destination != env.Destination || !got.PublishedAt.Equal(env.PublishedAt) {
			t.Errorf("received %+v, want %+v", got, env)
		}
	case <-time.After(waitTimeout):
		t.Fatalf("the envelope was not delivered")
	}
}

// deliverOnceGroup: each envelope reaches one member of a DeliverOnce
// group.
func (s suite) deliverOnceGroup(t tester) {
	f := s.newFixture(t)
	c := newCollector()
	f.subscribe("g", doger.DeliverOnce, c.handler)
	f.subscribe("g", doger.DeliverOnce, c.handler)
	const n = 10
	for range n {
		f.send(f.envelope())
	}

	ids := c.expect(t, n)
	c.quiet(t)
	assertDistinct(t, ids)
}

// deliverAllGroups: each envelope reaches every DeliverAll group.
func (s suite) deliverAllGroups(t tester) {
	if s.cfg.noDeliverAll {
		t.Skipf("the transport cannot broadcast (WithoutDeliverAll)")
	}
	f := s.newFixture(t)
	c1, c2 := newCollector(), newCollector()
	f.subscribe("g/"+newID(), doger.DeliverAll, c1.handler)
	f.subscribe("g/"+newID(), doger.DeliverAll, c2.handler)
	const n = 5
	for range n {
		f.send(f.envelope())
	}

	c1.expect(t, n)
	c2.expect(t, n)
	c1.quiet(t)
	c2.quiet(t)
}

// deliverAllRejected: a transport that cannot broadcast rejects DeliverAll.
func (s suite) deliverAllRejected(t tester) {
	f := s.newFixture(t)
	sub := doger.Subscription{Destination: f.destination, Group: "g/" + newID(), Mode: doger.DeliverAll, Ephemeral: true}
	stop, err := f.transport.Subscribe(context.Background(), sub, func(context.Context, doger.Envelope) error { return nil })
	if err == nil {
		_ = stop(context.Background())
		t.Fatalf("Subscribe accepted a DeliverAll subscription")
	}
	if !errors.Is(err, doger.ErrSubscriptionNotSupported) {
		t.Errorf("Subscribe: %v, want an error matching doger.ErrSubscriptionNotSupported", err)
	}
}

// newGroupAtLatest: a new group does not receive the envelopes sent before
// it existed.
func (s suite) newGroupAtLatest(t tester) {
	f := s.newFixture(t)
	f.send(f.envelope())
	c := newCollector()
	f.subscribe("late", doger.DeliverOnce, c.handler)
	env := f.envelope()
	f.send(env)

	ids := c.expect(t, 1)
	c.quiet(t)
	if ids[0] != env.ID {
		t.Errorf("received %s, want the latest envelope %s", ids[0], env.ID)
	}
}

// redelivery: an envelope whose handler failed is delivered again.
func (s suite) redelivery(t tester) {
	f := s.newFixture(t)
	attempts := make(chan string, 16)
	var calls atomic.Int32
	f.subscribe("g", doger.DeliverOnce, func(_ context.Context, env doger.Envelope) error {
		attempts <- env.ID
		if calls.Add(1) == 1 {
			return errors.New("eventtransporttest: transient failure")
		}
		return nil
	})
	env := f.envelope()
	f.send(env)

	ids := (&collector{ids: attempts}).expect(t, 2)
	if ids[0] != env.ID || ids[1] != env.ID {
		t.Errorf("attempts %v, want %s twice", ids, env.ID)
	}
}

// permanentError: an envelope whose handler failed permanently is not
// delivered again.
func (s suite) permanentError(t tester) {
	f := s.newFixture(t)
	c := newCollector()
	f.subscribe("g", doger.DeliverOnce, func(ctx context.Context, env doger.Envelope) error {
		_ = c.handler(ctx, env)
		return doger.PermanentError(errors.New("eventtransporttest: permanent failure"))
	})
	f.send(f.envelope())

	c.expect(t, 1)
	c.quiet(t)
}

// stopWaitsHandlers: stop returns only once the handler in flight returned.
func (s suite) stopWaitsHandlers(t tester) {
	f := s.newFixture(t)
	handling, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { closeOnce(release) })
	stop := f.subscribe("g", doger.DeliverOnce, func(context.Context, doger.Envelope) error {
		closeOnce(handling)
		<-release
		return nil
	})
	f.send(f.envelope())
	waitClosed(t, handling, "the handler was not called")

	stopped := make(chan error, 1)
	go func() { stopped <- stop(context.Background()) }()
	select {
	case err := <-stopped:
		t.Errorf("stop returned (%v) while the handler was running", err)
		return
	case <-time.After(quietPeriod):
	}
	closeOnce(release)
	select {
	case err := <-stopped:
		if err != nil {
			t.Errorf("stop: %v", err)
		}
	case <-time.After(waitTimeout):
		t.Fatalf("stop did not return once the handler returned")
	}
}

// cancellation: the stop context bounds the wait, and the handler's
// context is then cancelled.
func (s suite) cancellation(t tester) {
	f := s.newFixture(t)
	handling, cancelled := make(chan struct{}), make(chan struct{})
	stop := f.subscribe("g", doger.DeliverOnce, func(ctx context.Context, _ doger.Envelope) error {
		closeOnce(handling)
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	})
	f.send(f.envelope())
	waitClosed(t, handling, "the handler was not called")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = stop(ctx)
	}()
	waitClosed(t, stopped, "stop did not return once its context ended")
	waitClosed(t, cancelled, "the handler context was not cancelled")
}

// outboxCommitDelivers: an envelope written in a transaction is delivered
// once it committed, not before.
func (s suite) outboxCommitDelivers(t tester) {
	f, sender := s.outboxFixture(t)
	c := newCollector()
	f.subscribe("g", doger.DeliverOnce, c.handler)
	txCtx := s.begin(t)
	env := f.envelope()
	if err := sender.SendTx(txCtx, env); err != nil {
		t.Fatalf("SendTx: %v", err)
	}
	c.quiet(t)
	if err := s.cfg.tx.Commit(txCtx); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	ids := c.expect(t, 1)
	if ids[0] != env.ID {
		t.Errorf("received %s, want %s", ids[0], env.ID)
	}
}

// outboxRollbackDiscards: an envelope written in a transaction that rolled
// back is never delivered.
func (s suite) outboxRollbackDiscards(t tester) {
	f, sender := s.outboxFixture(t)
	c := newCollector()
	f.subscribe("g", doger.DeliverOnce, c.handler)
	txCtx := s.begin(t)
	if err := sender.SendTx(txCtx, f.envelope()); err != nil {
		t.Fatalf("SendTx: %v", err)
	}
	if err := s.cfg.tx.Rollback(txCtx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	c.quiet(t)
}

// outboxFixture returns the fixture of an outbox subtest and its
// transport as a TxEventSender.
func (s suite) outboxFixture(t tester) (*fixture, doger.TxEventSender) {
	t.Helper()
	f := s.newFixture(t)
	sender, ok := f.transport.(doger.TxEventSender)
	if !ok {
		t.Fatalf("WithTx: the transport does not implement doger.TxEventSender")
	}

	return f, sender
}

// begin begins a transaction with the harness.
func (s suite) begin(t tester) context.Context {
	t.Helper()
	txCtx, err := s.cfg.tx.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	return txCtx
}

// assertDistinct fails t when ids holds a duplicate.
func assertDistinct(t tester, ids []string) {
	t.Helper()
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Errorf("envelope %s was delivered more than once", id)
		}
		seen[id] = true
	}
}

// closeOnce closes ch unless it is closed.
func closeOnce(ch chan struct{}) {
	select {
	case <-ch:
	default:
		close(ch)
	}
}

// waitClosed waits for ch to be closed, failing t with message after the
// wait timeout.
func waitClosed(t tester, ch <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(waitTimeout):
		t.Fatalf("%s", message)
	}
}
