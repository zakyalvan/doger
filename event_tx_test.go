package doger

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// txRoot returns a root whose TxSource is a fake, with a HookEventError
// recording into errs when errs is not nil.
func txRoot(t *testing.T, source TxSource, errs *errorRecorder) *RootScope {
	t.Helper()
	opts := &InjectorOpts{TxSource: source}
	if errs != nil {
		opts.HookEventError = errs.hook
	}
	root := NewWithOpts(opts)
	t.Cleanup(func() { root.Shutdown() })

	return root
}

// errorRecorder records the errors HookEventError receives.
type errorRecorder struct {
	mu   sync.Mutex
	errs []error
}

func (r *errorRecorder) hook(_ context.Context, _ *RootScope, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, err)
}

func TestTxPhase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		opts   []ListenerOption
		commit bool
		during int32 // runs during Publish
		after  int32 // runs once the transaction ended
	}{
		{name: "default/commit", commit: true, during: 1},
		{name: "immediate/commit", opts: []ListenerOption{ListenerTxPhase(TxPhaseImmediate)}, commit: true, during: 1},
		{name: "after_commit/commit", opts: []ListenerOption{ListenerTxPhase(TxPhaseAfterCommit)}, commit: true, after: 1},
		{name: "after_commit/rollback", opts: []ListenerOption{ListenerTxPhase(TxPhaseAfterCommit)}},
		{name: "after_rollback/rollback", opts: []ListenerOption{ListenerTxPhase(TxPhaseAfterRollback)}, after: 1},
		{name: "after_rollback/commit", opts: []ListenerOption{ListenerTxPhase(TxPhaseAfterRollback)}, commit: true},
		{name: "after_completion/commit", opts: []ListenerOption{ListenerTxPhase(TxPhaseAfterCompletion)}, commit: true, after: 1},
		{name: "after_completion/rollback", opts: []ListenerOption{ListenerTxPhase(TxPhaseAfterCompletion)}, after: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := txRoot(t, &fakeTxSource{}, nil)
			var runs atomic.Int32
			Listen(root, func(context.Context, OrderPlaced) error {
				runs.Add(1)
				return nil
			}, append([]ListenerOption{ListenerID("l")}, tt.opts...)...)
			tx := newFakeTx("tx1")

			require.NoError(t, Publish(withFakeTx(context.Background(), tx), root, OrderPlaced{ID: "1"}))
			assert.Equal(t, tt.during, runs.Load(), "during Publish")

			if tt.commit {
				tx.commit()
			} else {
				tx.rollback()
			}
			assert.Equal(t, tt.during+tt.after, runs.Load(), "after the transaction ended")
		})
	}

	t.Run("after_commit_async/commit", func(t *testing.T) {
		t.Parallel()
		testWithTimeout(t, 5*time.Second)

		root := txRoot(t, &fakeTxSource{}, nil)
		ran := make(chan struct{}, 2)
		Listen(root, func(context.Context, OrderPlaced) error {
			ran <- struct{}{}
			return nil
		}, ListenerID("l"), ListenerTxPhase(TxPhaseAfterCommit), ListenerAsync())
		tx := newFakeTx("tx1")

		require.NoError(t, Publish(withFakeTx(context.Background(), tx), root, OrderPlaced{ID: "1"}))
		assert.Empty(t, ran, "not submitted before commit")

		tx.commit()
		<-ran
		require.True(t, root.Shutdown().Succeed)
		assert.Empty(t, ran, "ran once")
	})
}

func TestTxPhase_Order(t *testing.T) {
	t.Parallel()

	root := txRoot(t, &fakeTxSource{}, nil)
	rec := &recorder{}
	Listen(root, recordAs[OrderPlaced](rec, "a", nil), ListenerID("a"), ListenerOrder(1), ListenerTxPhase(TxPhaseAfterCommit))
	Listen(root, recordAs[OrderPlaced](rec, "b", nil), ListenerID("b"), ListenerOrder(0), ListenerTxPhase(TxPhaseAfterCommit))
	tx := newFakeTx("tx1")

	require.NoError(t, Publish(withFakeTx(context.Background(), tx), root, OrderPlaced{ID: "1"}))
	assert.Empty(t, rec.names)

	tx.commit()
	assert.Equal(t, []string{"b", "a"}, rec.names)
}

