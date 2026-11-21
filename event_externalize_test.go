package doger

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ErrBrokerDown is the error of the transports whose Send fails.
var ErrBrokerDown = errors.New("broker down")

func TestExternalize_WhichTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		register func(root *RootScope) error
		sends    int
	}{
		{name: "unregistered", register: func(*RootScope) error { return nil }},
		{name: "registered", register: func(r *RootScope) error { return RegisterEventType[OrderPlaced](r, "orders.placed") }},
		{name: "externalized", register: func(r *RootScope) error {
			return RegisterEventType[OrderPlaced](r, "orders.placed", EventExternalized())
		}, sends: 1},
		{name: "externalize_immediate", register: func(r *RootScope) error {
			return RegisterEventType[OrderPlaced](r, "orders.placed", EventExternalize(ExternalizeImmediate))
		}, sends: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			transport := &recordingTransport{}
			root := NewWithOpts(&InjectorOpts{EventTransport: transport})
			require.NoError(t, tt.register(root))
			local := countListener[OrderPlaced](t, root)

			require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
			assert.Equal(t, 1, *local)
			assert.Len(t, transport.sent(), tt.sends)
		})
	}
}

func TestExternalize_ListenerCount(t *testing.T) {
	t.Parallel()

	for _, listeners := range []int{0, 1, 5} {
		t.Run(strings.Repeat("l", listeners), func(t *testing.T) {
			t.Parallel()

			transport := &recordingTransport{}
			root := NewWithOpts(&InjectorOpts{EventTransport: transport})
			require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalized()))
			for range listeners {
				countListener[OrderPlaced](t, root)
			}

			require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
			assert.Len(t, transport.sent(), 1)
		})
	}
}

func TestExternalize_NoTransport(t *testing.T) {
	t.Parallel()

	logger, logs := newRecordingLogger(t, slog.LevelDebug)
	root := NewWithOpts(&InjectorOpts{Logger: logger})
	require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalized()))
	local := countListener[OrderPlaced](t, root)

	for range 3 {
		require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
	}

	assert.Equal(t, 3, *local)
	matching := 0
	logs.mu.Lock()
	for _, rec := range logs.records {
		if strings.Contains(rec.String(), "no EventTransport") {
			matching++
		}
	}
	logs.mu.Unlock()
	assert.Equal(t, 1, matching)
}

func TestExternalize_Immediate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		send  func() error
		check func(t *testing.T, err error)
	}{
		{name: "ok", send: func() error { return nil }, check: func(t *testing.T, err error) {
			t.Helper()
			require.NoError(t, err)
		}},
		{name: "error", send: func() error { return ErrBrokerDown }, check: func(t *testing.T, err error) {
			t.Helper()
			var externalizeErr *ExternalizeError
			require.ErrorAs(t, err, &externalizeErr)
			assert.Equal(t, "orders.placed", externalizeErr.EventType)
			assert.Equal(t, "orders.placed", externalizeErr.Destination)
			assert.Equal(t, ExternalizeImmediate, externalizeErr.Strategy)
			require.ErrorIs(t, err, ErrBrokerDown)
		}},
		{name: "panic", send: func() error { panic("kaboom") }, check: func(t *testing.T, err error) {
			t.Helper()
			var externalizeErr *ExternalizeError
			require.ErrorAs(t, err, &externalizeErr)
			assert.Contains(t, err.Error(), "kaboom")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := &syncRecorder{}
			transport := &recordingTransport{onSend: func(context.Context, Envelope) error {
				rec.add("send")
				return tt.send()
			}}
			root := NewWithOpts(&InjectorOpts{EventTransport: transport})
			immediateOrders(t, root)
			Listen(root, func(context.Context, OrderPlaced) error {
				rec.add("local")
				return nil
			})

			tt.check(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
			assert.Equal(t, []string{"local", "send"}, rec.list())
		})
	}
}

