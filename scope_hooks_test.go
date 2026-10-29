package doger

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScopeHooks_RegistrationOrder(t *testing.T) {
	t.Parallel()

	// observer returns a scope observer hook recording label and the scope
	observer := func(calls *callRecorder, label string) func(context.Context, *Scope) {
		return func(_ context.Context, scope *Scope) { calls.record(label + " " + scope.Name()) }
	}
	// veto returns a Starting hook recording label and the scope
	veto := func(calls *callRecorder, label string) func(context.Context, *Scope) error {
		return func(ctx context.Context, scope *Scope) error {
			observer(calls, label)(ctx, scope)
			return nil
		}
	}
	start := func(t *testing.T, scope *Scope) { assert.NoError(t, scope.Start()) }
	shutdown := func(t *testing.T, scope *Scope) { assert.True(t, scope.Shutdown().Succeed) }

	tests := []struct {
		name string
		// register sets h1, nil, h2 in the options, then adds h3 and nil
		register func(calls *callRecorder) *RootScope
		trigger  func(t *testing.T, scope *Scope)
	}{
		{
			name: "HookScopeStarting",
			register: func(calls *callRecorder) *RootScope {
				root := NewWithOpts(&InjectorOpts{
					HookScopeStarting: []func(context.Context, *Scope) error{veto(calls, "h1"), nil, veto(calls, "h2")},
				})
				root.AddScopeStartingHook(veto(calls, "h3"))
				root.AddScopeStartingHook(nil)
				return root
			},
			trigger: start,
		},
		{
			name: "HookScopeStarted",
			register: func(calls *callRecorder) *RootScope {
				root := NewWithOpts(&InjectorOpts{
					HookScopeStarted: []func(context.Context, *Scope){observer(calls, "h1"), nil, observer(calls, "h2")},
				})
				root.AddScopeStartedHook(observer(calls, "h3"))
				root.AddScopeStartedHook(nil)
				return root
			},
			trigger: start,
		},
		{
			name: "HookScopeClosing",
			register: func(calls *callRecorder) *RootScope {
				root := NewWithOpts(&InjectorOpts{
					HookScopeClosing: []func(context.Context, *Scope){observer(calls, "h1"), nil, observer(calls, "h2")},
				})
				root.AddScopeClosingHook(observer(calls, "h3"))
				root.AddScopeClosingHook(nil)
				return root
			},
			trigger: shutdown,
		},
		{
			name: "HookScopeClosed",
			register: func(calls *callRecorder) *RootScope {
				root := NewWithOpts(&InjectorOpts{
					HookScopeClosed: []func(context.Context, *Scope){observer(calls, "h1"), nil, observer(calls, "h2")},
				})
				root.AddScopeClosedHook(observer(calls, "h3"))
				root.AddScopeClosedHook(nil)
				return root
			},
			trigger: shutdown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			calls := &callRecorder{}
			root := tt.register(calls)
			require.NoError(t, root.Start())
			tenant := root.Scope("tenant")

			assert.NotPanics(t, func() { tt.trigger(t, tenant) })
			assert.Equal(t, []string{"h1 tenant", "h2 tenant", "h3 tenant"}, calls.list())
		})
	}
}

func TestScopeStartingHook_Veto(t *testing.T) {
	t.Parallel()

	errVeto := errors.New("veto")

	tests := []struct {
		name     string
		behavior func() error
		check    func(t *testing.T, err error)
		starts   int
		recorded []string
	}{
		{
			name:     "returns nil",
			behavior: func() error { return nil },
			check:    func(t *testing.T, err error) { t.Helper(); assert.NoError(t, err) },
			starts:   1,
			recorded: []string{"tenant"},
		},
		{
			name:     "returns errVeto",
			behavior: func() error { return errVeto },
			check: func(t *testing.T, err error) {
				t.Helper()
				assert.ErrorIs(t, err, errVeto)
				assert.ErrorContains(t, err, "scope `tenant`")
			},
		},
		{
			name:     "panics",
			behavior: func() error { panic("veto-boom") },
			check: func(t *testing.T, err error) {
				t.Helper()
				assert.EqualError(t, err, "DI: scope `tenant` start aborted: DI: scope starting hook panicked: veto-boom")
			},
		},
		{
			name:     "panics with errVeto",
			behavior: func() error { panic(errVeto) },
			check: func(t *testing.T, err error) {
				t.Helper()
				assert.ErrorIs(t, err, errVeto)
				assert.ErrorContains(t, err, "scope starting hook panicked")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			require.NoError(t, root.Start())
			tenant := root.Scope("tenant")
			starts := &countingStarters{}
			provideCountedStarter(tenant, starts, "worker")
			started := &callRecorder{}
			root.AddScopeStartingHook(func(context.Context, *Scope) error { return tt.behavior() })
			root.AddScopeStartedHook(func(_ context.Context, scope *Scope) { started.record(scope.Name()) })

			var err error
			require.NotPanics(t, func() { err = tenant.Start() })

			tt.check(t, err)
			assert.Equal(t, tt.starts, starts.count("worker"))
			assert.Equal(t, tt.recorded, started.list())
		})
	}
}

