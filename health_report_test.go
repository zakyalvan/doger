package doger

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthState_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value HealthState
		want  string
	}{
		{name: "unknown", value: HealthUnknown, want: "unknown"},
		{name: "healthy", value: HealthHealthy, want: "healthy"},
		{name: "degraded", value: HealthDegraded, want: "degraded"},
		{name: "unhealthy", value: HealthUnhealthy, want: "unhealthy"},
		{name: "out_of_range", value: HealthState(9), want: "HealthState(9)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.value.String())
		})
	}
}

func TestHealthReport_OverallState(t *testing.T) {
	t.Parallel()

	critical := func(state HealthState) ServiceHealth { return ServiceHealth{State: state} }
	optional := func(state HealthState) ServiceHealth { return ServiceHealth{State: state, Optional: true} }

	tests := []struct {
		name     string
		services []ServiceHealth
		want     HealthState
	}{
		{name: "healthy_healthy", services: []ServiceHealth{critical(HealthHealthy), critical(HealthHealthy)}, want: HealthHealthy},
		{name: "healthy_unhealthy", services: []ServiceHealth{critical(HealthHealthy), critical(HealthUnhealthy)}, want: HealthUnhealthy},
		{name: "unknown_unhealthy", services: []ServiceHealth{critical(HealthUnknown), critical(HealthUnhealthy)}, want: HealthUnhealthy},
		{name: "unknown_critical_healthy", services: []ServiceHealth{critical(HealthUnknown), critical(HealthHealthy)}, want: HealthUnknown},
		{name: "unknown_optional_healthy", services: []ServiceHealth{optional(HealthUnknown), critical(HealthHealthy)}, want: HealthHealthy},
		{name: "unknown_unknown", services: []ServiceHealth{critical(HealthUnknown), critical(HealthUnknown)}, want: HealthUnknown},
		{name: "healthy_degraded", services: []ServiceHealth{critical(HealthHealthy), optional(HealthDegraded)}, want: HealthDegraded},
		{name: "unknown_critical_degraded", services: []ServiceHealth{critical(HealthUnknown), optional(HealthDegraded)}, want: HealthDegraded},
		{name: "degraded_unhealthy", services: []ServiceHealth{optional(HealthDegraded), critical(HealthUnhealthy)}, want: HealthUnhealthy},
		{name: "none", services: nil, want: HealthHealthy},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, overallHealthState(tt.services))
		})
	}
}

var healthReportTestEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// healthReportTestOnClock is a Healthchecker whose check advances a fake
// clock by takes, then returns err.
type healthReportTestOnClock struct {
	clock *clockwork.FakeClock
	takes time.Duration
	err   error
}

func (c *healthReportTestOnClock) HealthCheck() error {
	c.clock.Advance(c.takes)
	return c.err
}

// healthReportTestSwitch is a Healthchecker counting its calls and returning
// the error last stored.
type healthReportTestSwitch struct {
	calls atomic.Int32
	err   atomic.Pointer[error]
}

func (s *healthReportTestSwitch) set(err error) { s.err.Store(&err) }

func (s *healthReportTestSwitch) HealthCheck() error {
	s.calls.Add(1)
	if err := s.err.Load(); err != nil {
		return *err
	}
	return nil
}

// healthReportTestListing returns the entries of r as "<scope name>/<name>".
func healthReportTestListing(r HealthReport) []string {
	listing := make([]string, 0, len(r.Services))
	for _, entry := range r.Services {
		listing = append(listing, entry.Service.ScopeName+"/"+entry.Service.Service)
	}
	return listing
}

// healthReportTestEntry returns the entry of r for service name.
func healthReportTestEntry(t *testing.T, r HealthReport, name string) ServiceHealth {
	t.Helper()
	for _, entry := range r.Services {
		if entry.Service.Service == name {
			return entry
		}
	}
	require.Failf(t, "no entry", "the report has no entry for %q: %v", name, healthReportTestListing(r))
	return ServiceHealth{}
}

func TestHealthReport_Entries(t *testing.T) {
	t.Parallel()

	errDown := errors.New("down")

	tests := []struct {
		name    string
		service string
		child   bool
		takes   time.Duration
		result  error
		state   HealthState
	}{
		{name: "root_healthy", service: "db", takes: 0, state: HealthHealthy},
		{name: "root_unhealthy_2s", service: "db", takes: 2 * time.Second, result: errDown, state: HealthUnhealthy},
		{name: "child_healthy_1s", service: "cache", child: true, takes: time.Second, state: HealthHealthy},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)
			is := assert.New(t)

			fc := clockwork.NewFakeClockAt(healthReportTestEpoch)
			i := NewWithOpts(&InjectorOpts{Clock: fc})
			var scope Injector = i
			if tt.child {
				scope = i.Scope("child")
			}
			ProvideNamedValue(scope, tt.service, &healthReportTestOnClock{clock: fc, takes: tt.takes, err: tt.result})

			report := i.HealthReport()

			entry := healthReportTestEntry(t, report, tt.service)
			is.Equal(tt.state, entry.State)
			if tt.result == nil {
				is.NoError(entry.Err)
			} else {
				is.Same(tt.result, entry.Err)
			}
			is.Equal(healthReportTestEpoch.Add(tt.takes), entry.CheckedAt)
			is.Equal(tt.takes, entry.Duration)
			is.Zero(entry.PendingSince)
			is.Equal(healthReportTestEpoch.Add(tt.takes), report.CheckedAt)
			is.Equal(PhaseIdle, report.Phase)
		})
	}
}

func TestHealthReport_Isolated(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	i := New()
	ProvideNamedValue(i, "db", &healthReportTestSwitch{})

	first := i.HealthReport()
	require.Len(t, first.Services, 1)
	first.Services[0].State = HealthUnhealthy

	assert.Equal(t, HealthHealthy, healthReportTestEntry(t, i.HealthReport(), "db").State)
}

