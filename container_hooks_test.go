package doger

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// provideBuildCountedServer registers lazy Starter "server" in root and
// returns the counter of its provider calls.
func provideBuildCountedServer(root *RootScope) *atomic.Int32 {
	builds := &atomic.Int32{}
	ProvideNamed(root, "server", func(Injector) (*startWithErr, error) {
		builds.Add(1)
		return &startWithErr{}, nil
	})
	return builds
}

func TestContainerStartingHook_Veto(t *testing.T) {
	t.Parallel()

	errMissing := errors.New("missing DB_URL")
	errLicense := errors.New("no license")

	tests := []struct {
		name    string
		results []error
		wantErr error
		builds  int32
		ran     int32
	}{
		{name: "nil", results: []error{nil}, wantErr: nil, builds: 1, ran: 1},
		{name: "nil, nil", results: []error{nil, nil}, wantErr: nil, builds: 1, ran: 2},
		{name: "missing DB_URL", results: []error{errMissing}, wantErr: errMissing, builds: 0, ran: 1},
		{name: "nil, no license, nil", results: []error{nil, errLicense, nil}, wantErr: errLicense, builds: 0, ran: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			var ran atomic.Int32
			opts := &InjectorOpts{}
			for _, result := range tt.results {
				opts.HookContainerStarting = append(opts.HookContainerStarting, func(context.Context, *RootScope) error {
					ran.Add(1)
					return result
				})
			}
			root := NewWithOpts(opts)
			builds := provideBuildCountedServer(root)

			err := root.Start()

			if tt.wantErr == nil {
				is.NoError(err)
			} else {
				is.ErrorIs(err, tt.wantErr)
			}
			is.Equal(tt.builds, builds.Load())
			is.Equal(tt.ran, ran.Load())
		})
	}
}

func TestContainerStartingHook_BeforeContextCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result func(ctx context.Context) error
	}{
		{name: "returns ctx.Err()", result: func(ctx context.Context) error { return ctx.Err() }},
		{name: "returns nil", result: func(context.Context) error { return nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			builds := provideBuildCountedServer(root)
			var ran atomic.Int32
			root.AddContainerStartingHook(func(ctx context.Context, _ *RootScope) error {
				ran.Add(1)
				return tt.result(ctx)
			})

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := root.StartWithContext(ctx)

			is.ErrorIs(err, context.Canceled)
			is.Equal(int32(1), ran.Load())
			is.Equal(int32(0), builds.Load())
		})
	}
}

func TestContainerStartingHook_ReceivesContext(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	var gotValue any
	var gotRoot *RootScope
	root.AddContainerStartingHook(func(ctx context.Context, r *RootScope) error {
		gotValue = ctx.Value(ctxTestKey)
		gotRoot = r
		return nil
	})

	ctx := context.WithValue(context.Background(), ctxTestKey, "abc")
	require.NoError(t, root.StartWithContext(ctx))

	is.Equal("abc", gotValue)
	is.Same(root, gotRoot)
}

// recordingStarterRec is a Starter recording "<name>.Start".
type recordingStarterRec struct {
	name string
	rec  *callRecorder
}

func (s *recordingStarterRec) Start() error {
	s.rec.record(s.name + ".Start")
	return nil
}

// recordingRunner is a Runner recording "<name>.Run".
type recordingRunner struct {
	name string
	rec  *callRecorder
}

func (r *recordingRunner) Run(context.Context) error {
	r.rec.record(r.name + ".Run")
	return nil
}

// addRecordingContainerHooks registers one hook per given point, each
// recording "<point>-hook".
func addRecordingContainerHooks(root *RootScope, rec *callRecorder, points ...string) {
	for _, point := range points {
		label := point + "-hook"
		switch point {
		case "starting":
			root.AddContainerStartingHook(func(context.Context, *RootScope) error { rec.record(label); return nil })
		case "started":
			root.AddContainerStartedHook(func(context.Context, *RootScope) { rec.record(label) })
		case "ready":
			root.AddContainerReadyHook(func(context.Context, *RootScope) { rec.record(label) })
		case "failed":
			root.AddContainerFailedHook(func(context.Context, *RootScope, error) { rec.record(label) })
		default:
			panic("unknown hook point " + point)
		}
	}
}

func TestContainerHooks_StartOrder(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	rec := &callRecorder{}
	ProvideNamedValue(root, "server", &recordingStarterRec{name: "server", rec: rec})
	ProvideNamedValue(root, "migrate", &recordingRunner{name: "migrate", rec: rec}, AsRunnable())
	addRecordingContainerHooks(root, rec, "starting", "started", "ready")

	require.NoError(t, root.Start())
	assert.Equal(t, []string{"starting-hook", "server.Start", "migrate.Run", "started-hook", "ready-hook"}, rec.list())
}

func TestContainerHooks_EmptyContainer(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	rec := &callRecorder{}
	addRecordingContainerHooks(root, rec, "starting", "started", "ready", "failed")

	require.NoError(t, root.Start())
	assert.Equal(t, []string{"starting-hook", "started-hook", "ready-hook"}, rec.list())
}

// failingRunner is a Runner returning err.
type failingRunner struct{ err error }

func (r *failingRunner) Run(context.Context) error { return r.err }

// failingJob is a component of the kinds used by failure-point tests.
type failingJob struct{}

func TestContainerFailedHook_FailurePoint(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("boom")

	tests := []struct {
		name  string
		setup func(root *RootScope) // registers the failing piece
		veto  error                 // returned by the Starting hook
		ctx   func() context.Context
		cause error
	}{
		{name: "Starting hook veto", veto: errBoom, cause: errBoom},
		{
			name: "context already cancelled",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			cause: context.Canceled,
		},
		{
			name: "provider of a Starter service",
			setup: func(root *RootScope) {
				ProvideNamed(root, "server", func(Injector) (*startWithErr, error) { return nil, errBoom })
			},
			cause: errBoom,
		},
		{
			name: "StageBeforeStart kind handler",
			setup: func(root *RootScope) {
				k := NewKind[*failingJob, struct{}]("k")
				ProvideNamedValue(root, "job", &failingJob{}, k.With(struct{}{}))
				k.Handle(root, StageBeforeStart, func(context.Context, Component[*failingJob, struct{}]) error { return errBoom })
			},
			cause: errBoom,
		},
		{
			name: "Start method of a Starter",
			setup: func(root *RootScope) {
				ProvideNamedValue(root, "server", &errorStarter{err: errBoom})
			},
			cause: errBoom,
		},
		{
			name: "Run method of a Runnable",
			setup: func(root *RootScope) {
				ProvideNamedValue(root, "migrate", &failingRunner{err: errBoom}, AsRunnable())
			},
			cause: errBoom,
		},
		{
			name: "StageAfterStart kind handler",
			setup: func(root *RootScope) {
				k := NewKind[*failingJob, struct{}]("k")
				ProvideNamedValue(root, "job", &failingJob{}, k.With(struct{}{}))
				k.Handle(root, StageAfterStart, func(context.Context, Component[*failingJob, struct{}]) error { return errBoom })
			},
			cause: errBoom,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			rec := &callRecorder{}
			root.AddContainerStartingHook(func(context.Context, *RootScope) error {
				rec.record("starting")
				return tt.veto
			})
			root.AddContainerStartedHook(func(context.Context, *RootScope) { rec.record("started") })
			root.AddContainerReadyHook(func(context.Context, *RootScope) { rec.record("ready") })
			var failedErr error
			root.AddContainerFailedHook(func(_ context.Context, _ *RootScope, err error) {
				rec.record("failed")
				failedErr = err
			})
			if tt.setup != nil {
				tt.setup(root)
			}
			ctx := context.Background()
			if tt.ctx != nil {
				ctx = tt.ctx()
			}

			err := root.StartWithContext(ctx)

			is.ErrorIs(err, tt.cause)
			is.Same(err, failedErr, "the Failed hook received the error Start returned")
			is.Equal([]string{"starting", "failed"}, rec.list())
		})
	}
}

