package doger

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jonboulle/clockwork"
)

// schedState is where a container's HealthScheduler is.
type schedState int

const (
	schedIdle      schedState = iota // not called
	schedStarting                    // ScheduleHealthRounds is running
	schedScheduled                   // rounds are scheduled: reports are monitored
	schedFailed                      // ScheduleHealthRounds failed: reports stay unmonitored
	schedStopped                     // root Shutdown began
)

// HealthScheduler schedules the health rounds of a container, keeping its
// HealthReport results fresh. Set it in InjectorOpts.HealthScheduler; the
// github.com/kartaladev/doger/scheduler/gocron module provides one.
//
// The container calls ScheduleHealthRounds once, when it reaches PhaseReady
// and before the Ready hooks run, with the injector clock and the round to
// run. Each call of round checks every eligible health checker once and
// returns once each result is recorded or its bound elapsed; it honors ctx.
// The returned stop function is called once, when root Shutdown begins and
// before the Closing hooks run, with a context Shutdown's context cannot
// cancel: it must stop scheduling and return once no round of this schedule
// runs. Each call is an independent schedule, so one value may serve several
// containers, such as a container and its clones.
//
// An error or a panic leaves the container unmonitored: Start still
// succeeds, the error or panic is logged, or a panic is passed to
// HookContainerPanic with the point "healthscheduler" when it is set, and
// reads check stale services on demand.
type HealthScheduler interface {
	ScheduleHealthRounds(clock clockwork.Clock, round func(ctx context.Context)) (stop func(ctx context.Context) error, err error)
}

// startScheduling calls the HealthScheduler, if any, for a container that
// became ready. It holds no lock while the scheduler runs.
func (s *RootScope) startScheduling() {
	scheduler := s.opts.HealthScheduler
	if scheduler == nil {
		return
	}

	m := &s.health
	m.mu.Lock()
	if m.sched != schedIdle {
		m.mu.Unlock()
		return
	}
	m.sched = schedStarting
	m.roundCtx, m.roundCancel = context.WithCancel(context.Background())
	m.scheduling = make(chan struct{})
	m.mu.Unlock()

	// a panic HookContainerPanic raises propagates through the deferred
	// update, leaving errSchedulerInterrupted
	var stop func(context.Context) error
	err := errSchedulerInterrupted
	defer func() { m.finishScheduling(stop, err) }()

	stop, err = s.callScheduler(scheduler)
}

// errSchedulerInterrupted is the outcome of a ScheduleHealthRounds call a
// panic interrupted.
var errSchedulerInterrupted = errors.New("DI: health scheduler interrupted")

// finishScheduling records the outcome of ScheduleHealthRounds: its stop
// function, nil on failure, which stopScheduling calls once shutdown began,
// and whether rounds are scheduled, unless shutdown began meanwhile. It then
// signals that the call returned.
func (m *healthMonitor) finishScheduling(stop func(context.Context) error, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.stop = stop
	if m.sched == schedStarting {
		m.sched = schedScheduled
		if err != nil {
			m.sched = schedFailed
		}
	}
	close(m.scheduling)
}

// stopScheduling stops the container's health scheduling as root Shutdown
// begins: nothing is recorded from now on, the rounds in progress and the
// contexts of the checks in flight are cancelled, and the scheduler's stop
// function is called, once, with a context ctx cannot cancel, after an
// in-progress ScheduleHealthRounds returned. The checks in flight are not
// waited for: a check ignoring its context may still run while its service
// shuts down. An error of stop is logged; a panic is passed to
// HookContainerPanic when it is set, and logged otherwise.
func (s *RootScope) stopScheduling(ctx context.Context) {
	m := &s.health
	m.mu.Lock()
	m.closing = true
	m.sched = schedStopped
	cancel, scheduling := m.roundCancel, m.scheduling
	for _, call := range m.inflight {
		call.cancel(ErrStartAfterShutdown) // cancelled, not waited for
	}
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if scheduling != nil {
		<-scheduling
	}

	m.mu.Lock()
	stop := m.stop
	m.stop = nil
	m.mu.Unlock()
	if stop == nil {
		return
	}

	s.callStop(ctx, stop)
}

// callStop calls the scheduler's stop function with a context ctx cannot
// cancel. Its error is logged; its panic is passed to HookContainerPanic when
// it is set, and logged otherwise.
func (s *RootScope) callStop(ctx context.Context, stop func(context.Context) error) {
	recovered, panicked := callHookSafely(func() {
		if err := stop(context.WithoutCancel(ctx)); err != nil {
			s.opts.Logf("DI: health scheduler stop failed: %v", err)
		}
	})
	switch {
	case !panicked:
	case s.opts.HookContainerPanic != nil:
		s.opts.HookContainerPanic(s, "healthscheduler", recovered)
	default:
		s.opts.Logf("DI: health scheduler stop panicked: %v", recovered)
	}
}

// callScheduler calls scheduler, recovering its panic: an error or a panic is
// logged, or a panic is passed to HookContainerPanic when it is set, and
// returned as an error.
func (s *RootScope) callScheduler(scheduler HealthScheduler) (stop func(context.Context) error, err error) {
	var recovered any
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				recovered, panicked = r, true
			}
		}()
		stop, err = scheduler.ScheduleHealthRounds(s.opts.Clock, s.healthRound)
	}()

	switch {
	case panicked && s.opts.HookContainerPanic != nil:
		s.opts.HookContainerPanic(s, "healthscheduler", recovered)
		return nil, fmt.Errorf("DI: health scheduler panicked: %v", recovered)
	case panicked:
		err = fmt.Errorf("%v", recovered)
	case err == nil && stop == nil:
		stop = func(context.Context) error { return nil }
	}
	if err != nil {
		s.opts.Logf("DI: health scheduler failed: %v", err)
		return nil, err
	}

	return stop, nil
}