func TestTx_DetachedContext(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	source := &fakeTxSource{}
	root := txRoot(t, source, nil)
	var got context.Context
	Listen(root, func(ctx context.Context, _ OrderPlaced) error {
		got = ctx
		return nil
	}, ListenerID("l"), ListenerTxPhase(TxPhaseAfterCommit))
	tx := newFakeTx("tx1")

	ctx, cancel := context.WithCancel(context.WithValue(withFakeTx(context.Background(), tx), ctxTestKey, "t1"))
	require.NoError(t, Publish(ctx, root, OrderPlaced{ID: "1"}))
	cancel()

	done := make(chan struct{})
	go func() { // goroutine "g2": the listener runs before commit returns
		defer close(done)
		tx.commit()
	}()
	<-done

	require.NotNil(t, got)
	assert.Equal(t, "t1", got.Value(ctxTestKey))
	require.NoError(t, got.Err())
	_, found := source.Lookup(got)
	assert.False(t, found)
}

func (r *errorRecorder) list() []error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]error{}, r.errs...)
}

func TestTx_DeferredFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		behavior func(context.Context, OrderPlaced) error
		match    func(t *testing.T, err error)
	}{
		{
			name:     "error",
			behavior: func(context.Context, OrderPlaced) error { return ErrOutOfStock },
			match:    func(t *testing.T, err error) { t.Helper(); require.ErrorIs(t, err, ErrOutOfStock) },
		},
		{
			name:     "panic",
			behavior: func(context.Context, OrderPlaced) error { panic("kaboom") },
			match:    func(t *testing.T, err error) { t.Helper(); assert.Contains(t, err.Error(), "kaboom") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			errs := &errorRecorder{}
			root := txRoot(t, &fakeTxSource{}, errs)
			Listen(root, tt.behavior, ListenerID("a"), ListenerTxPhase(TxPhaseAfterCommit))
			bRan := 0
			Listen(root, func(context.Context, OrderPlaced) error {
				bRan++
				return nil
			}, ListenerID("b"), ListenerTxPhase(TxPhaseAfterCommit))
			tx := newFakeTx("tx1")

			require.NoError(t, Publish(withFakeTx(context.Background(), tx), root, OrderPlaced{ID: "1"}))
			require.NotPanics(t, tx.commit)

			assert.Equal(t, 1, bRan)
			got := errs.list()
			require.Len(t, got, 1)
			var listenerErr *ListenerError
			require.ErrorAs(t, got[0], &listenerErr)
			assert.Equal(t, "a", listenerErr.Listener)
			tt.match(t, got[0])
		})
	}
}

func TestTx_DeferredSurviveFailFast(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		phase TxPhase
		stop  func(cancel context.CancelFunc) error // what the immediate listener does
		want  error
	}{
		{name: "error/after_rollback", phase: TxPhaseAfterRollback, stop: func(context.CancelFunc) error { return ErrOutOfStock }, want: ErrOutOfStock},
		{name: "error/after_completion", phase: TxPhaseAfterCompletion, stop: func(context.CancelFunc) error { return ErrOutOfStock }, want: ErrOutOfStock},
		{name: "cancelled/after_rollback", phase: TxPhaseAfterRollback, stop: func(cancel context.CancelFunc) error { cancel(); return nil }, want: context.Canceled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := txRoot(t, &fakeTxSource{}, nil)
			var cRan atomic.Int32
			Listen(root, func(context.Context, OrderPlaced) error {
				cRan.Add(1)
				return nil
			}, ListenerID("c"), ListenerTxPhase(tt.phase))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			Listen(root, func(context.Context, OrderPlaced) error { return tt.stop(cancel) }, ListenerID("r"))
			Listen(root, func(context.Context, OrderPlaced) error { return nil }, ListenerID("z"))
			tx := newFakeTx("tx1")

			err := Publish(withFakeTx(ctx, tx), root, OrderPlaced{ID: "1"})
			require.ErrorIs(t, err, tt.want)
			tx.rollback()

			assert.Equal(t, int32(1), cRan.Load(), "deferred listener routed before the failure")
		})
	}
}