func TestContainerFailedHook_GuardErrors(t *testing.T) {
	t.Parallel()

	errVeto := errors.New("veto")

	tests := []struct {
		name    string
		veto    error
		prepare func(root *RootScope)
		wantErr error
	}{
		{name: "started", prepare: func(root *RootScope) { _ = root.Start() }, wantErr: ErrAlreadyStarted},
		{name: "failed after a Starting veto", veto: errVeto, prepare: func(root *RootScope) { _ = root.Start() }, wantErr: ErrAlreadyStarted},
		{name: "shut down", prepare: func(root *RootScope) { _ = root.Shutdown() }, wantErr: ErrStartAfterShutdown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			var starting, failed atomic.Int32
			root.AddContainerStartingHook(func(context.Context, *RootScope) error {
				starting.Add(1)
				return tt.veto
			})
			root.AddContainerFailedHook(func(context.Context, *RootScope, error) { failed.Add(1) })
			tt.prepare(root)
			starting.Store(0)
			failed.Store(0)

			err := root.Start()

			is.ErrorIs(err, tt.wantErr)
			is.Equal(int32(0), starting.Load())
			is.Equal(int32(0), failed.Load())
		})
	}
}

func TestContainerFailedHook_PipelinePanic(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("boom")

	tests := []struct {
		name  string
		opts  func(panicFor func(name string)) *InjectorOpts
		value any
		check func(t *testing.T, failedErr error)
	}{
		{
			name: "HookBeforeStart, sequential",
			opts: func(panicFor func(string)) *InjectorOpts {
				return &InjectorOpts{HookBeforeStart: []func(*Scope, string){func(_ *Scope, name string) { panicFor(name) }}}
			},
			value: errBoom,
			check: func(t *testing.T, failedErr error) { t.Helper(); assert.ErrorIs(t, failedErr, errBoom) },
		},
		{
			name: "HookAfterBuild, sequential",
			opts: func(panicFor func(string)) *InjectorOpts {
				return &InjectorOpts{HookAfterBuild: []func(*Scope, string, any){func(_ *Scope, name string, _ any) { panicFor(name) }}}
			},
			value: "boom",
			check: func(t *testing.T, failedErr error) { t.Helper(); assert.ErrorContains(t, failedErr, "boom") },
		},
		{
			name: "HookAfterStart, StartParallelism 4",
			opts: func(panicFor func(string)) *InjectorOpts {
				return &InjectorOpts{
					StartParallelism: 4,
					HookAfterStart:   []func(*Scope, string, error){func(_ *Scope, name string, _ error) { panicFor(name) }},
				}
			},
			value: errBoom,
			check: func(t *testing.T, failedErr error) { t.Helper(); assert.ErrorIs(t, failedErr, errBoom) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := NewWithOpts(tt.opts(func(name string) {
				if name == "a" {
					panic(tt.value)
				}
			}))
			rec := &callRecorder{}
			root.AddContainerStartingHook(func(context.Context, *RootScope) error { rec.record("starting"); return nil })
			root.AddContainerStartedHook(func(context.Context, *RootScope) { rec.record("started") })
			root.AddContainerReadyHook(func(context.Context, *RootScope) { rec.record("ready") })
			var failedErr error
			root.AddContainerFailedHook(func(_ context.Context, _ *RootScope, err error) {
				rec.record("failed")
				failedErr = err
			})
			provideLazy(root, "a", &errorStarter{})
			provideLazy(root, "b", &errorStarter{})

			is.PanicsWithValue(tt.value, func() { _ = root.Start() })

			is.Equal([]string{"starting", "failed"}, rec.list())
			tt.check(t, failedErr)
			is.Equal(PhaseFailed, root.Phase())
		})
	}
}

func TestContainerFailedHook_PanicDuringPipelinePanic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		policy bool
	}{
		{name: "nil policy logs it", policy: false},
		{name: "policy receives it", policy: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			logs := &callRecorder{}
			panics := &callRecorder{}
			opts := &InjectorOpts{
				HookBeforeStart: []func(*Scope, string){func(*Scope, string) { panic("pipeline") }},
				Logf:            func(format string, args ...any) { logs.record(fmt.Sprintf(format, args...)) },
			}
			if tt.policy {
				opts.HookContainerPanic = func(_ *RootScope, point string, recovered any) {
					panics.record(fmt.Sprintf("%s %v", point, recovered))
				}
			}
			root := NewWithOpts(opts)
			root.AddContainerFailedHook(func(context.Context, *RootScope, error) { panic("failed-hook") })
			provideLazy(root, "a", &errorStarter{})

			is.PanicsWithValue("pipeline", func() { _ = root.Start() })

			if tt.policy {
				is.Equal([]string{"failed failed-hook"}, panics.list())
				return
			}
			is.True(slices.ContainsFunc(logs.list(), func(line string) bool { return strings.Contains(line, "failed-hook") }),
				"logs: %v", logs.list())
		})
	}
}

// recordingShutdowner records "<name>.Shutdown" and returns err.
type recordingShutdowner struct {
	name string
	rec  *callRecorder
	err  error
}

func (s *recordingShutdowner) Shutdown() error {
	s.rec.record(s.name + ".Shutdown")
	return s.err
}

// provideBuiltShutdowner registers lazy Shutdowner name in root and builds it.
func provideBuiltShutdowner(t *testing.T, root *RootScope, rec *callRecorder, name string, err error) {
	t.Helper()
	ProvideNamed(root, name, func(Injector) (*recordingShutdowner, error) {
		return &recordingShutdowner{name: name, rec: rec, err: err}, nil
	})
	_, invokeErr := InvokeNamed[*recordingShutdowner](root, name)
	require.NoError(t, invokeErr)
}