func TestHealthReport_Order(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	i := New()
	ProvideNamedValue(i, "b", &healthReportTestSwitch{})
	ProvideNamedValue(i, "a", &healthReportTestSwitch{})
	x, w := i.Scope("x"), i.Scope("w")
	ProvideNamedValue(x, "z", &healthReportTestSwitch{})
	ProvideNamedValue(w, "c", &healthReportTestSwitch{})
	ProvideNamedValue(x.Scope("leaf"), "q", &healthReportTestSwitch{})
	ProvideNamedValue(w.Scope("leaf"), "p", &healthReportTestSwitch{})

	report := i.HealthReport()

	got := make([]string, 0, len(report.Services))
	for _, entry := range report.Services {
		got = append(got, entry.Service.Service)
	}
	assert.Equal(t, []string{"a", "b", "c", "z", "p", "q"}, got)
	scopes := map[string]string{}
	for _, entry := range report.Services {
		scopes[entry.Service.Service] = entry.Service.ScopeID
	}
	assert.Equal(t, w.ID(), scopes["c"])
	assert.Equal(t, x.ID(), scopes["z"])
}

func TestHealthReport_Membership(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		setup  func(i *RootScope) func(...HealthReportOption) HealthReport
		listed []string
	}{
		{
			name: "plain_value",
			setup: func(i *RootScope) func(...HealthReportOption) HealthReport {
				ProvideNamedValue(i, "answer", 42)
				return i.HealthReport
			},
			listed: []string{},
		},
		{
			name: "lazy_never_invoked",
			setup: func(i *RootScope) func(...HealthReportOption) HealthReport {
				ProvideNamed(i, "db", func(Injector) (*healthReportTestSwitch, error) { return &healthReportTestSwitch{}, nil })
				return i.HealthReport
			},
			listed: []string{},
		},
		{
			name: "lazy_invoked",
			setup: func(i *RootScope) func(...HealthReportOption) HealthReport {
				ProvideNamed(i, "db", func(Injector) (*healthReportTestSwitch, error) { return &healthReportTestSwitch{}, nil })
				must1(InvokeNamed[*healthReportTestSwitch](i, "db"))
				return i.HealthReport
			},
			listed: []string{"[root]/db"},
		},
		{
			name: "transient_invoked",
			setup: func(i *RootScope) func(...HealthReportOption) HealthReport {
				ProvideNamedTransient(i, "tmp", func(Injector) (*healthReportTestSwitch, error) { return &healthReportTestSwitch{}, nil })
				must1(InvokeNamed[*healthReportTestSwitch](i, "tmp"))
				return i.HealthReport
			},
			listed: []string{},
		},
		{
			name: "value_with_alias",
			setup: func(i *RootScope) func(...HealthReportOption) HealthReport {
				ProvideNamedValue(i, "db", &healthReportTestSwitch{})
				must0(AsNamed[*healthReportTestSwitch, Healthchecker](i, "db", "hc"))
				return i.HealthReport
			},
			listed: []string{"[root]/db"},
		},
		{
			name: "alias_to_lazy_never_invoked",
			setup: func(i *RootScope) func(...HealthReportOption) HealthReport {
				ProvideNamed(i, "db", func(Injector) (*healthReportTestSwitch, error) { return &healthReportTestSwitch{}, nil })
				must0(AsNamed[*healthReportTestSwitch, Healthchecker](i, "db", "hc"))
				return i.HealthReport
			},
			listed: []string{},
		},
		{
			name: "export_from_root",
			setup: func(i *RootScope) func(...HealthReportOption) HealthReport {
				child := i.Scope("child")
				ProvideNamedValue(child, "db", &healthReportTestSwitch{})
				must0(ExportNamed[*healthReportTestSwitch](child, "db"))
				return i.HealthReport
			},
			listed: []string{"child/db"},
		},
		{
			name: "export_from_root_own_scope",
			setup: func(i *RootScope) func(...HealthReportOption) HealthReport {
				child := i.Scope("child")
				ProvideNamedValue(child, "db", &healthReportTestSwitch{})
				must0(ExportNamed[*healthReportTestSwitch](child, "db"))
				return i.self.HealthReport
			},
			listed: []string{"child/db"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)

			read := tt.setup(New())
			assert.Equal(t, tt.listed, healthReportTestListing(read()))
		})
	}
}

func TestHealthReport_Coverage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target func(i *RootScope) func(...HealthReportOption) HealthReport
		listed []string
	}{
		{
			name:   "root",
			target: func(i *RootScope) func(...HealthReportOption) HealthReport { return i.HealthReport },
			listed: []string{"[root]/cache", "[root]/db", "[root]/q", "child/db", "leaf/queue"},
		},
		{
			name: "child",
			target: func(i *RootScope) func(...HealthReportOption) HealthReport {
				child, _ := i.ChildByName("child")
				return child.HealthReport
			},
			listed: []string{"[root]/cache", "[root]/q", "child/db"},
		},
		{
			name: "grandchild",
			target: func(i *RootScope) func(...HealthReportOption) HealthReport {
				child, _ := i.ChildByName("child")
				leaf, _ := child.ChildByName("leaf")
				return leaf.HealthReport
			},
			listed: []string{"[root]/cache", "[root]/q", "child/db", "leaf/queue"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)

			i := New()
			ProvideNamedValue(i, "db", &healthReportTestSwitch{})
			ProvideNamedValue(i, "cache", &healthReportTestSwitch{})
			child := i.Scope("child")
			down := &healthReportTestSwitch{}
			down.set(errors.New("down"))
			ProvideNamedValue(child, "db", down)
			ProvideNamedValue(child, "q", &healthReportTestSwitch{}, AsDefault())
			ProvideNamedValue(i, "q", &healthReportTestSwitch{})
			ProvideNamedValue(child.Scope("leaf"), "queue", &healthReportTestSwitch{})

			assert.Equal(t, tt.listed, healthReportTestListing(tt.target(i)()))
		})
	}
}

func TestHealthReport_MonitoredNoChecks(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 10*time.Second)
	is := assert.New(t)

	scheduler := &manualScheduler{}
	i := NewWithOpts(&InjectorOpts{HealthScheduler: scheduler})
	db := &healthReportTestSwitch{}
	ProvideNamedValue(i, "db", db)
	require.NoError(t, i.Start())
	defer i.Shutdown()
	scheduler.runRound()

	var wg sync.WaitGroup
	var bad atomic.Int32
	for range 1000 {
		wg.Go(func() {
			report := i.HealthReport()
			if !report.Monitored || len(report.Services) != 1 || report.Services[0].State != HealthHealthy {
				bad.Add(1)
			}
		})
	}
	wg.Wait()

	is.Zero(bad.Load(), "reports not monitored or not healthy")
	is.Equal(int32(1), db.calls.Load(), "calls of the check")
}