func TestExternalize_ImmediateContext(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	transport := &recordingTransport{onSend: func(ctx context.Context, _ Envelope) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	root := NewWithOpts(&InjectorOpts{EventTransport: transport})
	immediateOrders(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := Publish(ctx, root, OrderPlaced{ID: "1"})

	var externalizeErr *ExternalizeError
	require.ErrorAs(t, err, &externalizeErr)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// committedCalls publishes OrderPlaced{ID: "1"} on root in a context
// holding a fake transaction that then commits, and returns
// the transport calls made during Publish and after commit, and the error
// of Publish.
func committedCalls(t *testing.T, root *RootScope, transport *recordingTransport, opts ...PublishOption) (during, after []string, err error) {
	t.Helper()
	tx := newFakeTx("tx1")
	err = Publish(withFakeTx(context.Background(), tx), root, OrderPlaced{ID: "1"}, opts...)
	during = transport.callList()
	tx.commit()

	return during, transport.callList()[len(during):], err
}

func TestExternalize_Precedence(t *testing.T) {
	t.Parallel()

	const send, sendTx = "send:orders.placed", "sendtx:orders.placed"
	tests := []struct {
		name    string
		global  ExternalizeStrategy
		typ     EventTypeOption
		publish []PublishOption
		during  []string
		after   []string
	}{
		{name: "auto_outbox", typ: EventExternalized(), during: []string{sendTx}},
		{name: "global", global: ExternalizeImmediate, typ: EventExternalized(), during: []string{send}},
		{name: "type_over_global", global: ExternalizeImmediate, typ: EventExternalize(ExternalizeAfterCommit), after: []string{send}},
		{name: "publish_over_type", global: ExternalizeImmediate, typ: EventExternalize(ExternalizeAfterCommit), publish: []PublishOption{PublishExternalize(ExternalizeOutbox)}, during: []string{sendTx}},
		{name: "publish_over_outbox_type", typ: EventExternalize(ExternalizeOutbox), publish: []PublishOption{PublishExternalize(ExternalizeImmediate)}, during: []string{send}},
		{name: "global_after_commit", global: ExternalizeAfterCommit, typ: EventExternalized(), after: []string{send}},
		{name: "type_auto_over_global", global: ExternalizeImmediate, typ: EventExternalize(ExternalizeAuto), during: []string{sendTx}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := &fakeTxSource{}
			transport := &recordingTxTransport{recordingTransport: &recordingTransport{}, source: source}
			root := NewWithOpts(&InjectorOpts{EventExternalize: tt.global, EventTransport: transport, TxSource: source})
			require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed", tt.typ))

			during, after, err := committedCalls(t, root, transport.recordingTransport, tt.publish...)

			require.NoError(t, err)
			assert.Equal(t, tt.during, nilIfEmpty(during), "during Publish")
			assert.Equal(t, tt.after, nilIfEmpty(after), "after commit")
		})
	}
}

// nilIfEmpty returns s, or nil when it is empty.
func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func TestExternalize_Auto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		txSender bool
		source   bool // InjectorOpts.TxSource, the transport's own one for a TxEventSender
		inTx     bool
		during   string
	}{
		{name: "tx_sender/tx", txSender: true, source: true, inTx: true, during: "sendtx:orders.placed"},
		{name: "tx_sender/no_tx", txSender: true, source: true, during: "send:orders.placed"},
		{name: "plain/tx", source: true, inTx: true, during: "send:orders.placed"},
		{name: "plain/no_source", during: "send:orders.placed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recording := &recordingTransport{}
			source := &fakeTxSource{}
			opts := &InjectorOpts{EventTransport: recording}
			if tt.txSender {
				opts.EventTransport = &recordingTxTransport{recordingTransport: recording, source: source}
			}
			if tt.source {
				opts.TxSource = source
			}
			root := NewWithOpts(opts)
			require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalized()))
			ctx := context.Background()
			if tt.inTx {
				ctx = withFakeTx(ctx, newFakeTx("tx1"))
			}

			require.NoError(t, Publish(ctx, root, OrderPlaced{ID: "1"}))
			assert.Equal(t, []string{tt.during}, recording.callList())
		})
	}
}