func TestContainerClosingClosedHooks_Order(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(root *RootScope)
	}{
		{name: "configured (never started)", prepare: func(*RootScope) {}},
		{name: "ready", prepare: func(root *RootScope) { _ = root.Start() }},
		{
			name: "failed",
			prepare: func(root *RootScope) {
				root.AddContainerStartingHook(func(context.Context, *RootScope) error { return errors.New("veto") })
				_ = root.Start()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			rec := &callRecorder{}
			provideBuiltShutdowner(t, root, rec, "db", nil)
			var closedReport *ShutdownReport
			root.AddContainerClosingHook(func(context.Context, *RootScope) { rec.record("closing-hook") })
			root.AddContainerClosedHook(func(_ context.Context, _ *RootScope, report *ShutdownReport) {
				rec.record("closed-hook")
				closedReport = report
			})
			tt.prepare(root)

			report := root.Shutdown()

			is.Equal([]string{"closing-hook", "db.Shutdown", "closed-hook"}, rec.list())
			is.Same(report, closedReport)
		})
	}
}

func TestContainerClosedHook_ReportErrors(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	provideBuiltShutdowner(t, root, &callRecorder{}, "db", errors.New("flush failed"))
	var closedReport *ShutdownReport
	root.AddContainerClosedHook(func(_ context.Context, _ *RootScope, report *ShutdownReport) {
		closedReport = report
	})

	root.Shutdown()

	require.NotNil(t, closedReport)
	is.False(closedReport.Succeed)
	is.Len(closedReport.Errors, 1)
	is.Contains(closedReport.Errors, newServiceDescription(root.ID(), root.Name(), "db"))
}

func TestContainerHooks_ShutdownOnSignalsWithContext(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	var closing, closed atomic.Int32
	var closingCtx, closedCtx context.Context
	root.AddContainerClosingHook(func(ctx context.Context, _ *RootScope) {
		closing.Add(1)
		closingCtx = ctx
	})
	root.AddContainerClosedHook(func(ctx context.Context, _ *RootScope, _ *ShutdownReport) {
		closed.Add(1)
		closedCtx = ctx
	})

	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxTestKey, "r-1"))
	cancel()
	_, _ = root.ShutdownOnSignalsWithContext(ctx)

	is.Equal(int32(1), closing.Load())
	is.Equal(int32(1), closed.Load())
	for hook, hookCtx := range map[string]context.Context{"closing": closingCtx, "closed": closedCtx} {
		if is.NotNil(hookCtx, hook) {
			is.NoError(hookCtx.Err(), hook)
			is.Equal("r-1", hookCtx.Value(ctxTestKey), hook)
		}
	}
}

func TestContainerHooks_HookCallsShutdown(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("boom")

	// callShutdown records the report of a nested root Shutdown.
	type nested struct{ report *ShutdownReport }

	tests := []struct {
		name      string
		setup     func(root *RootScope)
		register  func(root *RootScope, got *nested)
		operation func(t *testing.T, root *RootScope)
	}{
		{
			name:  "Started",
			setup: func(root *RootScope) { ProvideNamedValue(root, "server", &startWithErr{}) },
			register: func(root *RootScope, got *nested) {
				root.AddContainerStartedHook(func(_ context.Context, r *RootScope) { got.report = r.Shutdown() })
			},
			operation: func(t *testing.T, root *RootScope) { t.Helper(); assert.NoError(t, root.Start()) },
		},
		{
			name:  "Ready",
			setup: func(root *RootScope) { ProvideNamedValue(root, "server", &startWithErr{}) },
			register: func(root *RootScope, got *nested) {
				root.AddContainerReadyHook(func(_ context.Context, r *RootScope) { got.report = r.Shutdown() })
			},
			operation: func(t *testing.T, root *RootScope) { t.Helper(); assert.NoError(t, root.Start()) },
		},
		{
			name:  "Failed",
			setup: func(root *RootScope) { ProvideNamedValue(root, "server", &errorStarter{err: errBoom}) },
			register: func(root *RootScope, got *nested) {
				root.AddContainerFailedHook(func(_ context.Context, r *RootScope, _ error) { got.report = r.Shutdown() })
			},
			operation: func(t *testing.T, root *RootScope) { t.Helper(); assert.ErrorIs(t, root.Start(), errBoom) },
		},
		{
			name:  "Closed",
			setup: func(*RootScope) {},
			register: func(root *RootScope, got *nested) {
				root.AddContainerClosedHook(func(_ context.Context, r *RootScope, _ *ShutdownReport) { got.report = r.Shutdown() })
			},
			operation: func(t *testing.T, root *RootScope) { t.Helper(); assert.True(t, root.Shutdown().Succeed) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			provideBuiltShutdowner(t, root, &callRecorder{}, "db", nil)
			var closing, closed atomic.Int32
			root.AddContainerClosingHook(func(context.Context, *RootScope) { closing.Add(1) })
			root.AddContainerClosedHook(func(context.Context, *RootScope, *ShutdownReport) { closed.Add(1) })
			tt.setup(root)
			got := &nested{}
			tt.register(root, got)

			tt.operation(t, root)

			is.Equal(int32(1), closing.Load())
			is.Equal(int32(1), closed.Load())
			require.NotNil(t, got.report)
			is.True(got.report.Succeed)
		})
	}
}

// errHookVeto is a Starting hook's own veto.
var errHookVeto = errors.New("veto")