func TestHealthReport_MonitoredFirstCheck(t *testing.T) {
	t.Parallel()

	t.Run("returns_nil", func(t *testing.T) {
		t.Parallel()
		testWithTimeout(t, 5*time.Second)
		is := assert.New(t)

		scheduler := &manualScheduler{}
		i := NewWithOpts(&InjectorOpts{Clock: clockwork.NewFakeClockAt(healthReportTestEpoch), HealthScheduler: scheduler})
		ProvideNamedValue(i, "db", &healthReportTestSwitch{})
		require.NoError(t, i.Start())
		defer i.Shutdown()

		report := i.HealthReport()
		is.True(report.Monitored)
		is.Equal(HealthHealthy, healthReportTestEntry(t, report, "db").State)
	})

	t.Run("in_flight_from_another_reader", func(t *testing.T) {
		t.Parallel()
		testWithTimeout(t, 5*time.Second)
		is := assert.New(t)

		scheduler := &manualScheduler{}
		i := NewWithOpts(&InjectorOpts{Clock: clockwork.NewFakeClockAt(healthReportTestEpoch), HealthScheduler: scheduler})
		db := newHealthRunTestBlocking(nil)
		ProvideNamedValue(i, "db", db)
		require.NoError(t, i.Start())
		defer i.Shutdown()

		first := make(chan HealthReport, 1)
		go func() { first <- i.HealthReport() }()
		require.Eventually(t, func() bool { return db.calls.Load() == 1 }, time.Second, time.Millisecond)

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		report := i.HealthReportWithContext(ctx)
		is.True(report.Monitored)
		entry := healthReportTestEntry(t, report, "db")
		is.Equal(HealthUnknown, entry.State)
		is.Equal(healthReportTestEpoch, entry.PendingSince)

		close(db.release)
		<-first
	})
}

func TestHealthReport_OnDemandTTL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ttl     time.Duration
		advance time.Duration
		calls   int32
	}{
		{name: "default_999ms", advance: 999 * time.Millisecond, calls: 1},
		{name: "default_1s", advance: time.Second, calls: 2},
		{name: "5s_4999ms", ttl: 5 * time.Second, advance: 4999 * time.Millisecond, calls: 1},
		{name: "5s_5s", ttl: 5 * time.Second, advance: 5 * time.Second, calls: 2},
		{name: "negative_0s", ttl: -time.Nanosecond, advance: 0, calls: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)
			is := assert.New(t)

			fc := clockwork.NewFakeClock()
			i := NewWithOpts(&InjectorOpts{Clock: fc, HealthReportCacheTTL: tt.ttl})
			db := &healthReportTestSwitch{}
			ProvideNamedValue(i, "db", db)

			first := i.HealthReport()
			fc.Advance(tt.advance)
			second := i.HealthReport()

			is.Equal(tt.calls, db.calls.Load(), "calls of the check")
			is.False(first.Monitored)
			is.False(second.Monitored)
		})
	}
}

func TestHealthReport_OnDemandShared(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		i := New()
		db := newHealthRunTestBlocking(nil)
		ProvideNamedValue(i, "db", db)

		reports := make(chan HealthReport, 50)
		for range 50 {
			go func() { reports <- i.HealthReport() }()
		}
		synctest.Wait()
		close(db.release)

		for range 50 {
			is.Equal(HealthHealthy, healthReportTestEntry(t, <-reports, "db").State)
		}
		is.Equal(int32(1), db.calls.Load(), "calls of the check")
	})
}

func TestHealthReport_CachedReadsLogNothing(t *testing.T) {
	t.Parallel()
	is := assert.New(t)

	log := &healthMonitorTestLog{}
	i := NewWithOpts(&InjectorOpts{Clock: clockwork.NewFakeClock(), Logf: log.logf})
	for n := range 50 {
		ProvideNamedValue(i, fmt.Sprintf("svc-%d", n), &healthReportTestSwitch{})
	}
	i.HealthReport()
	is.Len(log.containing("requested health check for service"), 50, "lines of the first read")

	for range 10 {
		i.HealthReport() // within the TTL: on the fake clock, no time passes
	}

	is.Len(log.containing("requested health check for service"), 50, "lines after the cached reads")
}

func TestHealthReport_RefreshedByForced(t *testing.T) {
	t.Parallel()

	dbName := NameOf[*healthReportTestSwitch]()

	tests := []struct {
		name  string
		entry func(i *RootScope)
	}{
		{name: "HealthCheck", entry: func(i *RootScope) { i.HealthCheck() }},
		{name: "HealthCheckWithContext", entry: func(i *RootScope) { i.HealthCheckWithContext(context.Background()) }},
		{name: "HealthCheckNamed", entry: func(i *RootScope) { _ = HealthCheckNamed(i, dbName) }},
		{name: "HealthCheck_typed", entry: func(i *RootScope) { _ = HealthCheck[*healthReportTestSwitch](i) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)
			is := assert.New(t)

			fc := clockwork.NewFakeClock()
			i := NewWithOpts(&InjectorOpts{Clock: fc, HealthReportCacheTTL: time.Hour})
			db := &healthReportTestSwitch{}
			ProvideValue(i, db)
			is.Equal(HealthHealthy, healthReportTestEntry(t, i.HealthReport(), dbName).State)

			errDown := errors.New("down")
			db.set(errDown)
			tt.entry(i)

			entry := healthReportTestEntry(t, i.HealthReport(), dbName)
			is.Equal(HealthUnhealthy, entry.State)
			is.Same(errDown, entry.Err)
		})
	}
}

func TestHealthReport_AbandonedNotRecorded(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		i := New()
		db := newHealthRunTestCtxAware()
		ProvideNamedValue(i, "db", db)

		ctx, cancel := context.WithCancel(context.Background())
		read := make(chan HealthReport, 1)
		go func() { read <- i.HealthReportWithContext(ctx) }()
		synctest.Wait()
		cancel()
		<-read
		synctest.Wait() // the abandoned check returned

		cancelled, cancelNow := context.WithCancel(context.Background())
		cancelNow()
		report := i.HealthReportWithContext(cancelled)
		synctest.Wait()

		assert.Zero(t, healthReportTestEntry(t, report, "db").CheckedAt)
		close(db.release)
	})
}