func TestExternalize_AfterCommit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		send     func() error
		codec    EventCodec // nil: the default
		inTx     bool
		commit   bool
		publish  func(t *testing.T, err error)
		sends    int
		hookErrs func(t *testing.T, errs []error)
	}{
		{name: "commit", send: func() error { return nil }, inTx: true, commit: true, sends: 1},
		{name: "rollback", send: func() error { return nil }, inTx: true},
		{name: "commit/error", send: func() error { return ErrBrokerDown }, inTx: true, commit: true, sends: 1, hookErrs: func(t *testing.T, errs []error) {
			t.Helper()
			require.Len(t, errs, 1)
			var externalizeErr *ExternalizeError
			require.ErrorAs(t, errs[0], &externalizeErr)
			assert.Equal(t, ExternalizeAfterCommit, externalizeErr.Strategy)
			require.ErrorIs(t, errs[0], ErrBrokerDown)
		}},
		{name: "commit/panic", send: func() error { panic("kaboom") }, inTx: true, commit: true, sends: 1, hookErrs: func(t *testing.T, errs []error) {
			t.Helper()
			require.Len(t, errs, 1)
			var externalizeErr *ExternalizeError
			require.ErrorAs(t, errs[0], &externalizeErr)
			assert.Contains(t, errs[0].Error(), "kaboom")
		}},
		{name: "no_tx/error", send: func() error { return ErrBrokerDown }, sends: 1, publish: func(t *testing.T, err error) {
			t.Helper()
			var externalizeErr *ExternalizeError
			require.ErrorAs(t, err, &externalizeErr)
			require.ErrorIs(t, err, ErrBrokerDown)
		}},
		{name: "commit/encoding_error", send: func() error { return nil }, codec: fakeCodec{contentType: "application/json", marshalErr: ErrCodecBroken}, inTx: true, commit: true, publish: func(t *testing.T, err error) {
			t.Helper()
			var externalizeErr *ExternalizeError
			require.ErrorAs(t, err, &externalizeErr)
			require.ErrorIs(t, err, ErrCodecBroken)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			errs := &errorRecorder{}
			transport := &recordingTransport{onSend: func(context.Context, Envelope) error { return tt.send() }}
			root := NewWithOpts(&InjectorOpts{TxSource: &fakeTxSource{}, HookEventError: errs.hook, EventTransport: transport, EventCodec: tt.codec})
			require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeAfterCommit)))
			ctx := context.Background()
			tx := newFakeTx("tx1")
			if tt.inTx {
				ctx = withFakeTx(ctx, tx)
			}

			err := Publish(ctx, root, OrderPlaced{ID: "1"})
			if tt.publish != nil {
				tt.publish(t, err)
			} else {
				require.NoError(t, err)
			}
			if tt.inTx {
				assert.Empty(t, transport.sent(), "nothing is sent before the transaction ends")
				if tt.commit {
					tx.commit()
				} else {
					tx.rollback()
				}
			}

			assert.Len(t, transport.sent(), tt.sends)
			if tt.hookErrs != nil {
				tt.hookErrs(t, errs.list())
			} else {
				assert.Empty(t, errs.list())
			}
		})
	}
}

func TestExternalize_AfterCommitDetached(t *testing.T) {
	t.Parallel()

	source := &fakeTxSource{}
	transport := &recordingTransport{}
	root := NewWithOpts(&InjectorOpts{TxSource: source, EventTransport: transport})
	require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeAfterCommit)))
	tx := newFakeTx("tx1")
	ctx, cancel := context.WithCancel(context.WithValue(withFakeTx(context.Background(), tx), ctxTestKey, "t1"))

	require.NoError(t, Publish(ctx, root, OrderPlaced{ID: "1"}))
	cancel()
	tx.commit()

	transport.mu.Lock()
	require.Len(t, transport.sendCtxs, 1)
	sendCtx := transport.sendCtxs[0]
	transport.mu.Unlock()
	assert.Equal(t, "t1", sendCtx.Value(ctxTestKey))
	require.NoError(t, sendCtx.Err())
	_, found := source.Lookup(sendCtx)
	assert.False(t, found)
}