func TestTx_DeferredFilterAtPublish(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		accept bool
		panics bool
		ran    int32
	}{
		{name: "accepted", accept: true, ran: 1},
		{name: "rejected"},
		{name: "panics", panics: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := txRoot(t, &fakeTxSource{}, nil)
			var filtered, ran atomic.Int32
			Listen(root, func(context.Context, OrderPlaced) error {
				ran.Add(1)
				return nil
			}, ListenerID("l"), ListenerTxPhase(TxPhaseAfterCommit), ListenerFilter(func(OrderPlaced) bool {
				filtered.Add(1)
				if tt.panics {
					panic("bad filter")
				}
				return tt.accept
			}))
			tx := newFakeTx("tx1")

			err := Publish(withFakeTx(context.Background(), tx), root, OrderPlaced{ID: "1"})
			assert.Equal(t, int32(1), filtered.Load(), "the filter runs during Publish")
			if tt.panics {
				var listenerErr *ListenerError
				require.ErrorAs(t, err, &listenerErr)
				assert.Contains(t, err.Error(), "bad filter")
			} else {
				require.NoError(t, err)
			}
			tx.commit()

			assert.Equal(t, int32(1), filtered.Load(), "the filter runs once")
			assert.Equal(t, tt.ran, ran.Load())
		})
	}
}

func TestTx_Fallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   bool // a fake TxSource, with the context marked with a committed transaction
		phase    TxPhase
		fallback []ListenerOption
		ran      int
	}{
		{name: "unset/after_commit/run", phase: TxPhaseAfterCommit, ran: 1},
		{name: "unset/after_commit/skip", phase: TxPhaseAfterCommit, fallback: []ListenerOption{ListenerTxFallback(TxFallbackSkip)}},
		{name: "unset/after_rollback/run", phase: TxPhaseAfterRollback, ran: 1},
		{name: "unset/after_completion/skip", phase: TxPhaseAfterCompletion, fallback: []ListenerOption{ListenerTxFallback(TxFallbackSkip)}},
		{name: "ended/after_commit/run", source: true, phase: TxPhaseAfterCommit, ran: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var source TxSource
			ctx := context.Background()
			if tt.source {
				source = &fakeTxSource{}
				tx := newFakeTx("tx1")
				tx.commit()
				ctx = withFakeTx(ctx, tx)
			}
			root := txRoot(t, source, nil)
			ran := 0
			Listen(root, func(context.Context, OrderPlaced) error {
				ran++
				return nil
			}, append([]ListenerOption{ListenerID("l"), ListenerTxPhase(tt.phase)}, tt.fallback...)...)

			require.NoError(t, Publish(ctx, root, OrderPlaced{ID: "1"}))
			assert.Equal(t, tt.ran, ran)
		})
	}
}

func TestTxOptions(t *testing.T) {
	t.Parallel()

	t.Run("invalid_phase", func(t *testing.T) {
		t.Parallel()
		root := New()
		assert.PanicsWithError(t, "doger: invalid TxPhase 9", func() {
			Listen(root, func(context.Context, OrderPlaced) error { return nil }, ListenerTxPhase(TxPhase(9)))
		})
	})

	t.Run("invalid_fallback", func(t *testing.T) {
		t.Parallel()
		root := New()
		assert.PanicsWithError(t, "doger: invalid TxFallbackMode 5", func() {
			Listen(root, func(context.Context, OrderPlaced) error { return nil }, ListenerTxFallback(TxFallbackMode(5)))
		})
	})

	t.Run("last_phase_wins", func(t *testing.T) {
		t.Parallel()
		root := txRoot(t, &fakeTxSource{}, nil)
		ran := 0
		Listen(root, func(context.Context, OrderPlaced) error {
			ran++
			return nil
		}, ListenerTxPhase(TxPhaseAfterRollback), ListenerTxPhase(TxPhaseAfterCommit))
		tx := newFakeTx("tx1")

		require.NoError(t, Publish(withFakeTx(context.Background(), tx), root, OrderPlaced{ID: "1"}))
		tx.commit()
		assert.Equal(t, 1, ran)
	})
}

func TestTxEnums_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value interface{ String() string }
		want  string
	}{
		{name: "immediate", value: TxPhaseImmediate, want: "TxPhaseImmediate"},
		{name: "after_commit", value: TxPhaseAfterCommit, want: "TxPhaseAfterCommit"},
		{name: "after_rollback", value: TxPhaseAfterRollback, want: "TxPhaseAfterRollback"},
		{name: "after_completion", value: TxPhaseAfterCompletion, want: "TxPhaseAfterCompletion"},
		{name: "run", value: TxFallbackRun, want: "TxFallbackRun"},
		{name: "skip", value: TxFallbackSkip, want: "TxFallbackSkip"},
		{name: "unknown_phase", value: TxPhase(9), want: "TxPhase(9)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.value.String())
		})
	}
}

func TestTxSourceResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		opts      string // "srcA", "srcS" or ""
		service   bool   // srcB under NameOf[TxSource]()
		transport string // "", "tx_sender" (TxSource srcS) or "plain"
		strategy  ExternalizeStrategy
		consulted string // the source with lookups, "" for none
		err       error
		ranDuring bool
	}{
		{name: "opts", opts: "srcA", strategy: ExternalizeImmediate, consulted: "srcA"},
		{name: "service", service: true, strategy: ExternalizeImmediate, consulted: "srcB"},
		{name: "transport", transport: "tx_sender", strategy: ExternalizeOutbox, consulted: "srcS"},
		{name: "conflict", opts: "srcA", transport: "tx_sender", strategy: ExternalizeOutbox, err: ErrTxSourceConflict},
		{name: "same_as_transport", opts: "srcS", transport: "tx_sender", strategy: ExternalizeOutbox, consulted: "srcS"},
		{name: "plain_transport", opts: "srcA", transport: "plain", strategy: ExternalizeAfterCommit, consulted: "srcA"},
		{name: "none", strategy: ExternalizeImmediate, ranDuring: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sources := map[string]*fakeTxSource{"srcA": {}, "srcB": {}, "srcS": {}}
			opts := &InjectorOpts{}
			if tt.opts != "" {
				opts.TxSource = sources[tt.opts]
			}
			switch tt.transport {
			case "tx_sender":
				opts.EventTransport = &recordingTxTransport{recordingTransport: &recordingTransport{}, source: sources["srcS"]}
			case "plain":
				opts.EventTransport = &recordingTransport{}
			}
			root := NewWithOpts(opts)
			if tt.service {
				ProvideNamedValue[TxSource](root, NameOf[TxSource](), sources["srcB"])
			}
			ran := 0
			Listen(root, func(context.Context, OrderPlaced) error {
				ran++
				return nil
			}, ListenerID("l"), ListenerTxPhase(TxPhaseAfterCommit))
			require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(tt.strategy)))

			err := Publish(withFakeTx(context.Background(), newFakeTx("tx1")), root, OrderPlaced{ID: "1"})

			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
				return
			}
			require.NoError(t, err)
			for name, source := range sources {
				assert.Equal(t, name == tt.consulted, source.lookups.Load() > 0, name)
			}
			assert.Equal(t, tt.ranDuring, ran == 1)
		})
	}
}

func TestTxSourceResolution_ConflictAtStart(t *testing.T) {
	t.Parallel()

	shared := mapTxSource{txs: map[string]Tx{}}
	tests := []struct {
		name      string
		opts      TxSource
		transport TxSource
		err       error
	}{
		{name: "different_pointers", opts: &fakeTxSource{}, transport: &fakeTxSource{}, err: ErrTxSourceConflict},
		{name: "same_pointer", opts: shared.ptr(), transport: shared.ptr()},
		{name: "non_comparable_same_value", opts: shared, transport: shared},
		{name: "non_comparable_other_type", opts: shared, transport: &fakeTxSource{}, err: ErrTxSourceConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := NewWithOpts(&InjectorOpts{
				TxSource:       tt.opts,
				EventTransport: &recordingTxTransport{recordingTransport: &recordingTransport{}, source: tt.transport},
			})
			t.Cleanup(func() { root.Shutdown() })

			err := root.Start()

			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
				return
			}
			require.NoError(t, err)
		})
	}
}

// mapTxSource is a TxSource of a non-comparable type: a struct value
// holding a map.
type mapTxSource struct{ txs map[string]Tx }

var sharedMapTxSource = &mapTxSource{}

// ptr returns one pointer, the same on every call.
func (mapTxSource) ptr() *mapTxSource { return sharedMapTxSource }

func (mapTxSource) Lookup(context.Context) (Tx, bool)          { return nil, false }
func (mapTxSource) Detach(ctx context.Context) context.Context { return context.WithoutCancel(ctx) }

func TestTxSourceResolution_Once(t *testing.T) {
	t.Parallel()

	root := New()
	calls := 0
	ProvideNamed(root, NameOf[TxSource](), func(Injector) (TxSource, error) {
		calls++
		return &fakeTxSource{}, nil
	})
	Listen(root, func(context.Context, OrderPlaced) error { return nil }, ListenerTxPhase(TxPhaseAfterCommit))

	for range 3 {
		require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
	}

	assert.Equal(t, 1, calls)
}
