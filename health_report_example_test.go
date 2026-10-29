package doger

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jonboulle/clockwork"
)

// healthReportExampleDatabase is a health check that counts its calls.
type healthReportExampleDatabase struct{ checks int }

func (db *healthReportExampleDatabase) HealthCheck() error {
	db.checks++
	return nil
}

func ExampleScope_HealthReport() {
	// Without a HealthScheduler, a read checks the services whose result is
	// missing or older than HealthReportCacheTTL (1s by default), and reuses
	// the others: many readers cost the same as one.
	clock := clockwork.NewFakeClock()
	injector := NewWithOpts(&InjectorOpts{
		Clock:                clock,
		HealthReportCacheTTL: 5 * time.Second,
	})

	db := &healthReportExampleDatabase{}
	ProvideNamedValue(injector, "db", db)

	report := injector.HealthReport()
	fmt.Println(report.State, report.Phase, report.Monitored)
	for _, entry := range report.Services {
		fmt.Println(entry.Service.Service, entry.State)
	}

	clock.Advance(time.Second)
	injector.HealthReport() // within the TTL: no check
	fmt.Println("checks:", db.checks)

	clock.Advance(4 * time.Second)
	injector.HealthReport() // the result is 5s old: checked again
	fmt.Println("checks:", db.checks)

	// Output:
	// healthy idle false
	// db healthy
	// checks: 1
	// checks: 2
}

// healthReportExampleCheck is a health check that passes.
type healthReportExampleCheck struct{}

func (healthReportExampleCheck) HealthCheck() error { return nil }

func ExampleMatchHealthTags() {
	injector := New()

	// Unclassified: tagged readiness and critical.
	ProvideNamedValue(injector, "db", healthReportExampleCheck{})
	// Readiness too, but a failure only degrades the report.
	ProvideNamedValue(injector, "cache", healthReportExampleCheck{}, AsOptionalHealth())
	// Liveness: a check of the process itself, never of an external system.
	ProvideNamedValue(injector, "process", healthReportExampleCheck{}, WithHealthTags(HealthTagLiveness))

	for _, tag := range []string{HealthTagReadiness, HealthTagLiveness} {
		report := injector.HealthReport(MatchHealthTags(tag))
		fmt.Println(tag, report.State)
		for _, entry := range report.Services {
			fmt.Println(" ", entry.Service.Service, entry.Tags, "optional:", entry.Optional)
		}
	}

	// Output:
	// readiness healthy
	//   cache [readiness] optional: true
	//   db [readiness] optional: false
	// liveness healthy
	//   process [liveness] optional: false
}

// healthReportExampleScheduler is a HealthScheduler that keeps the round, so
// the example runs it at will; dogergocron.New() runs it every 30s.
type healthReportExampleScheduler struct{ round func(context.Context) }

func (s *healthReportExampleScheduler) ScheduleHealthRounds(_ clockwork.Clock, round func(context.Context)) (func(context.Context) error, error) {
	s.round = round
	return func(context.Context) error { return nil }, nil
}

func ExampleRootScope_HealthReport() {
	scheduler := &healthReportExampleScheduler{}
	injector := NewWithOpts(&InjectorOpts{HealthScheduler: scheduler})
	ProvideNamedValue(injector, "db", healthReportExampleCheck{})

	// The container calls the scheduler once it is ready; reads then return
	// the results the rounds recorded, without running checks.
	_ = injector.Start()
	scheduler.round(context.Background())
	report := injector.HealthReport()
	fmt.Println(report.Phase, report.Monitored, report.State)

	// While Shutdown runs, readiness is gated: traffic drains whatever the
	// checks say. Liveness and IgnoreContainerPhase are not gated.
	injector.AddContainerClosingHook(func(context.Context, *RootScope) {
		fmt.Println("readiness:", injector.HealthReport(MatchHealthTags(HealthTagReadiness)).State)
		fmt.Println("liveness:", injector.HealthReport(MatchHealthTags(HealthTagLiveness)).State)
		fmt.Println("ignoring the phase:", injector.HealthReport(IgnoreContainerPhase()).State)
	})
	injector.Shutdown()

	// Output:
	// ready true healthy
	// readiness: unhealthy
	// liveness: healthy
	// ignoring the phase: healthy
}

// healthReportExampleFlaky is a health check that fails once down is set.
type healthReportExampleFlaky struct{ down atomic.Bool }

func (f *healthReportExampleFlaky) HealthCheck() error {
	if f.down.Load() {
		return errors.New("connection refused")
	}
	return nil
}

func ExampleRootScope_AddServiceHealthChangedHook() {
	injector := New()
	db := &healthReportExampleFlaky{}
	ProvideNamedValue(injector, "db", db)

	// Hooks run after the result reached its callers, one at a time and in
	// recording order, so they suit alerts and metrics. They should be fast.
	changes := make(chan string, 4)
	injector.AddServiceHealthChangedHook(func(_ *Scope, name string, previous, current ServiceHealth) {
		changes <- fmt.Sprintf("%s: %s -> %s (%v)", name, previous.State, current.State, current.Err)
	})

	injector.HealthCheck() // the first result is a change too
	fmt.Println(<-changes)

	db.down.Store(true)
	injector.HealthCheck()
	fmt.Println(<-changes)

	injector.HealthCheck() // same state: no call
	select {
	case change := <-changes:
		fmt.Println(change)
	case <-time.After(10 * time.Millisecond):
		fmt.Println("no change")
	}

	// Output:
	// db: unknown -> healthy (<nil>)
	// db: healthy -> unhealthy (connection refused)
	// no change
}