func TestScopeHooks_LaterPanicLogged(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	logs := &callRecorder{}
	panics := &callRecorder{}
	root := NewWithOpts(&InjectorOpts{
		Logf: func(format string, args ...any) { logs.record(fmt.Sprintf(format, args...)) },
		HookContainerPanic: func(_ *RootScope, point string, recovered any) {
			panics.record(fmt.Sprintf("%s %v", point, recovered))
		},
	})
	root.AddScopeClosingHook(func(context.Context, *Scope) { panic("first") })
	root.AddScopeClosingHook(func(context.Context, *Scope) { panic("second") })
	require.NoError(t, root.Start())
	tenant := root.Scope("tenant")

	tenant.Shutdown()

	is.Equal([]string{"scope-closing first"}, panics.list())
	is.Contains(logs.list(), "DI: scope-closing hook panicked: second")
	for _, line := range logs.list() {
		is.NotContains(line, "container scope-closing")
	}
}

// scopeShutdowner records "shutdown <scope>" when shut down.
type scopeShutdowner struct {
	scope string
	rec   *callRecorder
}

func (s *scopeShutdowner) Shutdown() error {
	s.rec.record("shutdown " + s.scope)
	return nil
}

// provideBuiltScopeShutdowner registers and builds a scopeShutdowner in scope.
func provideBuiltScopeShutdowner(t *testing.T, scope *Scope, rec *callRecorder) {
	t.Helper()
	ProvideNamed(scope, "svc", func(Injector) (*scopeShutdowner, error) {
		return &scopeShutdowner{scope: scope.Name(), rec: rec}, nil
	})
	_, err := InvokeNamed[*scopeShutdowner](scope, "svc")
	require.NoError(t, err)
}

func TestScopeStartedHook_SkippedWhileClosing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		closing string // the scope whose shutdown is held in its Closing hook
		// the shutdown begins from a Starting hook of the scope, after the
		// Starting check and before the run claims its services
		fromStartingHook bool
	}{
		{name: "the scope itself", closing: "tenant"},
		{name: "its parent", closing: "parent"},
		{name: "the scope itself, from its Starting hook", closing: "tenant", fromStartingHook: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			entered := make(chan struct{})
			release := make(chan struct{})
			var started atomic.Int32
			shutdownDone := make(chan struct{})
			var shuttingDown *Scope
			shutDown := func() {
				go func() {
					defer close(shutdownDone)
					shuttingDown.Shutdown()
				}()
				<-entered
			}
			root := New()
			if tt.fromStartingHook {
				root.AddScopeStartingHook(func(context.Context, *Scope) error {
					shutDown()
					return nil
				})
			}
			root.AddScopeStartedHook(func(context.Context, *Scope) { started.Add(1) })
			root.AddScopeClosingHook(func(_ context.Context, scope *Scope) {
				if scope.Name() == tt.closing {
					close(entered)
					<-release
				}
			})
			require.NoError(t, root.Start())
			parent := root.Scope("parent")
			tenant := parent.Scope("tenant")
			errUnused := errors.New("unused")
			w := &flakyStarter{err: errUnused}
			provideLazy(tenant, "w", w)
			shuttingDown = map[string]*Scope{"parent": parent, "tenant": tenant}[tt.closing]

			if !tt.fromStartingHook {
				shutDown()
			}

			is.NoError(tenant.Start())

			close(release)
			<-shutdownDone
			is.Equal(int32(0), w.calls.Load(), "w was started")
			is.Equal(int32(0), started.Load(), "HookScopeStarted ran")
		})
	}
}

func TestScopeStartingHook_SkippedWhileClosing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		closing string // the scope whose shutdown is held in its Closing hook
	}{
		{name: "the scope itself", closing: "tenant"},
		{name: "its parent", closing: "parent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			entered := make(chan struct{})
			release := make(chan struct{})
			var starting, started atomic.Int32
			root := New()
			root.AddScopeStartingHook(func(context.Context, *Scope) error {
				starting.Add(1)
				return nil
			})
			root.AddScopeStartedHook(func(context.Context, *Scope) { started.Add(1) })
			root.AddScopeClosingHook(func(_ context.Context, scope *Scope) {
				if scope.Name() == tt.closing {
					close(entered)
					<-release
				}
			})
			require.NoError(t, root.Start())
			parent := root.Scope("parent")
			tenant := parent.Scope("tenant")
			starts := &countingStarters{}
			provideCountedStarter(tenant, starts, "w")
			shuttingDown := map[string]*Scope{"parent": parent, "tenant": tenant}[tt.closing]

			shutdownDone := make(chan struct{})
			go func() {
				defer close(shutdownDone)
				shuttingDown.Shutdown()
			}()
			<-entered

			is.NoError(tenant.Start())

			close(release)
			<-shutdownDone
			is.Zero(starts.count("w"), "w was started")
			is.Equal(int32(0), starting.Load(), "HookScopeStarting ran")
			is.Equal(int32(0), started.Load(), "HookScopeStarted ran")
		})
	}
}