func TestHealthReport_NotRecordedAfterLeaving(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		leave func(t *testing.T, child *Scope)
	}{
		{name: "scope_shutdown", leave: func(t *testing.T, child *Scope) { require.True(t, child.Shutdown().Succeed) }},
		{name: "OverrideNamedValue", leave: func(_ *testing.T, child *Scope) { OverrideNamedValue(child, "db", 42) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)
			is := assert.New(t)

			i := New()
			recorder := &healthHookRecorder{}
			i.AddContainerHealthChangedHook(recorder.containerHook)
			child := i.Scope("child")
			var calls atomic.Int32
			release := make(chan struct{})
			ProvideNamedValue(child, "db", healthReportTestFunc{check: func(context.Context) error {
				if calls.Add(1) == 1 {
					return nil // recorded by the first read
				}
				<-release
				return errors.New("down")
			}})
			wrapper, ok := child.serviceGet("db")
			require.True(t, ok)
			is.Equal(HealthHealthy, healthReportTestEntry(t, i.HealthReport(), "db").State)
			require.Eventually(t, func() bool { return len(recorder.containerCalls()) == 1 }, time.Second, time.Millisecond)

			done := make(chan map[string]error, 1)
			go func() { done <- child.HealthCheck() }()
			require.Eventually(t, func() bool { return calls.Load() == 2 }, time.Second, time.Millisecond)

			tt.leave(t, child)
			before := recorder.containerCalls()
			close(release)
			<-done

			i.health.mu.Lock()
			_, recorded := i.health.records[wrapper]
			_, member := i.health.members[wrapper]
			i.health.mu.Unlock()
			is.False(recorded, "a health record of the registration that left")
			is.False(member, "a member for the registration that left")
			is.Never(func() bool { return len(recorder.containerCalls()) != len(before) }, 50*time.Millisecond, time.Millisecond,
				"container calls after the service left: %v", recorder.containerCalls())
		})
	}
}

// healthMonitorTestClosing is a Shutdowner that, during Shutdown, makes db
// fail, runs a forced check, and reads the report.
type healthMonitorTestClosing struct {
	root   *RootScope
	db     *healthReportTestSwitch
	report HealthReport
}

func (c *healthMonitorTestClosing) Shutdown() {
	c.db.set(errors.New("closed"))
	c.root.HealthCheck()
	c.report = c.root.HealthReport(IgnoreContainerPhase())
}

func TestHealthReport_NoRecordAfterShutdown(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)
	is := assert.New(t)

	i := New()
	recorder := &healthHookRecorder{}
	i.AddServiceHealthChangedHook(recorder.serviceHook)
	db := &healthReportTestSwitch{}
	ProvideNamedValue(i, "db", db)
	svc := &healthMonitorTestClosing{root: i, db: db}
	// svc depends on db, so it shuts down first
	ProvideNamed(i, "svc", func(inj Injector) (*healthMonitorTestClosing, error) {
		must1(InvokeNamed[*healthReportTestSwitch](inj, "db"))
		return svc, nil
	})
	must1(InvokeNamed[*healthMonitorTestClosing](i, "svc"))
	require.NoError(t, i.Start())
	is.Equal(HealthHealthy, healthReportTestEntry(t, i.HealthReport(), "db").State)

	i.Shutdown()

	entry := healthReportTestEntry(t, svc.report, "db")
	is.Equal(HealthHealthy, entry.State, "a result was recorded after shutdown began")
	is.NoError(entry.Err)
	time.Sleep(10 * time.Millisecond) // let a stray hook call land
	is.Equal([]healthHookCall{{HealthUnknown, HealthHealthy, ""}}, recorder.serviceCalls())
}

func TestHealthReport_ReaderBound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		timeout   time.Duration
		cancelled bool
		advance   time.Duration
		returns   bool
		message   string
	}{
		{name: "default_10s", advance: DefaultHealthReportCheckTimeout, returns: true, message: healthCheckTestDeadlineMessage},
		{name: "default_9999ms", advance: DefaultHealthReportCheckTimeout - time.Millisecond},
		{name: "20ms", timeout: 20 * time.Millisecond, advance: 20 * time.Millisecond, returns: true, message: healthCheckTestDeadlineMessage},
		{name: "unbounded_10m", timeout: -time.Nanosecond, advance: 10 * time.Minute},
		{name: "cancelled_before", cancelled: true, returns: true, message: "DI: health check timeout: context canceled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				is := assert.New(t)

				fc := clockwork.NewFakeClock()
				i := NewWithOpts(&InjectorOpts{Clock: fc, HealthCheckTimeout: tt.timeout})
				db := newHealthRunTestBlocking(nil)
				ProvideNamedValue(i, "db", db)

				ctx, cancel := context.WithCancel(context.Background())
				if tt.cancelled {
					cancel()
				}
				defer cancel()

				read := make(chan HealthReport, 1)
				go func() { read <- i.HealthReportWithContext(ctx) }()
				synctest.Wait()
				fc.Advance(tt.advance)
				synctest.Wait()

				if !tt.returns {
					is.Empty(read, "the read returned before its bound")
					close(db.release)
					<-read
					return
				}

				if is.Len(read, 1, "the read did not return at its bound") {
					entry := healthReportTestEntry(t, <-read, "db")
					is.Equal(HealthUnhealthy, entry.State)
					is.EqualError(entry.Err, tt.message)
					is.ErrorIs(entry.Err, ErrHealthCheckTimeout)
				}
				close(db.release)
			})
		})
	}
}

func TestHealthReport_ReaderLeaves(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		i := New()
		db := newHealthRunTestCtxAware()
		ProvideNamedValue(i, "db", db)

		ctxA, cancelA := context.WithCancel(context.Background())
		a := make(chan HealthReport, 1)
		go func() { a <- i.HealthReportWithContext(ctxA) }()
		synctest.Wait()

		b := make(chan HealthReport, 1)
		go func() { b <- i.HealthReport() }()
		synctest.Wait()

		cancelA()
		synctest.Wait()
		close(db.release)

		entryA := healthReportTestEntry(t, <-a, "db")
		is.Equal(HealthUnhealthy, entryA.State)
		is.ErrorIs(entryA.Err, ErrHealthCheckTimeout)
		is.ErrorIs(entryA.Err, context.Canceled)
		is.Equal(HealthHealthy, healthReportTestEntry(t, <-b, "db").State)

		is.Equal(HealthHealthy, healthReportTestEntry(t, i.HealthReport(), "db").State)
		is.Equal(int32(1), db.calls.Load(), "calls of the check")
	})
}