func TestContainerHooks_HookCallsShutdownWithDeadline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		register func(root *RootScope, record func(*ShutdownReport))
		check    func(t *testing.T, root *RootScope, failed *atomic.Int32)
	}{
		{
			name: "Starting",
			register: func(root *RootScope, record func(*ShutdownReport)) {
				root.AddContainerStartingHook(func(_ context.Context, r *RootScope) error {
					// not derived from the Start context, which Shutdown cancels
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					record(r.ShutdownWithContext(ctx))
					return nil
				})
			},
			check: func(t *testing.T, root *RootScope, failed *atomic.Int32) {
				t.Helper()
				assert.ErrorIs(t, root.Start(), ErrStartAfterShutdown)
				assert.Equal(t, int32(1), failed.Load())
			},
		},
		{
			name: "Starting returning errVeto",
			register: func(root *RootScope, record func(*ShutdownReport)) {
				root.AddContainerStartingHook(func(_ context.Context, r *RootScope) error {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					record(r.ShutdownWithContext(ctx))
					return errHookVeto
				})
			},
			check: func(t *testing.T, root *RootScope, failed *atomic.Int32) {
				t.Helper()
				err := root.Start()
				assert.ErrorIs(t, err, ErrStartAfterShutdown)
				assert.ErrorIs(t, err, errHookVeto)
				assert.Equal(t, int32(1), failed.Load())
			},
		},
		{
			name: "Closing",
			register: func(root *RootScope, record func(*ShutdownReport)) {
				root.AddContainerClosingHook(func(ctx context.Context, r *RootScope) {
					ctx, cancel := context.WithTimeout(ctx, time.Second)
					defer cancel()
					record(r.ShutdownWithContext(ctx))
				})
			},
			check: func(t *testing.T, root *RootScope, failed *atomic.Int32) {
				t.Helper()
				assert.True(t, root.Shutdown().Succeed)
				assert.Equal(t, int32(0), failed.Load())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				is := assert.New(t)

				root := New()
				var closing, closed, failed atomic.Int32
				root.AddContainerClosingHook(func(context.Context, *RootScope) { closing.Add(1) })
				root.AddContainerClosedHook(func(context.Context, *RootScope, *ShutdownReport) { closed.Add(1) })
				root.AddContainerFailedHook(func(context.Context, *RootScope, error) { failed.Add(1) })
				var nestedReport *ShutdownReport
				tt.register(root, func(report *ShutdownReport) { nestedReport = report })

				tt.check(t, root, &failed)

				require.NotNil(t, nestedReport)
				is.False(nestedReport.Succeed)
				is.NotEmpty(nestedReport.Errors)
				for _, err := range nestedReport.Errors {
					is.ErrorIs(err, context.DeadlineExceeded)
				}
				is.Equal(int32(1), closing.Load())
				is.Equal(int32(1), closed.Load())
			})
		})
	}
}

func TestContainerHooks_ShutdownDuringStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// setup registers the row's pieces; shutdown runs root Shutdown at the
		// row's point and returns once it was launched or returned.
		setup    func(root *RootScope, rec *callRecorder) (shutdown func())
		wantErr  error
		counts   map[string]int // started, ready, failed
		ordering func(t *testing.T, events []string)
	}{
		{
			name: "Start finishes first",
			setup: func(root *RootScope, rec *callRecorder) func() {
				entered := make(chan struct{})
				shutdownReturned := make(chan struct{})
				root.AddContainerStartedHook(func(context.Context, *RootScope) {
					close(entered)
					<-shutdownReturned
				})
				return func() {
					<-entered
					root.Shutdown()
					close(shutdownReturned)
				}
			},
			counts: map[string]int{"started-hook": 1, "ready-hook": 1, "failed-hook": 0},
			ordering: func(t *testing.T, events []string) {
				t.Helper()
				assert.Equal(t, []string{"started-hook", "closing-hook", "closed-hook", "ready-hook"}, events)
			},
		},
		{
			name: "Shutdown cancels Start",
			setup: func(root *RootScope, rec *callRecorder) func() {
				entered := make(chan struct{})
				ProvideNamedValue(root, "a", &ctxStarter{fn: func(ctx context.Context) error {
					close(entered)
					<-ctx.Done()
					rec.record("a.Start returned")
					return ctx.Err()
				}})
				return func() {
					<-entered
					root.Shutdown()
				}
			},
			wantErr: ErrStartAfterShutdown,
			counts:  map[string]int{"started-hook": 0, "ready-hook": 0, "failed-hook": 1},
			ordering: func(t *testing.T, events []string) {
				t.Helper()
				assert.Less(t, indexOf(events, "a.Start returned"), indexOf(events, "closing-hook"))
			},
		},
		{
			name: "Shutdown in the last step",
			setup: func(root *RootScope, rec *callRecorder) func() {
				k := NewKind[*failingJob, struct{}]("last")
				ProvideNamedValue(root, "job", &failingJob{}, k.With(struct{}{}))
				var wg sync.WaitGroup
				launched := make(chan struct{})
				k.Handle(root, StageAfterStart, func(context.Context, Component[*failingJob, struct{}]) error {
					wg.Go(func() { root.Shutdown() })
					close(launched)
					synctest.Wait() // Shutdown is blocked, waiting for this Start
					rec.record("handler returned")
					return nil
				})
				// Wait only after Go added to the group: a Wait racing the Add
				// at a zero counter is a data race
				return func() {
					<-launched
					wg.Wait()
				}
			},
			wantErr: ErrStartAfterShutdown,
			counts:  map[string]int{"started-hook": 0, "ready-hook": 0, "failed-hook": 1},
			ordering: func(t *testing.T, events []string) {
				t.Helper()
				assert.Less(t, indexOf(events, "handler returned"), indexOf(events, "closing-hook"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				is := assert.New(t)

				root := New()
				rec := &callRecorder{}
				provideBuiltShutdowner(t, root, &callRecorder{}, "db", nil)
				root.AddContainerStartedHook(func(context.Context, *RootScope) { rec.record("started-hook") })
				root.AddContainerReadyHook(func(context.Context, *RootScope) { rec.record("ready-hook") })
				root.AddContainerFailedHook(func(context.Context, *RootScope, error) { rec.record("failed-hook") })
				root.AddContainerClosingHook(func(context.Context, *RootScope) { rec.record("closing-hook") })
				root.AddContainerClosedHook(func(context.Context, *RootScope, *ShutdownReport) { rec.record("closed-hook") })
				shutdown := tt.setup(root, rec)

				startErr := make(chan error, 1)
				go func() { startErr <- root.Start() }()
				shutdown()
				err := <-startErr
				synctest.Wait()

				if tt.wantErr == nil {
					is.NoError(err)
				} else {
					is.ErrorIs(err, tt.wantErr)
				}
				events := rec.list()
				for hook, count := range tt.counts {
					is.Equal(count, countOf(events, hook), hook)
				}
				is.Equal(1, countOf(events, "closing-hook"))
				is.Equal(1, countOf(events, "closed-hook"))
				tt.ordering(t, events)
			})
		})
	}
}

// indexOf returns the index of event in events, or -1.
func indexOf(events []string, event string) int {
	return slices.Index(events, event)
}

// countOf counts event in events.
func countOf(events []string, event string) int {
	n := 0
	for _, e := range events {
		if e == event {
			n++
		}
	}
	return n
}

