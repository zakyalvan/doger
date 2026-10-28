package dogergocron

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"github.com/jonboulle/clockwork"
	"github.com/kartaladev/doger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// counter is a health check that counts its calls and how many run at once,
// blocking until released when block is set.
type counter struct {
	calls, running, maxRunning atomic.Int32
	block                      chan struct{} // nil: never blocks
	blockFirst                 bool          // blocks only its first call
}

func (c *counter) HealthCheck() error {
	n := c.calls.Add(1)
	if r := c.running.Add(1); r > c.maxRunning.Load() {
		c.maxRunning.Store(r)
	}
	defer c.running.Add(-1)
	if c.block != nil && (!c.blockFirst || n == 1) {
		<-c.block
	}
	return nil
}

// finishedRuns counts the health jobs gocron finished running.
type finishedRuns struct{ n atomic.Int32 }

// option returns the job option recording finished runs.
func (r *finishedRuns) option() Option {
	return WithJobOptions(gocron.WithEventListeners(gocron.AfterJobRuns(func(uuid.UUID, string) { r.n.Add(1) })))
}

// wait waits until gocron finished n runs and released the job's singleton
// slot, which it does right after the listener returns.
func (r *finishedRuns) wait(t *testing.T, n int32) {
	t.Helper()
	require.Eventually(t, func() bool { return r.n.Load() >= n }, 2*time.Second, time.Millisecond, "gocron did not finish %d runs", n)
	time.Sleep(10 * time.Millisecond)
}

// waitForTimer waits until gocron armed its next run on fc.
func waitForTimer(t *testing.T, fc *clockwork.FakeClock) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, fc.BlockUntilContext(ctx, 1), "gocron armed no timer")
}

// startWith starts a container with scheduler on fc and a counter "db".
func startWith(t *testing.T, fc *clockwork.FakeClock, scheduler doger.HealthScheduler, db *counter, opts *doger.InjectorOpts) *doger.RootScope {
	t.Helper()
	if opts == nil {
		opts = &doger.InjectorOpts{}
	}
	opts.Clock, opts.HealthScheduler = fc, scheduler
	i := doger.NewWithOpts(opts)
	doger.ProvideNamedValue(i, "db", db)
	require.NoError(t, i.Start())
	return i
}

func TestNew_InvalidOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opt     Option
		message string // "" for a valid option
	}{
		{name: "interval_0", opt: WithInterval(0), message: "interval must be positive"},
		{name: "interval_negative", opt: WithInterval(-time.Second), message: "interval must be positive"},
		{name: "nil_job_definition", opt: WithJobDefinition(nil), message: "job definition is nil"},
		{name: "nil_scheduler", opt: WithScheduler(nil), message: "scheduler is nil"},
		{name: "nil_start_at", opt: WithStartAt(nil), message: "start-at option is nil"},
		{name: "interval_1ns", opt: WithInterval(time.Nanosecond)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			scheduler, err := New(tt.opt)
			if tt.message == "" {
				assert.NoError(t, err)
				assert.NotNil(t, scheduler)
				return
			}
			assert.ErrorIs(t, err, ErrInvalidOption)
			assert.ErrorContains(t, err, tt.message)
			assert.Nil(t, scheduler)
		})
	}
}

func TestScheduler_DefaultCadence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		steps []time.Duration // advances, each waiting for gocron's timer
		calls int32
	}{
		{name: "0s", calls: 1},
		{name: "29999ms", steps: []time.Duration{DefaultInterval - time.Millisecond}, calls: 1},
		{name: "30s", steps: []time.Duration{DefaultInterval}, calls: 2},
		{name: "60s", steps: []time.Duration{DefaultInterval, DefaultInterval}, calls: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fc := clockwork.NewFakeClock()
			runs := &finishedRuns{}
			db := &counter{}
			i := startWith(t, fc, must(New(runs.option())), db, nil)
			defer i.Shutdown()

			require.Eventually(t, func() bool { return db.calls.Load() == 1 }, 2*time.Second, time.Millisecond, "the first round did not run at once")
			for n, step := range tt.steps {
				runs.wait(t, int32(n+1))
				waitForTimer(t, fc)
				fc.Advance(step)
				if want := int32(n + 2); want <= tt.calls {
					require.Eventually(t, func() bool { return db.calls.Load() == want }, 2*time.Second, time.Millisecond)
				}
			}
			time.Sleep(20 * time.Millisecond)
			assert.Equal(t, tt.calls, db.calls.Load())
		})
	}
}