func TestHealthReport_LateResultDiscarded(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		fc := clockwork.NewFakeClock()
		recorder := &healthHookRecorder{}
		i := NewWithOpts(&InjectorOpts{Clock: fc, HealthReportCacheTTL: time.Hour})
		i.AddServiceHealthChangedHook(recorder.serviceHook)
		db := newHealthRunTestBlocking(nil)
		ProvideNamedValue(i, "db", db)

		read := make(chan HealthReport, 1)
		go func() { read <- i.HealthReport() }()
		synctest.Wait()
		fc.Advance(DefaultHealthReportCheckTimeout)
		first := <-read

		close(db.release)
		synctest.Wait() // the check returned nil, late

		for _, report := range []HealthReport{first, i.HealthReport()} {
			entry := healthReportTestEntry(t, report, "db")
			is.Equal(HealthUnhealthy, entry.State)
			is.ErrorIs(entry.Err, ErrHealthCheckTimeout)
		}
		is.Equal(int32(1), db.calls.Load(), "calls of the check")
		synctest.Wait()
		is.Equal([]healthHookCall{{HealthUnknown, HealthUnhealthy, healthCheckTestDeadlineMessage}}, recorder.serviceCalls())
	})
}

func TestHealthReport_TimeoutWhileAbandoned(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		scheduler *manualScheduler
		ttl       time.Duration
		// read waits for "db" as the row's reader, and returns the report
		// the consumer then sees
		read func(i *RootScope, scheduler *manualScheduler) HealthReport
	}{
		{
			name:      "monitored_round",
			scheduler: &manualScheduler{},
			read: func(i *RootScope, scheduler *manualScheduler) HealthReport {
				scheduler.runRound()
				return i.HealthReport()
			},
		},
		{
			name: "unmonitored_read",
			ttl:  -1,
			read: func(i *RootScope, _ *manualScheduler) HealthReport { return i.HealthReport() },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				is := assert.New(t)

				opts := &InjectorOpts{HealthCheckTimeout: 20 * time.Millisecond, HealthReportCacheTTL: tt.ttl}
				if tt.scheduler != nil {
					opts.HealthScheduler = tt.scheduler
				}
				i := NewWithOpts(opts)
				var calls atomic.Int32
				release := make(chan struct{})
				ProvideNamedValue(i, "db", healthReportTestFunc{check: func(context.Context) error {
					if calls.Add(1) > 1 {
						<-release // ignores its context
					}
					return nil
				}})
				wrapper, ok := i.serviceGet("db")
				require.True(t, ok)
				if tt.scheduler != nil {
					require.NoError(t, i.Start())
					defer i.Shutdown()
					tt.scheduler.runRound()
				} else {
					i.HealthReport()
				}
				require.Equal(t, HealthHealthy, healthReportTestRecorded(i, wrapper).state, "the recorded nil result")

				is.ErrorIs(i.HealthCheck()["db"], ErrHealthCheckTimeout) // abandons the hung check

				entry := healthReportTestEntry(t, tt.read(i, tt.scheduler), "db")
				is.Equal(HealthUnhealthy, entry.State)
				is.ErrorIs(entry.Err, ErrHealthCheckTimeout)
				record := healthReportTestRecorded(i, wrapper)
				is.Equal(HealthUnhealthy, record.state, "the recorded state")
				is.ErrorIs(record.err, ErrHealthCheckTimeout)

				close(release)
				synctest.Wait()
			})
		})
	}
}

// healthReportTestRecorded returns the health record of service in i.
func healthReportTestRecorded(i *RootScope, service any) healthRecord {
	i.health.mu.Lock()
	defer i.health.mu.Unlock()
	return i.health.records[service]
}

func TestHealthReport_BoundStartsAtSlot(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		fc := clockwork.NewFakeClock()
		i := NewWithOpts(&InjectorOpts{Clock: fc, HealthCheckParallelism: 1})
		ProvideNamedValue(i, "a", &healthCheckTestOnClock{clock: fc, duration: 9 * time.Second})
		ProvideNamedValue(i, "b", &healthReportTestSwitch{})

		read := make(chan HealthReport, 1)
		go func() { read <- i.HealthReport() }()
		synctest.Wait()
		fc.Advance(9 * time.Second)
		synctest.Wait()
		fc.Advance(time.Second)

		entry := healthReportTestEntry(t, <-read, "b")
		assert.Equal(t, HealthHealthy, entry.State)
		assert.NoError(t, entry.Err)
	})
}

func TestHealthClassification_Default(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []ProvideOption
	}{
		{name: "none"},
		{name: "nil", opts: []ProvideOption{nil}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)

			i := New()
			ProvideNamedValue(i, "db", &healthReportTestSwitch{}, tt.opts...)

			entry := healthReportTestEntry(t, i.HealthReport(), "db")
			assert.Equal(t, []string{"readiness"}, entry.Tags)
			assert.False(t, entry.Optional)
		})
	}
}

func TestHealthTags_Constants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		constant string
		want     string
	}{
		{name: "readiness", constant: HealthTagReadiness, want: "readiness"},
		{name: "liveness", constant: HealthTagLiveness, want: "liveness"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.constant)
		})
	}
}

func TestHealthClassification_Tags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []ProvideOption
		tags []string
	}{
		{name: "liveness", opts: []ProvideOption{WithHealthTags(HealthTagLiveness)}, tags: []string{"liveness"}},
		{name: "liveness_readiness", opts: []ProvideOption{WithHealthTags(HealthTagLiveness, HealthTagReadiness)}, tags: []string{"liveness", "readiness"}},
		{name: "repeated", opts: []ProvideOption{WithHealthTags("db"), WithHealthTags("db", "external")}, tags: []string{"db", "external"}},
		{name: "no_tag", opts: []ProvideOption{WithHealthTags()}, tags: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)

			i := New()
			ProvideNamedValue(i, "db", &healthReportTestSwitch{}, tt.opts...)

			assert.Equal(t, tt.tags, healthReportTestEntry(t, i.HealthReport(), "db").Tags)
		})
	}
}