func TestContainerHooks_NilContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		register  func(root *RootScope, record func(context.Context))
		operation func(root *RootScope)
		count     int
	}{
		{
			name:  "StartWithContext",
			count: 3,
			register: func(root *RootScope, record func(context.Context)) {
				root.AddContainerStartingHook(func(ctx context.Context, _ *RootScope) error { record(ctx); return nil })
				root.AddContainerStartedHook(func(ctx context.Context, _ *RootScope) { record(ctx) })
				root.AddContainerReadyHook(func(ctx context.Context, _ *RootScope) { record(ctx) })
			},
			operation: func(root *RootScope) {
				_ = root.StartWithContext(nil) //nolint:staticcheck // nil context is the case under test
			},
		},
		{
			name:  "ShutdownWithContext",
			count: 2,
			register: func(root *RootScope, record func(context.Context)) {
				root.AddContainerClosingHook(func(ctx context.Context, _ *RootScope) { record(ctx) })
				root.AddContainerClosedHook(func(ctx context.Context, _ *RootScope, _ *ShutdownReport) { record(ctx) })
			},
			operation: func(root *RootScope) {
				_ = root.ShutdownWithContext(nil) //nolint:staticcheck // nil context is the case under test
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			var contexts []context.Context
			tt.register(root, func(ctx context.Context) { contexts = append(contexts, ctx) })

			is.NotPanics(func() { tt.operation(root) })

			is.Len(contexts, tt.count)
			for _, ctx := range contexts {
				is.NotNil(ctx)
			}
		})
	}
}

func TestContainerHooks_ObserverContexts(t *testing.T) {
	t.Parallel()

	type traceKey struct{}
	errVeto := errors.New("veto")

	tests := []struct {
		name      string
		register  func(root *RootScope, record func(context.Context, *RootScope))
		operation func(root *RootScope, ctx context.Context)
	}{
		{
			name: "Started",
			register: func(root *RootScope, record func(context.Context, *RootScope)) {
				root.AddContainerStartedHook(func(ctx context.Context, r *RootScope) { record(ctx, r) })
			},
			operation: func(root *RootScope, ctx context.Context) { _ = root.StartWithContext(ctx) },
		},
		{
			name: "Ready",
			register: func(root *RootScope, record func(context.Context, *RootScope)) {
				root.AddContainerReadyHook(func(ctx context.Context, r *RootScope) { record(ctx, r) })
			},
			operation: func(root *RootScope, ctx context.Context) { _ = root.StartWithContext(ctx) },
		},
		{
			name: "Failed",
			register: func(root *RootScope, record func(context.Context, *RootScope)) {
				root.AddContainerStartingHook(func(context.Context, *RootScope) error { return errVeto })
				root.AddContainerFailedHook(func(ctx context.Context, r *RootScope, _ error) { record(ctx, r) })
			},
			operation: func(root *RootScope, ctx context.Context) { _ = root.StartWithContext(ctx) },
		},
		{
			name: "Closing",
			register: func(root *RootScope, record func(context.Context, *RootScope)) {
				root.AddContainerClosingHook(func(ctx context.Context, r *RootScope) { record(ctx, r) })
			},
			operation: func(root *RootScope, ctx context.Context) { _ = root.ShutdownWithContext(ctx) },
		},
		{
			name: "Closed",
			register: func(root *RootScope, record func(context.Context, *RootScope)) {
				root.AddContainerClosedHook(func(ctx context.Context, r *RootScope, _ *ShutdownReport) { record(ctx, r) })
			},
			operation: func(root *RootScope, ctx context.Context) { _ = root.ShutdownWithContext(ctx) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			var received []context.Context
			var roots []*RootScope
			tt.register(root, func(ctx context.Context, r *RootScope) {
				received = append(received, ctx)
				roots = append(roots, r)
			})
			callerCtx := context.WithValue(context.Background(), traceKey{}, "abc")

			tt.operation(root, callerCtx)

			require.Len(t, received, 1)
			// the caller's context itself, not the operation's derived one,
			// which Start cancels before these hooks run
			assert.Same(t, callerCtx, received[0])
			assert.NoError(t, received[0].Err())
			assert.Equal(t, "abc", received[0].Value(traceKey{}))
			assert.Same(t, root, roots[0], "the hook receives the root it is registered on")
		})
	}
}

func TestContainerHooks_RegistrationOrder(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	rec := &callRecorder{}
	recordAs := func(name string) func(context.Context, *RootScope) {
		return func(context.Context, *RootScope) { rec.record(name) }
	}
	root := NewWithOpts(&InjectorOpts{
		HookContainerStarted: []func(context.Context, *RootScope){recordAs("opt-1"), recordAs("opt-2")},
	})
	root.AddContainerStartedHook(recordAs("add-1"))
	root.AddContainerStartedHook(recordAs("add-2"))

	require.NoError(t, root.Start())
	assert.Equal(t, []string{"opt-1", "opt-2", "add-1", "add-2"}, rec.list())
}

func TestContainerHooks_NilIgnored(t *testing.T) {
	t.Parallel()

	start := func(root *RootScope) { _ = root.Start() }
	shutdown := func(root *RootScope) { root.Shutdown() }

	tests := []struct {
		name      string
		build     func(ran *atomic.Int32) (*InjectorOpts, func(*RootScope))
		operation func(*RootScope)
	}{
		{
			name: "HookContainerStarting",
			build: func(ran *atomic.Int32) (*InjectorOpts, func(*RootScope)) {
				return &InjectorOpts{HookContainerStarting: []func(context.Context, *RootScope) error{
					nil, func(context.Context, *RootScope) error { ran.Add(1); return nil },
				}}, func(root *RootScope) {
					root.AddContainerStartingHook(nil)
				}
			},
			operation: start,
		},
		{
			name: "HookContainerStarted",
			build: func(ran *atomic.Int32) (*InjectorOpts, func(*RootScope)) {
				return &InjectorOpts{HookContainerStarted: []func(context.Context, *RootScope){
					nil, func(context.Context, *RootScope) { ran.Add(1) },
				}}, func(root *RootScope) {
					root.AddContainerStartedHook(nil)
				}
			},
			operation: start,
		},
		{
			name: "HookContainerReady",
			build: func(ran *atomic.Int32) (*InjectorOpts, func(*RootScope)) {
				return &InjectorOpts{HookContainerReady: []func(context.Context, *RootScope){
					nil, func(context.Context, *RootScope) { ran.Add(1) },
				}}, func(root *RootScope) {
					root.AddContainerReadyHook(nil)
				}
			},
			operation: start,
		},
		{
			name: "HookContainerFailed",
			build: func(ran *atomic.Int32) (*InjectorOpts, func(*RootScope)) {
				return &InjectorOpts{HookContainerFailed: []func(context.Context, *RootScope, error){
					nil, func(context.Context, *RootScope, error) { ran.Add(1) },
				}}, func(root *RootScope) {
					root.AddContainerFailedHook(nil)
					root.AddContainerStartingHook(func(context.Context, *RootScope) error { return errors.New("boom") })
				}
			},
			operation: start,
		},
		{
			name: "HookContainerClosing",
			build: func(ran *atomic.Int32) (*InjectorOpts, func(*RootScope)) {
				return &InjectorOpts{HookContainerClosing: []func(context.Context, *RootScope){
					nil, func(context.Context, *RootScope) { ran.Add(1) },
				}}, func(root *RootScope) {
					root.AddContainerClosingHook(nil)
				}
			},
			operation: shutdown,
		},
		{
			name: "HookContainerClosed",
			build: func(ran *atomic.Int32) (*InjectorOpts, func(*RootScope)) {
				return &InjectorOpts{HookContainerClosed: []func(context.Context, *RootScope, *ShutdownReport){
					nil, func(context.Context, *RootScope, *ShutdownReport) { ran.Add(1) },
				}}, func(root *RootScope) {
					root.AddContainerClosedHook(nil)
				}
			},
			operation: shutdown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			var ran atomic.Int32
			opts, setup := tt.build(&ran)
			root := NewWithOpts(opts)
			setup(root)

			assert.NotPanics(t, func() { tt.operation(root) })
			assert.Equal(t, int32(1), ran.Load())
		})
	}
}