func TestScopeStartedHook_ShutdownBeforeClaim(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	var started atomic.Int32
	root := New()
	root.AddScopeStartedHook(func(context.Context, *Scope) { started.Add(1) })
	require.NoError(t, root.Start())
	tenant := root.Scope("tenant")
	starts := &countingStarters{}
	provideCountedStarter(tenant, starts, "w")
	pause := pauseCollection(tenant.Scope("jobs"))

	startErr := make(chan error, 1)
	go func() { startErr <- tenant.Start() }()
	<-pause.reached
	tenant.Shutdown()
	close(pause.release)

	is.NoError(<-startErr)
	is.Zero(starts.count("w"))
	// as for a tenant.Start() called after tenant.Shutdown() returned
	is.Equal(int32(1), started.Load(), "HookScopeStarted runs")
}

func TestScopeHooks_ClosingClosed(t *testing.T) {
	t.Parallel()

	nested := []string{"closing jobs", "shutdown jobs", "closed jobs", "closing tenant", "shutdown tenant", "closed tenant"}

	tests := []struct {
		name      string
		operation func(root *RootScope, tenant, jobs *Scope)
		events    []string
	}{
		{name: "tenant.Shutdown()", operation: func(_ *RootScope, tenant, _ *Scope) { tenant.Shutdown() }, events: nested},
		{name: "root.Shutdown()", operation: func(root *RootScope, _, _ *Scope) { root.Shutdown() }, events: nested},
		{name: "jobs.Shutdown()", operation: func(_ *RootScope, _, jobs *Scope) { jobs.Shutdown() }, events: []string{"closing jobs", "shutdown jobs", "closed jobs"}},
		{
			name: "tenant.Shutdown() then root.Shutdown()",
			operation: func(root *RootScope, tenant, _ *Scope) {
				tenant.Shutdown()
				root.Shutdown()
			},
			events: append(append([]string{}, nested...), "closing tenant", "closed tenant"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			rec := &callRecorder{}
			tenant := root.Scope("tenant")
			jobs := tenant.Scope("jobs")
			provideBuiltScopeShutdowner(t, tenant, rec)
			provideBuiltScopeShutdowner(t, jobs, rec)
			root.AddScopeClosingHook(func(_ context.Context, scope *Scope) { rec.record("closing " + scope.Name()) })
			root.AddScopeClosedHook(func(_ context.Context, scope *Scope) { rec.record("closed " + scope.Name()) })

			tt.operation(root, tenant, jobs)

			assert.Equal(t, tt.events, rec.list())
		})
	}
}

func TestScopeHooks_RootFiresNone(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	var calls atomic.Int32
	root.AddScopeStartingHook(func(context.Context, *Scope) error { calls.Add(1); return nil })
	root.AddScopeStartedHook(func(context.Context, *Scope) { calls.Add(1) })
	root.AddScopeClosingHook(func(context.Context, *Scope) { calls.Add(1) })
	root.AddScopeClosedHook(func(context.Context, *Scope) { calls.Add(1) })

	require.NoError(t, root.Start())
	root.Shutdown()

	assert.Equal(t, int32(0), calls.Load())
}

// countingShutdowner counts its Shutdown calls.
type countingShutdowner struct{ shutdowns atomic.Int32 }

func (s *countingShutdowner) Shutdown() error {
	s.shutdowns.Add(1)
	return nil
}