func TestHealthClassification_Optional(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		opts     []ProvideOption
		tags     []string
		optional bool
	}{
		{name: "optional", opts: []ProvideOption{AsOptionalHealth()}, tags: []string{"readiness"}, optional: true},
		{name: "repeated", opts: []ProvideOption{AsOptionalHealth(), AsOptionalHealth()}, tags: []string{"readiness"}, optional: true},
		{name: "with_tags", opts: []ProvideOption{AsOptionalHealth(), WithHealthTags("cache")}, tags: []string{"cache"}, optional: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)

			i := New()
			ProvideNamedValue(i, "db", &healthReportTestSwitch{}, tt.opts...)

			entry := healthReportTestEntry(t, i.HealthReport(), "db")
			assert.Equal(t, tt.tags, entry.Tags)
			assert.Equal(t, tt.optional, entry.Optional)
		})
	}
}

func TestHealthClassification_CloneOverrideDefaultAlias(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		action   func(i *RootScope) func(...HealthReportOption) HealthReport
		tags     []string
		optional bool
	}{
		{
			name:     "clone",
			action:   func(i *RootScope) func(...HealthReportOption) HealthReport { return i.Clone().HealthReport },
			tags:     []string{"liveness"},
			optional: true,
		},
		{
			name: "override_without_options",
			action: func(i *RootScope) func(...HealthReportOption) HealthReport {
				OverrideNamedValue(i, "db", &healthReportTestSwitch{})
				return i.HealthReport
			},
			tags: []string{"readiness"},
		},
		{
			name: "override_with_tags",
			action: func(i *RootScope) func(...HealthReportOption) HealthReport {
				OverrideNamedValue(i, "db", &healthReportTestSwitch{}, WithHealthTags("x"))
				return i.HealthReport
			},
			tags: []string{"x"},
		},
		{
			name: "alias",
			action: func(i *RootScope) func(...HealthReportOption) HealthReport {
				must0(AsNamed[*healthReportTestSwitch, Healthchecker](i, "db", "hc"))
				return i.HealthReport
			},
			tags:     []string{"liveness"},
			optional: true,
		},
		{
			name: "yielded_default",
			action: func(i *RootScope) func(...HealthReportOption) HealthReport {
				child := i.Scope("child")
				ProvideNamedValue(child, "db", &healthReportTestSwitch{}, AsDefault(), WithHealthTags("x"))
				return child.HealthReport
			},
			tags:     []string{"liveness"},
			optional: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)

			i := New()
			ProvideNamedValue(i, "db", &healthReportTestSwitch{}, WithHealthTags(HealthTagLiveness), AsOptionalHealth())

			report := tt.action(i)()
			require.Len(t, report.Services, 1, "entries: %v", healthReportTestListing(report))
			entry := report.Services[0]
			assert.Equal(t, "db", entry.Service.Service)
			assert.Equal(t, tt.tags, entry.Tags)
			assert.Equal(t, tt.optional, entry.Optional)
		})
	}
}

func TestHealthReport_MatchTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		opts   []HealthReportOption
		listed []string
		state  HealthState
	}{
		{name: "none", listed: []string{"cache", "db", "proc"}, state: HealthUnhealthy},
		{name: "liveness", opts: []HealthReportOption{MatchHealthTags(HealthTagLiveness)}, listed: []string{"proc"}, state: HealthHealthy},
		{name: "readiness", opts: []HealthReportOption{MatchHealthTags(HealthTagReadiness)}, listed: []string{"cache", "db"}, state: HealthUnhealthy},
		{name: "custom", opts: []HealthReportOption{MatchHealthTags("cache")}, listed: []string{"cache"}, state: HealthHealthy},
		{name: "repeated", opts: []HealthReportOption{MatchHealthTags("cache"), MatchHealthTags(HealthTagLiveness)}, listed: []string{"cache", "proc"}, state: HealthHealthy},
		{name: "no_tag", opts: []HealthReportOption{MatchHealthTags()}, listed: []string{"cache", "db", "proc"}, state: HealthUnhealthy},
		{name: "unknown_tag", opts: []HealthReportOption{MatchHealthTags("unknown-tag")}, listed: []string{}, state: HealthHealthy},
		{name: "zero_value", opts: []HealthReportOption{{}}, listed: []string{"cache", "db", "proc"}, state: HealthUnhealthy},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)
			is := assert.New(t)

			i := New()
			db := &healthReportTestSwitch{}
			db.set(errors.New("down"))
			ProvideNamedValue(i, "db", db)
			ProvideNamedValue(i, "proc", &healthReportTestSwitch{}, WithHealthTags(HealthTagLiveness))
			ProvideNamedValue(i, "cache", &healthReportTestSwitch{}, WithHealthTags("cache", HealthTagReadiness))

			report := i.HealthReport(tt.opts...)

			listed := make([]string, 0, len(report.Services))
			for _, entry := range report.Services {
				listed = append(listed, entry.Service.Service)
			}
			is.Equal(tt.listed, listed)
			is.Equal(tt.state, report.State)
		})
	}
}

func TestHealthReport_MatchTagsOnDemand(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	i := New()
	db, proc := &healthReportTestSwitch{}, &healthReportTestSwitch{}
	ProvideNamedValue(i, "db", db)
	ProvideNamedValue(i, "proc", proc, WithHealthTags(HealthTagLiveness))

	i.HealthReport(MatchHealthTags(HealthTagLiveness))

	assert.Equal(t, int32(1), proc.calls.Load(), "calls of proc")
	assert.Equal(t, int32(0), db.calls.Load(), "calls of db")
}

// healthReportTestFunc is a HealthcheckerWithContext running check.
type healthReportTestFunc struct {
	check func(ctx context.Context) error
}

func (f healthReportTestFunc) HealthCheck(ctx context.Context) error { return f.check(ctx) }