func TestContainerHooks_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		clone func(*RootScope) *RootScope
		count int32
	}{
		{name: "Clone()", clone: (*RootScope).Clone, count: 2},
		{name: "CloneWithOpts(&InjectorOpts{})", clone: func(root *RootScope) *RootScope { return root.CloneWithOpts(&InjectorOpts{}) }, count: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			var started atomic.Int32
			root.AddContainerStartedHook(func(context.Context, *RootScope) { started.Add(1) })
			require.NoError(t, root.Start())

			clone := tt.clone(root)

			require.NoError(t, clone.Start())
			assert.Equal(t, tt.count, started.Load())
		})
	}
}

func TestContainerHooks_ObserverPanicDefault(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("boom")

	tests := []struct {
		name string
		// register adds the panicking hook, the following recording hook and
		// the row's other hooks; it returns the events expected to be
		// recorded before the panic reached the caller.
		register   func(root *RootScope, rec *callRecorder) []string
		operation  func(root *RootScope)
		phase      ContainerPhase // reached although the hook panicked
		laterError error
	}{
		{
			name: "Started",
			register: func(root *RootScope, rec *callRecorder) []string {
				root.AddContainerStartedHook(func(context.Context, *RootScope) { panic("kaboom") })
				root.AddContainerStartedHook(func(context.Context, *RootScope) { rec.record("next-started") })
				root.AddContainerReadyHook(func(context.Context, *RootScope) { rec.record("ready") })
				return []string{"next-started", "ready"}
			},
			operation:  func(root *RootScope) { _ = root.Start() },
			phase:      PhaseReady,
			laterError: ErrAlreadyStarted,
		},
		{
			name: "Ready",
			register: func(root *RootScope, rec *callRecorder) []string {
				root.AddContainerReadyHook(func(context.Context, *RootScope) { panic("kaboom") })
				root.AddContainerReadyHook(func(context.Context, *RootScope) { rec.record("next-ready") })
				return []string{"next-ready"}
			},
			operation:  func(root *RootScope) { _ = root.Start() },
			phase:      PhaseReady,
			laterError: ErrAlreadyStarted,
		},
		{
			name: "Failed",
			register: func(root *RootScope, rec *callRecorder) []string {
				ProvideNamedValue(root, "server", &errorStarter{err: errBoom})
				root.AddContainerFailedHook(func(context.Context, *RootScope, error) { panic("kaboom") })
				root.AddContainerFailedHook(func(context.Context, *RootScope, error) { rec.record("next-failed") })
				return []string{"next-failed"}
			},
			operation:  func(root *RootScope) { _ = root.Start() },
			phase:      PhaseFailed,
			laterError: ErrAlreadyStarted,
		},
		{
			name: "Closing",
			register: func(root *RootScope, rec *callRecorder) []string {
				root.AddContainerClosingHook(func(context.Context, *RootScope) { panic("kaboom") })
				root.AddContainerClosingHook(func(context.Context, *RootScope) { rec.record("next-closing") })
				root.AddContainerClosedHook(func(context.Context, *RootScope, *ShutdownReport) { rec.record("closed") })
				return []string{"next-closing", "db.Shutdown", "closed"}
			},
			operation:  func(root *RootScope) { root.Shutdown() },
			phase:      PhaseClosed,
			laterError: ErrStartAfterShutdown,
		},
		{
			name: "Closed",
			register: func(root *RootScope, rec *callRecorder) []string {
				root.AddContainerClosedHook(func(context.Context, *RootScope, *ShutdownReport) { panic("kaboom") })
				root.AddContainerClosedHook(func(context.Context, *RootScope, *ShutdownReport) { rec.record("next-closed") })
				return []string{"db.Shutdown", "next-closed"}
			},
			operation:  func(root *RootScope) { root.Shutdown() },
			phase:      PhaseClosed,
			laterError: ErrStartAfterShutdown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			rec := &callRecorder{}
			provideBuiltShutdowner(t, root, rec, "db", nil)
			completed := tt.register(root, rec)

			var recovered any
			func() {
				defer func() { recovered = recover() }()
				tt.operation(root)
			}()

			is.Equal("kaboom", recovered)
			is.Equal(completed, rec.list())
			is.Equal(tt.phase, root.Phase())
			is.ErrorIs(root.Start(), tt.laterError)
		})
	}
}

func TestContainerHooks_ObserverPanicOverride(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		register  func(root *RootScope)
		operation func(t *testing.T, root *RootScope)
		point     string
	}{
		{
			name: "Started",
			register: func(root *RootScope) {
				root.AddContainerStartedHook(func(context.Context, *RootScope) { panic("kaboom") })
				root.AddContainerStartedHook(func(context.Context, *RootScope) { panic("boom") })
			},
			operation: func(t *testing.T, root *RootScope) { t.Helper(); assert.NoError(t, root.Start()) },
			point:     "started",
		},
		{
			name: "Closing",
			register: func(root *RootScope) {
				root.AddContainerClosingHook(func(context.Context, *RootScope) { panic("kaboom") })
				root.AddContainerClosingHook(func(context.Context, *RootScope) { panic("boom") })
			},
			operation: func(t *testing.T, root *RootScope) { t.Helper(); assert.True(t, root.Shutdown().Succeed) },
			point:     "closing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			logs := &callRecorder{}
			type call struct {
				point string
				value any
			}
			var calls []call
			root := NewWithOpts(&InjectorOpts{
				Logf: func(format string, args ...any) { logs.record(fmt.Sprintf(format, args...)) },
				HookContainerPanic: func(_ *RootScope, hook string, recovered any) {
					calls = append(calls, call{point: hook, value: recovered})
				},
			})
			tt.register(root)

			is.NotPanics(func() { tt.operation(t, root) })

			is.Equal([]call{{point: tt.point, value: "kaboom"}}, calls)
			is.True(slices.ContainsFunc(logs.list(), func(line string) bool { return strings.Contains(line, "boom") && !strings.Contains(line, "kaboom") }))
		})
	}
}