// ErrInsertFailed is the error of the outbox writes that fail.
var ErrInsertFailed = errors.New("insert failed")

func TestExternalize_Outbox(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		commit bool
		ran    int32
	}{
		{name: "commit", commit: true, ran: 1},
		{name: "rollback"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)

			memory := NewMemoryEventTransport()
			transport := &recordingTxTransport{recordingTransport: &recordingTransport{inner: memory}, source: &fakeTxSource{}}
			a := NewWithOpts(&InjectorOpts{EventTransport: transport})
			require.NoError(t, RegisterEventType[OrderPlaced](a, "orders.placed", EventExternalize(ExternalizeOutbox)))
			var ran atomic.Int32
			memoryRoot(t, transport, func(b *RootScope) {
				Listen(b, func(context.Context, OrderPlaced) error {
					ran.Add(1)
					return nil
				}, ListenerID("mail"), ListenerDelivery(DeliverOnce))
			})
			tx := newFakeTx("tx1")

			require.NoError(t, Publish(withFakeTx(context.Background(), tx), a, OrderPlaced{ID: "1"}))
			assert.Contains(t, transport.callList(), "sendtx:orders.placed")
			if tt.commit {
				tx.commit()
			} else {
				tx.rollback()
			}
			waitIdle(t, memory)

			assert.Equal(t, tt.ran, ran.Load())
		})
	}
}

func TestExternalize_AfterAsyncRefusal(t *testing.T) {
	t.Parallel()

	for _, mode := range []EventErrorMode{EventFailFast, EventContinue} {
		t.Run(mode.String(), func(t *testing.T) {
			t.Parallel()

			transport := &recordingTransport{}
			root := NewWithOpts(&InjectorOpts{EventErrorMode: mode, EventTransport: transport})
			immediateOrders(t, root)
			Listen(root, func(context.Context, OrderPlaced) error { return nil }, ListenerAsync())
			fromShutdown := publishOnShutdown(t, root)

			root.Shutdown()

			require.ErrorIs(t, *fromShutdown, ErrPublishAfterShutdown)
			assert.Contains(t, transport.callList(), "send:orders.placed", "the refusal does not stop externalization")
		})
	}
}

func TestExternalize_OutboxError(t *testing.T) {
	t.Parallel()

	source := &fakeTxSource{}
	transport := &recordingTxTransport{
		recordingTransport: &recordingTransport{},
		source:             source,
		onSendTx:           func(context.Context, Envelope) error { return ErrInsertFailed },
	}
	root := NewWithOpts(&InjectorOpts{EventTransport: transport})
	require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeOutbox)))

	err := Publish(withFakeTx(context.Background(), newFakeTx("tx1")), root, OrderPlaced{ID: "1"})

	var externalizeErr *ExternalizeError
	require.ErrorAs(t, err, &externalizeErr)
	assert.Equal(t, ExternalizeOutbox, externalizeErr.Strategy)
	require.ErrorIs(t, err, ErrInsertFailed)
}

func TestExternalize_OutboxFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		fallback OutboxFallback
		err      error
		calls    []string
	}{
		{name: "unset", err: ErrNoTransaction},
		{name: "fail", fallback: OutboxFallbackFail, err: ErrNoTransaction},
		{name: "immediate", fallback: OutboxFallbackImmediate, calls: []string{"send:orders.placed"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			transport := &recordingTxTransport{recordingTransport: &recordingTransport{}, source: &fakeTxSource{}}
			root := NewWithOpts(&InjectorOpts{EventTransport: transport, EventOutboxFallback: tt.fallback})
			require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeOutbox)))

			err := Publish(context.Background(), root, OrderPlaced{ID: "1"})

			if tt.err != nil {
				var externalizeErr *ExternalizeError
				require.ErrorAs(t, err, &externalizeErr)
				require.ErrorIs(t, err, tt.err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.calls, nilIfEmpty(transport.callList()))
		})
	}
}

