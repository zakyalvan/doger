package doger

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
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
	root.AddContainerClosingHook(func(context.Context, *RootScope) { closing.Add(1) })
	root.AddContainerClosedHook(func(context.Context, *RootScope, *ShutdownReport) { closed.Add(1) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = root.ShutdownOnSignalsWithContext(ctx)

	is.Equal(int32(1), closing.Load())
	is.Equal(int32(1), closed.Load())
}