func TestContainerHooks_ClonePanicPolicy(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	var points []string
	root := NewWithOpts(&InjectorOpts{
		HookContainerPanic: func(_ *RootScope, hook string, _ any) { points = append(points, hook) },
	})
	root.AddContainerStartedHook(func(context.Context, *RootScope) { panic("kaboom") })

	clone := root.Clone()

	assert.NotPanics(t, func() { _ = clone.Start() })
	assert.Equal(t, []string{"started"}, points)
}

func TestContainerStartingHook_Panic(t *testing.T) {
	t.Parallel()

	errKaboom := errors.New("kaboom error")

	tests := []struct {
		name  string
		value any
		match func(t *testing.T, err error)
	}{
		{
			name:  "string kaboom",
			value: "kaboom",
			match: func(t *testing.T, err error) { t.Helper(); assert.ErrorContains(t, err, "kaboom") },
		},
		{
			name:  "error ErrKaboom",
			value: errKaboom,
			match: func(t *testing.T, err error) { t.Helper(); assert.ErrorIs(t, err, errKaboom) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			var panicCalls, nextRan, failed atomic.Int32
			root := NewWithOpts(&InjectorOpts{
				HookContainerPanic: func(*RootScope, string, any) { panicCalls.Add(1) },
			})
			builds := provideBuildCountedServer(root)
			root.AddContainerStartingHook(func(context.Context, *RootScope) error { panic(tt.value) })
			root.AddContainerStartingHook(func(context.Context, *RootScope) error { nextRan.Add(1); return nil })
			root.AddContainerFailedHook(func(context.Context, *RootScope, error) { failed.Add(1) })

			var err error
			is.NotPanics(func() { err = root.Start() })

			tt.match(t, err)
			is.Equal(int32(0), nextRan.Load())
			is.Equal(int32(0), builds.Load())
			is.Equal(int32(1), failed.Load())
			is.Equal(int32(0), panicCalls.Load())
		})
	}
}

func TestContainerHooks_ConcurrentStart(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)
	is := assert.New(t)

	root := New()
	ProvideNamedValue(root, "server", &startWithErr{})
	var starting, started, ready atomic.Int32
	root.AddContainerStartingHook(func(context.Context, *RootScope) error { starting.Add(1); return nil })
	root.AddContainerStartedHook(func(context.Context, *RootScope) { started.Add(1) })
	root.AddContainerReadyHook(func(context.Context, *RootScope) { ready.Add(1) })

	const callers = 50
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() { errs <- root.Start() })
	}
	wg.Wait()
	close(errs)

	succeeded, already := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrAlreadyStarted):
			already++
		}
	}
	is.Equal(1, succeeded)
	is.Equal(callers-1, already)
	is.Equal(int32(1), starting.Load())
	is.Equal(int32(1), started.Load())
	is.Equal(int32(1), ready.Load())
}

func TestContainerHooks_ConcurrentShutdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		callers int
		times   int
	}{
		{name: "1 caller, 2 times", callers: 1, times: 2},
		{name: "20 callers, 1 time", callers: 20, times: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)
			is := assert.New(t)

			root := New()
			require.NoError(t, root.Start())
			var closing, closed atomic.Int32
			root.AddContainerClosingHook(func(context.Context, *RootScope) { closing.Add(1) })
			root.AddContainerClosedHook(func(context.Context, *RootScope, *ShutdownReport) { closed.Add(1) })

			reports := make(chan *ShutdownReport, tt.callers*tt.times)
			var wg sync.WaitGroup
			for range tt.callers {
				wg.Go(func() {
					for range tt.times {
						reports <- root.Shutdown()
					}
				})
			}
			wg.Wait()
			close(reports)

			is.Equal(int32(1), closing.Load())
			is.Equal(int32(1), closed.Load())
			for report := range reports {
				is.True(report.Succeed)
			}
		})
	}
}

func TestContainerHooks_ReentrantStart(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	var started atomic.Int32
	var inner error
	root.AddContainerStartedHook(func(_ context.Context, r *RootScope) {
		started.Add(1)
		inner = r.Start()
	})

	is.NoError(root.Start())
	is.ErrorIs(inner, ErrAlreadyStarted)
	is.Equal(int32(1), started.Load())
}

func TestContainerHooks_ChildScopeShutdown(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	require.NoError(t, root.Start())
	var closing, closed atomic.Int32
	root.AddContainerClosingHook(func(context.Context, *RootScope) { closing.Add(1) })
	root.AddContainerClosedHook(func(context.Context, *RootScope, *ShutdownReport) { closed.Add(1) })
	request := root.Scope("request")
	rec := &callRecorder{}
	ProvideNamed(request, "session", func(Injector) (*recordingShutdowner, error) {
		return &recordingShutdowner{name: "session", rec: rec}, nil
	})
	_, err := InvokeNamed[*recordingShutdowner](request, "session")
	require.NoError(t, err)

	request.Shutdown()

	is.Equal([]string{"session.Shutdown"}, rec.list())
	is.Equal(int32(0), closing.Load())
	is.Equal(int32(0), closed.Load())
}

func TestContainerHooks_ChildScopeShutdownThroughInjector(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	require.NoError(t, root.Start())
	var closing, closed atomic.Int32
	root.AddContainerClosingHook(func(context.Context, *RootScope) { closing.Add(1) })
	root.AddContainerClosedHook(func(context.Context, *RootScope, *ShutdownReport) { closed.Add(1) })
	request := root.Scope("request")
	var injector Injector
	ProvideNamed(request, "session", func(i Injector) (*recordingShutdowner, error) {
		injector = i
		return &recordingShutdowner{name: "session", rec: &callRecorder{}}, nil
	})
	_, err := InvokeNamed[*recordingShutdowner](request, "session")
	require.NoError(t, err)

	report := injector.Shutdown()

	is.True(report.Succeed, report.Error())
	is.Equal(int32(0), closing.Load())
	is.Equal(int32(0), closed.Load())
}

func TestContainerHooks_NoHooksUnchanged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctor func() *RootScope
	}{
		{name: "New()", ctor: func() *RootScope { return New() }},
		{name: "NewWithOpts(&InjectorOpts{})", ctor: func() *RootScope { return NewWithOpts(&InjectorOpts{}) }},
		{name: "NewWithOpts(nil)", ctor: func() *RootScope { return NewWithOpts(nil) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := tt.ctor()
			ProvideNamedValue(root, "server", &startWithErr{})
			provideBuiltShutdowner(t, root, &callRecorder{}, "db", nil)

			is.NotPanics(func() {
				is.NoError(root.Start())
				is.True(root.Shutdown().Succeed)
			})
		})
	}
}