func TestScopeHooks_ObserverPanic(t *testing.T) {
	t.Parallel()

	type dispatched struct {
		point string
		value any
	}

	tests := []struct {
		name       string
		register   func(root *RootScope)
		recordPol  bool
		operation  func(tenant *Scope) error
		panics     bool
		dispatched []dispatched
		shutdowns  int32
	}{
		{
			name:      "HookScopeClosing, default policy, tenant.Shutdown()",
			register:  func(root *RootScope) { root.AddScopeClosingHook(func(context.Context, *Scope) { panic("scope-boom") }) },
			operation: func(tenant *Scope) error { tenant.Shutdown(); return nil },
			panics:    true,
			shutdowns: 1,
		},
		{
			name:       "HookScopeClosed, recording policy, tenant.Shutdown()",
			register:   func(root *RootScope) { root.AddScopeClosedHook(func(context.Context, *Scope) { panic("scope-boom") }) },
			recordPol:  true,
			operation:  func(tenant *Scope) error { tenant.Shutdown(); return nil },
			dispatched: []dispatched{{point: "scope-closed", value: "scope-boom"}},
			shutdowns:  1,
		},
		{
			name:       "HookScopeClosing, recording policy, tenant.Shutdown()",
			register:   func(root *RootScope) { root.AddScopeClosingHook(func(context.Context, *Scope) { panic("scope-boom") }) },
			recordPol:  true,
			operation:  func(tenant *Scope) error { tenant.Shutdown(); return nil },
			dispatched: []dispatched{{point: "scope-closing", value: "scope-boom"}},
			shutdowns:  1,
		},
		{
			name:       "HookScopeStarted, recording policy, tenant.Start()",
			register:   func(root *RootScope) { root.AddScopeStartedHook(func(context.Context, *Scope) { panic("scope-boom") }) },
			recordPol:  true,
			operation:  func(tenant *Scope) error { return tenant.Start() },
			dispatched: []dispatched{{point: "scope-started", value: "scope-boom"}},
			shutdowns:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			var calls []dispatched
			opts := &InjectorOpts{}
			if tt.recordPol {
				opts.HookContainerPanic = func(_ *RootScope, hook string, recovered any) {
					calls = append(calls, dispatched{point: hook, value: recovered})
				}
			}
			root := NewWithOpts(opts)
			require.NoError(t, root.Start())
			tenant := root.Scope("tenant")
			svc := &countingShutdowner{}
			provideLazy(tenant, "svc", svc)
			_, err := InvokeNamed[*countingShutdowner](tenant, "svc")
			require.NoError(t, err)
			tt.register(root)

			if tt.panics {
				is.PanicsWithValue("scope-boom", func() { _ = tt.operation(tenant) })
			} else {
				is.NotPanics(func() { is.NoError(tt.operation(tenant)) })
			}

			is.Equal(tt.dispatched, calls)
			is.Equal(tt.shutdowns, svc.shutdowns.Load())
		})
	}
}

func TestScopeStartedHook_PanicDefault(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	require.NoError(t, root.Start())
	tenant := root.Scope("tenant")
	rec := &callRecorder{}
	provideBuiltScopeShutdowner(t, tenant, rec)
	starts := &countingStarters{}
	provideCountedStarter(tenant, starts, "worker")
	var second atomic.Int32
	root.AddScopeStartedHook(func(context.Context, *Scope) { panic("scope-boom") })
	root.AddScopeStartedHook(func(context.Context, *Scope) { second.Add(1) })

	is.PanicsWithValue("scope-boom", func() { _ = tenant.Start() })

	is.Equal(int32(1), second.Load(), "the second Started hook ran before the panic was re-raised")
	is.Equal(1, starts.count("worker"))
	is.Zero(countOf(rec.list(), "shutdown tenant"), "svc had Shutdown called")
}

// TestScopeHooks_ObserverPanicThroughParent pins that a child scope hook
// panic during a parent's shutdown, where the child shuts down on its own
// goroutine, reaches the caller after the whole shutdown completed.
func TestScopeHooks_ObserverPanicThroughParent(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	tenant := root.Scope("tenant")
	svc := &countingShutdowner{}
	provideLazy(tenant, "svc", svc)
	_, err := InvokeNamed[*countingShutdowner](tenant, "svc")
	require.NoError(t, err)
	root.AddScopeClosingHook(func(context.Context, *Scope) { panic("scope-boom") })

	is.PanicsWithValue("scope-boom", func() { root.Shutdown() })
	is.Equal(int32(1), svc.shutdowns.Load())
	is.ErrorIs(root.Start(), ErrStartAfterShutdown)
}

func TestScopeHooks_NilContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation func(tenant *Scope)
	}{
		{name: "tenant.StartWithContext(nil)", operation: func(tenant *Scope) { _ = tenant.StartWithContext(nil) }},       //nolint:staticcheck // nil is the subject
		{name: "tenant.ShutdownWithContext(nil)", operation: func(tenant *Scope) { _ = tenant.ShutdownWithContext(nil) }}, //nolint:staticcheck // nil is the subject
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			require.NoError(t, root.Start())
			tenant := root.Scope("tenant")
			var contexts []context.Context
			record := func(ctx context.Context, _ *Scope) { contexts = append(contexts, ctx) }
			root.AddScopeStartingHook(func(ctx context.Context, scope *Scope) error { record(ctx, scope); return nil })
			root.AddScopeStartedHook(record)
			root.AddScopeClosingHook(record)
			root.AddScopeClosedHook(record)

			tt.operation(tenant)

			is.Len(contexts, 2)
			for _, ctx := range contexts {
				is.NotNil(ctx)
			}
		})
	}
}