func TestExternalize_ErrorReportsStrategyUsed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		strategy ExternalizeStrategy
		inTx     bool
		want     ExternalizeStrategy
	}{
		{name: "outbox_fallback_immediate", strategy: ExternalizeOutbox, want: ExternalizeImmediate},
		{name: "after_commit_no_tx", strategy: ExternalizeAfterCommit, want: ExternalizeImmediate},
		{name: "auto_no_tx", strategy: ExternalizeAuto, want: ExternalizeImmediate},
		{name: "immediate", strategy: ExternalizeImmediate, inTx: true, want: ExternalizeImmediate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recording := &recordingTransport{onSend: func(context.Context, Envelope) error { return ErrBrokerDown }}
			transport := &recordingTxTransport{recordingTransport: recording, source: &fakeTxSource{}}
			root := NewWithOpts(&InjectorOpts{EventTransport: transport, EventOutboxFallback: OutboxFallbackImmediate})
			require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(tt.strategy)))
			ctx := context.Background()
			if tt.inTx {
				ctx = withFakeTx(ctx, newFakeTx("tx1"))
			}

			err := Publish(ctx, root, OrderPlaced{ID: "1"})

			var externalizeErr *ExternalizeError
			require.ErrorAs(t, err, &externalizeErr)
			require.ErrorIs(t, err, ErrBrokerDown)
			assert.Equal(t, tt.want, externalizeErr.Strategy)
		})
	}
}

func TestExternalize_OutboxNotSupported(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		global ExternalizeStrategy
		typ    EventTypeOption
		act    func(root *RootScope) error
	}{
		{name: "global/start", global: ExternalizeOutbox, typ: EventExternalized(), act: func(r *RootScope) error { return r.Start() }},
		{name: "type/start", typ: EventExternalize(ExternalizeOutbox), act: func(r *RootScope) error { return r.Start() }},
		{name: "publish", typ: EventExternalized(), act: func(r *RootScope) error {
			err := Publish(context.Background(), r, OrderPlaced{ID: "1"}, PublishExternalize(ExternalizeOutbox))
			if _, ok := errors.AsType[*ExternalizeError](err); !ok {
				return errors.New("not an *ExternalizeError")
			}
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := NewWithOpts(&InjectorOpts{EventTransport: &recordingTransport{}, EventExternalize: tt.global})
			t.Cleanup(func() { root.Shutdown() })
			require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed", tt.typ))

			require.ErrorIs(t, tt.act(root), ErrOutboxNotSupported)
		})
	}
}

func TestExternalize_AfterDispatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		mode  EventErrorMode
		send  error
		sends int
		check func(t *testing.T, err error)
	}{
		{name: "fail_fast", mode: EventFailFast, check: func(t *testing.T, err error) {
			t.Helper()
			var listenerErr *ListenerError
			require.ErrorAs(t, err, &listenerErr)
			require.ErrorIs(t, err, ErrOutOfStock)
		}},
		{name: "continue", mode: EventContinue, sends: 1, check: func(t *testing.T, err error) {
			t.Helper()
			require.ErrorIs(t, err, ErrOutOfStock)
		}},
		{name: "continue/send_error", mode: EventContinue, send: ErrBrokerDown, sends: 1, check: func(t *testing.T, err error) {
			t.Helper()
			require.ErrorIs(t, err, ErrOutOfStock)
			require.ErrorIs(t, err, ErrBrokerDown)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			transport := &recordingTransport{onSend: func(context.Context, Envelope) error { return tt.send }}
			root := NewWithOpts(&InjectorOpts{EventErrorMode: tt.mode, EventTransport: transport})
			immediateOrders(t, root)
			Listen(root, func(context.Context, OrderPlaced) error { return ErrOutOfStock }, ListenerID("local"))

			tt.check(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
			assert.Len(t, transport.sent(), tt.sends)
		})
	}
}