func TestScheduler_SingletonSkipsTick(t *testing.T) {
	t.Parallel()

	fc := clockwork.NewFakeClock()
	runs := &finishedRuns{}
	db := &counter{block: make(chan struct{}), blockFirst: true}
	i := startWith(t, fc, must(New(runs.option())), db, &doger.InjectorOpts{HealthCheckTimeout: -time.Nanosecond})
	defer i.Shutdown()

	require.Eventually(t, func() bool { return db.calls.Load() == 1 }, 2*time.Second, time.Millisecond)
	waitForTimer(t, fc)
	fc.Advance(DefaultInterval) // the tick finds the first round running
	waitForTimer(t, fc)         // the next run is armed
	time.Sleep(20 * time.Millisecond)
	close(db.block)
	runs.wait(t, 1)

	fc.Advance(DefaultInterval)
	require.Eventually(t, func() bool { return db.calls.Load() >= 2 }, 2*time.Second, time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, int32(2), db.calls.Load())
	assert.Equal(t, int32(1), db.maxRunning.Load(), "checks running at once")
}

func TestScheduler_Overrides(t *testing.T) {
	t.Parallel()

	fc := clockwork.NewFakeClock()
	tests := []struct {
		name  string
		opts  func() []Option
		steps []time.Duration
		calls int32
	}{
		{name: "interval_10s", opts: func() []Option { return []Option{WithInterval(10 * time.Second)} }, steps: []time.Duration{10 * time.Second, 10 * time.Second}, calls: 3},
		{name: "duration_job_5s", opts: func() []Option { return []Option{WithJobDefinition(gocron.DurationJob(5 * time.Second))} }, steps: []time.Duration{5 * time.Second}, calls: 2},
		{name: "definition_wins_over_interval", opts: func() []Option {
			return []Option{WithJobDefinition(gocron.DurationJob(5 * time.Second)), WithInterval(10 * time.Second)}
		}, steps: []time.Duration{5 * time.Second, 5 * time.Second}, calls: 3},
		{name: "start_at_9999ms", opts: func() []Option {
			return []Option{WithStartAt(gocron.WithStartDateTime(fc.Now().Add(10 * time.Second)))}
		}, steps: []time.Duration{10*time.Second - time.Millisecond}, calls: 0},
		{name: "start_at_10s", opts: func() []Option {
			return []Option{WithStartAt(gocron.WithStartDateTime(fc.Now().Add(10 * time.Second)))}
		}, steps: []time.Duration{10 * time.Second}, calls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Not parallel: rows share fc, whose now the start-at rows read.
			clock := clockwork.NewFakeClockAt(fc.Now())
			runs := &finishedRuns{}
			db := &counter{}
			i := startWith(t, clock, must(New(append(tt.opts(), runs.option())...)), db, nil)
			defer i.Shutdown()

			want := tt.calls - int32(len(tt.steps)) // rounds run at once
			if want > 0 {
				require.Eventually(t, func() bool { return db.calls.Load() == want }, 2*time.Second, time.Millisecond, "the first round did not run at once")
			}
			for range tt.steps {
				if want > 0 {
					runs.wait(t, want)
				}
				waitForTimer(t, clock)
				clock.Advance(tt.steps[0])
				if want++; want > 0 {
					require.Eventually(t, func() bool { return db.calls.Load() >= want }, 2*time.Second, time.Millisecond)
				}
			}
			time.Sleep(20 * time.Millisecond)
			assert.Equal(t, tt.calls, db.calls.Load())
		})
	}
}

// recordingLogger is a gocron.Logger counting its calls.
type recordingLogger struct{ calls atomic.Int32 }

func (l *recordingLogger) Debug(string, ...any) { l.calls.Add(1) }
func (l *recordingLogger) Error(string, ...any) { l.calls.Add(1) }
func (l *recordingLogger) Info(string, ...any)  { l.calls.Add(1) }
func (l *recordingLogger) Warn(string, ...any)  { l.calls.Add(1) }

func TestScheduler_SchedulerOptions(t *testing.T) {
	t.Parallel()

	logger := &recordingLogger{}
	i := startWith(t, clockwork.NewFakeClock(), must(New(WithSchedulerOptions(gocron.WithLogger(logger)))), &counter{}, nil)
	i.Shutdown()

	assert.Positive(t, logger.calls.Load())
}