// healthRound checks every eligible health checker of the container once,
// within ctx and the report bound, and returns once each result is recorded
// or its bound elapsed. It returns at once after scheduling stopped.
func (s *RootScope) healthRound(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}

	m := &s.health
	m.mu.Lock()
	roundCtx := m.roundCtx
	stopped := m.sched == schedStopped || roundCtx == nil
	m.mu.Unlock()
	if stopped {
		return
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(roundCtx, cancel)()

	bound := reportBound(s.opts)
	covered := s.coveredChecks(containerCheckers(s))
	m.keepOnly(coveredMembers(covered))
	s.deliverTransitions()
	for _, c := range covered {
		if eligibleForReport(s, c.target.wrapper) {
			c.waiter = s.joinOrStart(waitSpec{ctx: ctx, bound: bound, pooled: true, reportPath: true}, c.target, c.check)
		}
	}
	for _, c := range covered {
		_ = m.wait(c.waiter)
	}
}

// monitored reports whether a HealthScheduler schedules the container's
// rounds, in phase.
func (m *healthMonitor) monitored(phase ContainerPhase) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.sched == schedScheduled && phase != PhaseClosing && phase != PhaseClosed
}

// neverChecked reports whether service has no recorded result and no check
// in flight.
func (m *healthMonitor) neverChecked(service any) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, recorded := m.records[service]
	_, inflight := m.inflight[service]
	return !recorded && !inflight
}

// healthTransition is a state change waiting for the hooks: a service's
// (service set), or the container's.
type healthTransition struct {
	service           bool
	target            healthTarget
	previous, current ServiceHealth
	containerPrevious HealthState
	containerCurrent  HealthState
}

// containerChangedLocked recomputes the overall state of the members,
// without the phase gate, and queues its change for the container hooks.
// m.mu must be held.
func (m *healthMonitor) containerChangedLocked() {
	if m.closing {
		return
	}

	current := m.counts.state()
	if current == m.containerState {
		return
	}
	m.transitions = append(m.transitions, healthTransition{containerPrevious: m.containerState, containerCurrent: current})
	m.containerState = current
}

// deliverTransitions calls the hooks for the queued transitions, in order,
// one at a time and with no lock held, unless another goroutine is
// delivering them: that goroutine then delivers these too. Nothing is
// delivered once root Shutdown began.
func (s *RootScope) deliverTransitions() {
	m := &s.health
	m.mu.Lock()
	if m.delivering {
		m.mu.Unlock()
		return
	}
	m.delivering = true

	defer func() {
		// reached with m.mu held, or by a panic HookContainerPanic raised
		if r := recover(); r != nil {
			m.mu.Lock()
			m.delivering = false
			m.mu.Unlock()
			panic(r)
		}
	}()

	for len(m.transitions) > 0 && !m.closing {
		transition := m.transitions[0]
		m.transitions = m.transitions[1:]
		serviceHooks, containerHooks := s.opts.HookServiceHealthChanged, s.opts.HookContainerHealthChanged
		m.mu.Unlock()

		s.deliverTransition(transition, serviceHooks, containerHooks)

		m.mu.Lock()
	}
	m.transitions = nil
	m.delivering = false
	m.mu.Unlock()
}

// deliverTransition calls the hooks of transition, in order, skipping nil
// ones.
func (s *RootScope) deliverTransition(transition healthTransition, serviceHooks []func(*Scope, string, ServiceHealth, ServiceHealth), containerHooks []func(*RootScope, HealthState, HealthState)) {
	if transition.service {
		for _, hook := range serviceHooks {
			if hook != nil {
				s.callHealthHook(func() { hook(transition.target.scope, transition.target.name, transition.previous, transition.current) })
			}
		}
		return
	}

	for _, hook := range containerHooks {
		if hook != nil {
			s.callHealthHook(func() { hook(s, transition.containerPrevious, transition.containerCurrent) })
		}
	}
}

// callHealthHook calls a health hook, recovering its panic: the panic goes
// to HookContainerPanic with the point "healthchanged" when it is set, and
// is logged otherwise.
func (s *RootScope) callHealthHook(call func()) {
	recovered, panicked := callHookSafely(call)
	if !panicked {
		return
	}

	if s.opts.HookContainerPanic != nil {
		s.opts.HookContainerPanic(s, "healthchanged", recovered)
		return
	}
	s.opts.Logf("DI: health hook panicked: %v", recovered)
}

// AddServiceHealthChangedHook adds a hook called when a service's recorded
// health state changes; see [InjectorOpts.HookServiceHealthChanged].
func (s *RootScope) AddServiceHealthChangedHook(hook func(scope *Scope, serviceName string, previous, current ServiceHealth)) {
	s.health.mu.Lock()
	defer s.health.mu.Unlock()

	s.opts.HookServiceHealthChanged = append(slices.Clone(s.opts.HookServiceHealthChanged), hook)
}

// AddContainerHealthChangedHook adds a hook called when the overall health
// state of the container changes; see
// [InjectorOpts.HookContainerHealthChanged].
func (s *RootScope) AddContainerHealthChangedHook(hook func(root *RootScope, previous, current HealthState)) {
	s.health.mu.Lock()
	defer s.health.mu.Unlock()

	s.opts.HookContainerHealthChanged = append(slices.Clone(s.opts.HookContainerHealthChanged), hook)
}