func TestHealthReport_Degraded(t *testing.T) {
	t.Parallel()

	errSlow := errors.New("slow")

	tests := []struct {
		name     string
		optional bool
		check    func(ctx context.Context) error
		state    HealthState
		match    error // the entry's error matches it; nil for no error
		same     bool  // the entry's error is the check's own error
	}{
		{name: "optional_error", optional: true, check: func(context.Context) error { return errSlow }, state: HealthDegraded, match: errSlow, same: true},
		{name: "optional_nil", optional: true, check: func(context.Context) error { return nil }, state: HealthHealthy},
		{name: "optional_timeout", optional: true, check: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, state: HealthDegraded, match: ErrHealthCheckTimeout},
		{name: "optional_panic", optional: true, check: func(context.Context) error { panic("boom") }, state: HealthDegraded, match: ErrHealthCheckPanic},
		{name: "critical_error", check: func(context.Context) error { return errSlow }, state: HealthUnhealthy, match: errSlow, same: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)
			is := assert.New(t)

			i := NewWithOpts(&InjectorOpts{HealthCheckTimeout: 10 * time.Millisecond})
			var opts []ProvideOption
			if tt.optional {
				opts = append(opts, AsOptionalHealth())
			}
			ProvideNamedValue(i, "cache", healthReportTestFunc{check: tt.check}, opts...)

			entry := healthReportTestEntry(t, i.HealthReport(), "cache")
			is.Equal(tt.state, entry.State)
			switch {
			case tt.match == nil:
				is.NoError(entry.Err)
			case tt.same:
				is.Same(tt.match, entry.Err)
			default:
				is.ErrorIs(entry.Err, tt.match)
			}
		})
	}
}

func TestHealthCheck_IgnoresClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		opts  []ProvideOption
		entry func(i *RootScope) error
	}{
		{name: "optional_HealthCheck", opts: []ProvideOption{AsOptionalHealth()}, entry: func(i *RootScope) error { return i.HealthCheck()["cache"] }},
		{name: "liveness_HealthCheck", opts: []ProvideOption{WithHealthTags(HealthTagLiveness)}, entry: func(i *RootScope) error { return i.HealthCheck()["cache"] }},
		{name: "optional_HealthCheckNamed", opts: []ProvideOption{AsOptionalHealth()}, entry: func(i *RootScope) error { return HealthCheckNamed(i, "cache") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)

			errSlow := errors.New("slow")
			i := New()
			ProvideNamedValue(i, "cache", &healthCheckTestResult{err: errSlow}, tt.opts...)

			assert.Same(t, errSlow, tt.entry(i))
		})
	}
}

// healthReportTestStarter is a Starter whose Start blocks until released,
// then returns err.
type healthReportTestStarter struct {
	release chan struct{}
	err     error
}

func (s *healthReportTestStarter) Start(context.Context) error {
	<-s.release
	return s.err
}

// healthReportTestInPhase brings i, whose services are registered, to phase
// and returns a function that releases it and shuts i down. Starting,
// Started and Closing are held by a blocked Starter, Started hook or Closing
// hook; Failed is a Starter returning an error.
func healthReportTestInPhase(t *testing.T, i *RootScope, phase ContainerPhase) (release func()) {
	t.Helper()

	gate := make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(gate) }) }
	done := make(chan struct{})

	switch phase {
	case PhaseIdle:
		close(done)
	case PhaseStarting:
		ProvideNamedValue(i, "starter", &healthReportTestStarter{release: gate})
		go func() { defer close(done); _ = i.Start() }()
	case PhaseStarted:
		i.AddContainerStartedHook(func(context.Context, *RootScope) { <-gate })
		go func() { defer close(done); _ = i.Start() }()
	case PhaseReady:
		require.NoError(t, i.Start())
		close(done)
	case PhaseFailed:
		open()
		ProvideNamedValue(i, "starter", &healthReportTestStarter{release: gate, err: errors.New("boom")})
		require.Error(t, i.Start())
		close(done)
	case PhaseClosing:
		i.AddContainerClosingHook(func(context.Context, *RootScope) { <-gate })
		require.NoError(t, i.Start())
		go func() { defer close(done); i.Shutdown() }()
	case PhaseClosed:
		require.NoError(t, i.Start())
		i.Shutdown()
		close(done)
	default:
		t.Fatalf("unsupported phase %v", phase)
	}

	require.Eventually(t, func() bool { return i.Phase() == phase }, time.Second, time.Millisecond, "the container did not reach %v", phase)

	return func() {
		open()
		<-done
		i.Shutdown()
	}
}

func TestHealthReport_PhaseGate(t *testing.T) {
	t.Parallel()

	readiness, liveness := MatchHealthTags(HealthTagReadiness), MatchHealthTags(HealthTagLiveness)

	tests := []struct {
		name  string
		phase ContainerPhase
		opts  []HealthReportOption
		state HealthState
	}{
		{name: "idle", phase: PhaseIdle, state: HealthHealthy},
		{name: "starting", phase: PhaseStarting, state: HealthUnhealthy},
		{name: "started_readiness", phase: PhaseStarted, opts: []HealthReportOption{readiness}, state: HealthUnhealthy},
		{name: "ready", phase: PhaseReady, state: HealthHealthy},
		{name: "failed_readiness", phase: PhaseFailed, opts: []HealthReportOption{readiness}, state: HealthUnhealthy},
		{name: "failed_liveness", phase: PhaseFailed, opts: []HealthReportOption{liveness}, state: HealthUnhealthy},
		{name: "closing_readiness", phase: PhaseClosing, opts: []HealthReportOption{readiness}, state: HealthUnhealthy},
		{name: "closing_liveness", phase: PhaseClosing, opts: []HealthReportOption{liveness}, state: HealthHealthy},
		{name: "starting_liveness", phase: PhaseStarting, opts: []HealthReportOption{liveness}, state: HealthHealthy},
		{name: "closing_custom_tag", phase: PhaseClosing, opts: []HealthReportOption{MatchHealthTags("cache")}, state: HealthHealthy},
		{name: "closing_readiness_ignore_phase", phase: PhaseClosing, opts: []HealthReportOption{readiness, IgnoreContainerPhase()}, state: HealthHealthy},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)
			is := assert.New(t)

			i := New()
			ProvideNamedValue(i, "db", &healthReportTestSwitch{})
			i.HealthCheck() // records HealthHealthy
			release := healthReportTestInPhase(t, i, tt.phase)
			defer release()

			report := i.HealthReport(tt.opts...)
			is.Equal(tt.phase, report.Phase)
			is.Equal(tt.state, report.State)
		})
	}
}

func TestHealthReport_PhaseUnchanged(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)
	is := assert.New(t)

	i := New()
	db := &healthReportTestSwitch{}
	db.set(errors.New("down"))
	ProvideNamedValue(i, "db", db)
	release := healthReportTestInPhase(t, i, PhaseReady)
	defer release()

	is.Equal(HealthUnhealthy, i.HealthReport().State)
	i.HealthCheck()
	is.Equal(PhaseReady, i.Phase())
}