// Not parallel: goleak checks the goroutines gocron leaves.
func TestScheduler_RejectedJob(t *testing.T) { //nolint:paralleltest
	ignore := goleak.IgnoreCurrent()

	scheduler := must(New(WithJobDefinition(gocron.CronJob("not a cron", false))))
	stop, err := scheduler.ScheduleHealthRounds(clockwork.NewFakeClock(), func(context.Context) {})

	assert.Error(t, err)
	assert.Nil(t, stop)
	assert.NoError(t, goleak.Find(ignore))
}

func TestScheduler_Shared(t *testing.T) {
	t.Parallel()
	is := assert.New(t)

	shared, err := gocron.NewScheduler()
	require.NoError(t, err)
	defer func() { _ = shared.Shutdown() }()
	_, err = shared.NewJob(gocron.DurationJob(time.Hour), gocron.NewTask(func() {}), gocron.WithName("report"))
	require.NoError(t, err)
	shared.Start()

	i := doger.NewWithOpts(&doger.InjectorOpts{HealthScheduler: must(New(WithScheduler(shared)))})
	doger.ProvideNamedValue(i, "db", &counter{})
	require.NoError(t, i.Start())
	is.Len(shared.Jobs(), 2, "jobs while the container is ready")

	i.Shutdown()

	jobs := shared.Jobs()
	if is.Len(jobs, 1) {
		is.Equal("report", jobs[0].Name())
	}
	ran := make(chan struct{})
	var once sync.Once
	_, err = shared.NewJob(gocron.OneTimeJob(gocron.OneTimeJobStartImmediately()), gocron.NewTask(func() { once.Do(func() { close(ran) }) }))
	require.NoError(t, err)
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		is.Fail("the shared scheduler no longer runs jobs")
	}
}

func TestScheduler_SharedStop(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// run checks how the stop of a round scheduled on shared returns
		run func(t *testing.T, shared gocron.Scheduler, scheduler *Scheduler)
	}{
		{name: "round_running", run: func(t *testing.T, _ gocron.Scheduler, scheduler *Scheduler) {
			started, release := make(chan struct{}), make(chan struct{})
			var returned atomic.Bool
			var once sync.Once
			stop, err := scheduler.ScheduleHealthRounds(clockwork.NewRealClock(), func(context.Context) {
				once.Do(func() { close(started) })
				<-release
				returned.Store(true)
			})
			require.NoError(t, err)
			defer close(release)
			<-started

			stopped := make(chan error, 1)
			go func() { stopped <- stop(context.Background()) }()
			select {
			case <-stopped:
				assert.Fail(t, "stop returned while the round still ran")
				return
			case <-time.After(50 * time.Millisecond):
			}
			release <- struct{}{} // lets the round return

			assert.NoError(t, <-stopped)
			assert.True(t, returned.Load(), "the round returned before stop")
		}},
		{name: "owner_shut_down", run: func(t *testing.T, shared gocron.Scheduler, scheduler *Scheduler) {
			stop, err := scheduler.ScheduleHealthRounds(clockwork.NewRealClock(), func(context.Context) {})
			require.NoError(t, err)
			require.NoError(t, shared.Shutdown())

			assert.NoError(t, stop(context.Background()))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			shared, err := gocron.NewScheduler()
			require.NoError(t, err)
			defer func() { _ = shared.Shutdown() }()
			shared.Start()

			tt.run(t, shared, must(New(WithScheduler(shared))))
		})
	}
}

// Not parallel: goleak checks the goroutines gocron leaves.
func TestScheduler_CleanStop(t *testing.T) { //nolint:paralleltest
	tests := []struct {
		name     string
		shutdown func(i *doger.RootScope)
	}{
		{name: "Shutdown", shutdown: func(i *doger.RootScope) { i.Shutdown() }},
		{name: "ShutdownWithContext_cancelled", shutdown: func(i *doger.RootScope) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			i.ShutdownWithContext(ctx)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ignore := goleak.IgnoreCurrent()

			i := doger.NewWithOpts(&doger.InjectorOpts{HealthScheduler: must(New())})
			doger.ProvideNamedValue(i, "db", &counter{})
			require.NoError(t, i.Start())
			tt.shutdown(i)

			assert.NoError(t, goleak.Find(ignore))
		})
	}
}

// must returns v, panicking on err.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