func TestExternalize_Scopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		child bool
		opts  []PublishOption
		sends int
	}{
		{name: "root", sends: 1},
		{name: "child", child: true, sends: 1},
		{name: "root/local", opts: []PublishOption{PublishLocal()}},
		{name: "child/local", child: true, opts: []PublishOption{PublishLocal()}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			transport := &recordingTransport{}
			root := NewWithOpts(&InjectorOpts{EventTransport: transport})
			require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalized()))
			var injector Injector = root
			if tt.child {
				injector = root.Scope("req")
			}

			require.NoError(t, Publish(context.Background(), injector, OrderPlaced{ID: "1"}, tt.opts...))
			assert.Len(t, transport.sent(), tt.sends)
		})
	}
}

func TestExternalize_Lifecycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(root *RootScope) context.Context
		err   error
		sends int
	}{
		{name: "before_start", setup: func(*RootScope) context.Context { return context.Background() }, sends: 1},
		{name: "after_start", setup: func(r *RootScope) context.Context {
			if err := r.Start(); err != nil {
				panic(err)
			}
			return context.Background()
		}, sends: 1},
		{name: "cancelled", setup: func(*RootScope) context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}, err: context.Canceled},
		{name: "after_shutdown", setup: func(r *RootScope) context.Context {
			r.Shutdown()
			return context.Background()
		}, err: ErrPublishAfterShutdown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			transport := &recordingTransport{}
			root := NewWithOpts(&InjectorOpts{EventTransport: transport})
			t.Cleanup(func() { root.Shutdown() })
			immediateOrders(t, root)
			ctx := tt.setup(root)

			err := Publish(ctx, root, OrderPlaced{ID: "1"})

			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
			} else {
				require.NoError(t, err)
			}
			assert.Len(t, transport.sent(), tt.sends)
		})
	}
}

func TestExternalize_Concurrent(t *testing.T) {
	t.Parallel()

	transport := &recordingTransport{}
	root := NewWithOpts(&InjectorOpts{EventTransport: transport})
	t.Cleanup(func() { root.Shutdown() })
	immediateOrders(t, root)
	require.NoError(t, root.Start())

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			assert.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: strconv.Itoa(i)}))
		})
	}
	wg.Wait()

	ids := map[string]bool{}
	for _, env := range transport.sent() {
		ids[env.ID] = true
	}
	assert.Len(t, transport.sent(), 100)
	assert.Len(t, ids, 100)
}

func TestExternalizeOptions_Invalid(t *testing.T) {
	t.Parallel()

	t.Run("global_strategy", func(t *testing.T) {
		t.Parallel()
		assert.PanicsWithError(t, "doger: invalid ExternalizeStrategy 9", func() {
			NewWithOpts(&InjectorOpts{EventExternalize: ExternalizeStrategy(9)})
		})
	})
	t.Run("outbox_fallback", func(t *testing.T) {
		t.Parallel()
		assert.PanicsWithError(t, "doger: invalid OutboxFallback 4", func() {
			NewWithOpts(&InjectorOpts{EventOutboxFallback: OutboxFallback(4)})
		})
	})
	t.Run("publish_strategy", func(t *testing.T) {
		t.Parallel()
		assert.PanicsWithError(t, "doger: invalid ExternalizeStrategy 7", func() {
			PublishExternalize(ExternalizeStrategy(7))
		})
	})
	t.Run("type_strategy", func(t *testing.T) {
		t.Parallel()
		root := New()
		err := RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeStrategy(7)))
		require.ErrorIs(t, err, ErrInvalidEventType)
	})
}

func TestExternalizeEnums_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value interface{ String() string }
		want  string
	}{
		{value: ExternalizeAuto, want: "ExternalizeAuto"},
		{value: ExternalizeImmediate, want: "ExternalizeImmediate"},
		{value: ExternalizeAfterCommit, want: "ExternalizeAfterCommit"},
		{value: ExternalizeOutbox, want: "ExternalizeOutbox"},
		{value: ExternalizeStrategy(9), want: "ExternalizeStrategy(9)"},
		{value: OutboxFallbackFail, want: "OutboxFallbackFail"},
		{value: OutboxFallbackImmediate, want: "OutboxFallbackImmediate"},
		{value: OutboxFallback(4), want: "OutboxFallback(4)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.value.String())
		})
	}
}