func TestHealthReport_Closed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []HealthReportOption
	}{
		{name: "none"},
		{name: "ignore_phase", opts: []HealthReportOption{IgnoreContainerPhase()}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)
			is := assert.New(t)

			i := New()
			db := &healthReportTestSwitch{}
			ProvideNamedValue(i, "db", db)
			release := healthReportTestInPhase(t, i, PhaseClosed)
			defer release()

			calls := db.calls.Load()
			report := i.HealthReport(tt.opts...)
			is.Equal(PhaseClosed, report.Phase)
			is.False(report.Monitored)
			is.Equal(HealthUnknown, report.State)
			is.NotNil(report.Services)
			is.Empty(report.Services)
			is.Equal(calls, db.calls.Load(), "calls by the read")
		})
	}
}

func TestHealthReport_OnDemandByPhase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		phase     ContainerPhase
		scheduler bool
		round     bool // runs the round once before the read
		calls     int32
		monitored bool
	}{
		{name: "idle", phase: PhaseIdle, calls: 1},
		{name: "idle_scheduler", phase: PhaseIdle, scheduler: true, calls: 1},
		{name: "failed", phase: PhaseFailed, calls: 1},
		{name: "ready", phase: PhaseReady, calls: 1},
		{name: "ready_scheduler_after_round", phase: PhaseReady, scheduler: true, round: true, calls: 0, monitored: true},
		{name: "starting", phase: PhaseStarting, calls: 0},
		{name: "started", phase: PhaseStarted, calls: 0},
		{name: "closing_scheduler", phase: PhaseClosing, scheduler: true, calls: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)
			is := assert.New(t)

			scheduler := &manualScheduler{}
			opts := &InjectorOpts{}
			if tt.scheduler {
				opts.HealthScheduler = scheduler
			}
			i := NewWithOpts(opts)
			db := &healthReportTestSwitch{}
			ProvideNamedValue(i, "db", db)
			release := healthReportTestInPhase(t, i, tt.phase)
			defer release()
			if tt.round {
				scheduler.runRound()
			}

			calls := db.calls.Load()
			report := i.HealthReport(IgnoreContainerPhase())
			is.Equal(tt.calls, db.calls.Load()-calls, "calls by the read")
			is.Equal(tt.monitored, report.Monitored)
		})
	}
}

func TestHealthReport_ClosingKeepsResults(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)
	is := assert.New(t)

	errDown := errors.New("down")
	i := New()
	db := &healthReportTestSwitch{}
	db.set(errDown)
	ProvideNamedValue(i, "db", db)
	i.HealthCheck() // records "down"
	release := healthReportTestInPhase(t, i, PhaseClosing)
	defer release()

	report := i.HealthReport(IgnoreContainerPhase())
	is.False(report.Monitored)
	entry := healthReportTestEntry(t, report, "db")
	is.Equal(HealthUnhealthy, entry.State)
	is.Same(errDown, entry.Err)
}

func TestHealthReport_LivenessZeroConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		phase ContainerPhase
		state HealthState
	}{
		{name: "idle", phase: PhaseIdle, state: HealthHealthy},
		{name: "starting", phase: PhaseStarting, state: HealthHealthy},
		{name: "ready", phase: PhaseReady, state: HealthHealthy},
		{name: "closing", phase: PhaseClosing, state: HealthHealthy},
		{name: "failed", phase: PhaseFailed, state: HealthUnhealthy},
		{name: "closed", phase: PhaseClosed, state: HealthUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)
			is := assert.New(t)

			i := New()
			db := &healthReportTestSwitch{}
			db.set(errors.New("down"))
			ProvideNamedValue(i, "db", db)
			release := healthReportTestInPhase(t, i, tt.phase)
			defer release()

			calls := db.calls.Load()
			report := i.HealthReport(MatchHealthTags(HealthTagLiveness))
			is.Empty(report.Services)
			is.Equal(tt.state, report.State)
			is.Equal(calls, db.calls.Load(), "calls by the read")
		})
	}
}

// healthReportTestLate is a Starter and Healthchecker whose check fails
// until it is started.
type healthReportTestLate struct {
	started atomic.Bool
	calls   atomic.Int32
}

func (l *healthReportTestLate) Start(context.Context) error {
	l.started.Store(true)
	return nil
}

func (l *healthReportTestLate) HealthCheck() error {
	l.calls.Add(1)
	if !l.started.Load() {
		return errors.New("not started")
	}
	return nil
}

func TestHealthReport_UnstartedNotChecked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		refresh bool
		state   HealthState
		calls   int32
	}{
		{name: "does_nothing_else", state: HealthUnknown, calls: 0},
		{name: "refresh", refresh: true, state: HealthHealthy, calls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)
			is := assert.New(t)

			i := New()
			release := healthReportTestInPhase(t, i, PhaseReady)
			defer release()

			late := &healthReportTestLate{}
			ProvideNamedValue(i, "late", late)
			if tt.refresh {
				require.NoError(t, i.Refresh())
			}

			calls := late.calls.Load()
			entry := healthReportTestEntry(t, i.HealthReport(), "late")
			is.Equal(tt.state, entry.State)
			is.Equal(tt.calls, late.calls.Load()-calls, "calls by the read")
		})
	}
}

func TestHealthReport_IdleChecksStarters(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	i := New()
	late := &healthReportTestLate{}
	late.started.Store(true)
	ProvideNamedValue(i, "svc", late)

	assert.Equal(t, HealthHealthy, healthReportTestEntry(t, i.HealthReport(), "svc").State)
}

func BenchmarkHealthReport_Monitored(b *testing.B) {
	scheduler := &manualScheduler{}
	i := NewWithOpts(&InjectorOpts{HealthScheduler: scheduler})
	for n := range 1000 {
		ProvideNamedValue(i, fmt.Sprintf("svc-%d", n), &healthReportTestSwitch{})
	}
	require.NoError(b, i.Start())
	defer i.Shutdown()
	scheduler.runRound()

	b.ReportAllocs()
	for b.Loop() {
		if report := i.HealthReport(); len(report.Services) != 1000 {
			b.Fatalf("got %d entries", len(report.Services))
		}
	}
}