func TestContainerHooks_IncrementalStartFiresNone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		inTenant  bool
		operation func(root *RootScope, tenant *Scope) error
	}{
		{name: "root.Refresh()", operation: func(root *RootScope, _ *Scope) error { return root.Refresh() }},
		{name: "tenant.Start()", inTenant: true, operation: func(_ *RootScope, tenant *Scope) error { return tenant.Start() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			var starting, started, ready, failed atomic.Int32
			root.AddContainerStartingHook(func(context.Context, *RootScope) error { starting.Add(1); return nil })
			root.AddContainerStartedHook(func(context.Context, *RootScope) { started.Add(1) })
			root.AddContainerReadyHook(func(context.Context, *RootScope) { ready.Add(1) })
			root.AddContainerFailedHook(func(context.Context, *RootScope, error) { failed.Add(1) })
			tenant := root.Scope("tenant")
			require.NoError(t, root.Start())

			starts := &countingStarters{}
			target := Injector(root)
			if tt.inTenant {
				target = tenant
			}
			provideCountedStarter(target, starts, "late")

			is.NoError(tt.operation(root, tenant))
			is.Equal(1, starts.count("late"))
			is.Equal([]int32{1, 1, 1, 0}, []int32{starting.Load(), started.Load(), ready.Load(), failed.Load()})
		})
	}
}

// TestContainerHooks_ShutdownCauseRace pins that a start whose pipeline
// returns just as Shutdown begins still fails with ErrStartAfterShutdown.
func TestContainerHooks_ShutdownCauseRace(t *testing.T) {
	t.Parallel()

	const runs = 20000

	tests := []struct {
		name      string
		operation func(t *testing.T, root *RootScope, provideJob func()) error
	}{
		{
			name: "Start()",
			operation: func(_ *testing.T, root *RootScope, provideJob func()) error {
				provideJob()
				return root.Start()
			},
		},
		{
			name: "Refresh()",
			operation: func(t *testing.T, root *RootScope, provideJob func()) error {
				t.Helper()
				require.NoError(t, root.Start())
				provideJob()
				return root.Refresh()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Minute)

			var bad int
			var sample error
			for range runs {
				root := New()
				scheduled := NewKind[Job, Spec]("k")
				begin, done := make(chan struct{}), make(chan struct{})
				go func() {
					<-begin
					root.Shutdown()
					close(done)
				}()
				scheduled.Handle(root, StageAfterStart, func(context.Context, Component[Job, Spec]) error {
					close(begin)
					return nil
				})
				provideJob := func() {
					ProvideNamed(root, "j", func(Injector) (*CleanupJob, error) { return &CleanupJob{name: "j"}, nil }, scheduled.With(Spec{}))
				}

				err := tt.operation(t, root, provideJob)
				<-done

				if err != nil && !errors.Is(err, ErrStartAfterShutdown) {
					bad++
					sample = err
				}
			}

			assert.Zero(t, bad, "errors not matching ErrStartAfterShutdown, e.g. %v", sample)
		})
	}
}

func TestContainerHooks_ShutdownInterruptsLateStep(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(root *RootScope, shutdowns *sync.WaitGroup)
		step  func(t *testing.T, err error) // the row fails at its own step
	}{
		{
			name: "Starting hook waiting for ctx",
			step: func(t *testing.T, err error) { t.Helper(); assert.ErrorIs(t, err, context.Canceled) },
			setup: func(root *RootScope, shutdowns *sync.WaitGroup) {
				root.AddContainerStartingHook(func(ctx context.Context, r *RootScope) error {
					shutdowns.Go(func() { r.Shutdown() })
					<-ctx.Done()
					return ctx.Err()
				})
			},
		},
		{
			name: "deferred kind binding",
			step: func(t *testing.T, err error) { t.Helper(); assert.ErrorContains(t, err, "does not implement") },
			setup: func(root *RootScope, shutdowns *sync.WaitGroup) {
				k := NewKind[Job, Spec]("k")
				ProvideNamed[any](root, "x", func(Injector) (any, error) {
					shutdowns.Go(func() { root.Shutdown() })
					synctest.Wait()        // Shutdown is blocked, waiting for this Start
					return struct{}{}, nil // not a Job
				}, k.With(Spec{}))
			},
		},
		{
			name: "cycle check",
			step: func(t *testing.T, err error) { t.Helper(); assert.ErrorIs(t, err, ErrCircularDependency) },
			setup: func(root *RootScope, shutdowns *sync.WaitGroup) {
				ProvideNamed(root, "scheduler", func(Injector) (*startWithErr, error) { return &startWithErr{}, nil })
				ProvideNamed(root, "job", func(i Injector) (*startWithErr, error) {
					_, err := InvokeNamed[*startWithErr](i, "scheduler")
					return &startWithErr{}, err
				})
				if err := DeclareDependency(root, "scheduler", "job"); err != nil {
					panic(err)
				}
				k := NewKind[Job, Spec]("k")
				ProvideNamed(root, "j", func(Injector) (*CleanupJob, error) { return &CleanupJob{name: "j"}, nil }, k.With(Spec{}))
				k.Handle(root, StageBeforeStart, func(context.Context, Component[Job, Spec]) error {
					shutdowns.Go(func() { root.Shutdown() })
					synctest.Wait()
					return nil
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				is := assert.New(t)

				root := New()
				var failedErr error
				root.AddContainerFailedHook(func(_ context.Context, _ *RootScope, err error) { failedErr = err })
				var shutdowns sync.WaitGroup
				tt.setup(root, &shutdowns)

				err := root.Start()
				shutdowns.Wait()

				is.ErrorIs(err, ErrStartAfterShutdown)
				is.Same(err, failedErr)
				tt.step(t, err)
			})
		})
	}
}

func TestContainerFailedHook_PipelinePanicAfterShutdown(t *testing.T) {
	t.Parallel()

	for _, n := range []uint{1, 4} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				is := assert.New(t)

				errBoom := errors.New("boom")
				root := NewWithOpts(&InjectorOpts{
					StartParallelism: n,
					HookAfterStart: []func(*Scope, string, error){func(_ *Scope, name string, _ error) {
						if name == "a" {
							panic(errBoom)
						}
					}},
				})
				var failedErr error
				root.AddContainerFailedHook(func(_ context.Context, _ *RootScope, err error) { failedErr = err })
				entered := make(chan struct{})
				provideLazy(root, "a", &ctxStarter{fn: func(ctx context.Context) error {
					close(entered)
					<-ctx.Done()
					return nil
				}})
				if n > 1 {
					provideLazy(root, "b", &ctxStarter{fn: func(context.Context) error { return nil }})
				}
				shutdownDone := make(chan struct{})
				go func() {
					defer close(shutdownDone)
					<-entered
					root.Shutdown()
				}()

				is.PanicsWithValue(errBoom, func() { _ = root.Start() })
				<-shutdownDone

				is.ErrorIs(failedErr, errBoom)
				is.ErrorIs(failedErr, ErrStartAfterShutdown, "Failed error: %v", failedErr)
			})
		})
	}
}
