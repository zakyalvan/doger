package doger

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
)

// healthTarget is a service a scope-wide health check covers: key is its
// entry in the result map, and wrapper, in scope, the registration whose
// check runs for it, which is the source of an alias or an export.
type healthTarget struct {
	key     string
	name    string
	scope   *Scope
	wrapper any
}

// newHealthTarget returns the target of the service registered as name in
// scope, keyed key.
func newHealthTarget(key, name string, scope *Scope, service any) healthTarget {
	sourceScope, sourceName, source := healthSource(scope, name, service)
	return healthTarget{key: key, name: sourceName, scope: sourceScope, wrapper: source}
}

// healthSource returns the registration whose check runs for service,
// registered as name in scope: the source an alias or an export resolves
// to, or service itself, with its scope and name.
func healthSource(scope *Scope, name string, service any) (*Scope, string, any) {
	for range maxHealthSourceDepth {
		if alias, ok := service.(serviceWrapperAliasTarget); ok {
			injector, targetName := alias.aliasTarget()
			target, targetScope, found := injector.serviceGetRec(targetName)
			if !found {
				return scope, name, service
			}
			scope, name, service = targetScope, targetName, target
			continue
		}

		if export, ok := asExport(service); ok {
			sourceScope, sourceName, found := export.exportSource()
			if !found {
				return scope, name, service
			}
			source, found := sourceScope.serviceGet(sourceName)
			if !found {
				return scope, name, service
			}
			scope, name, service = sourceScope, sourceName, source
			continue
		}

		break
	}

	return scope, name, service
}

// maxHealthSourceDepth bounds the chain of aliases and exports healthSource
// follows.
const maxHealthSourceDepth = 64

// servicesSnapshot returns the services of s, copied under its read lock.
func (s *Scope) servicesSnapshot() []scopeService {
	s.mu.RLock()
	defer s.mu.RUnlock()

	services := make([]scopeService, 0, len(s.services))
	for name, service := range s.services {
		services = append(services, scopeService{name: name, service: service})
	}

	return services
}

// scopeService is a registration of a scope.
type scopeService struct {
	name    string
	service any
}

// visibleCheckers lists the services a health check requested from s
// covers: those of s and its ancestors, the nearest scope winning for a
// name. A default that yielded is skipped, its name keeping the result of
// the service it yielded to, and so is an export whose source s sees under
// its own name. It holds no scope lock once it returns.
func visibleCheckers(s *Scope) []healthTarget {
	targets := []healthTarget{}
	seen := map[string]struct{}{}

	for scope := s; scope != nil; scope = scope.parentScope {
		for _, entry := range scope.servicesSnapshot() {
			if _, ok := seen[entry.name]; ok {
				continue
			}
			if isYieldedDefaultIn(scope, entry.service) || scope.exportCount() > 0 && readerSeesSource(s, entry.service) {
				continue
			}

			seen[entry.name] = struct{}{}
			targets = append(targets, newHealthTarget(entry.name, entry.name, scope, entry.service))
		}
	}

	return targets
}

// healthCheckTargets runs the checks of targets, once per wrapper, under the
// global health check timeout, and returns their results by key.
func (s *Scope) healthCheckTargets(ctx context.Context, targets []healthTarget) map[string]error {
	s.logf("requested health check")

	if s.rootScope.opts.HealthCheckGlobalTimeout > 0 {
		clock := s.clock()
		ctx = withHealthCheckDeadline(ctx, clock.Now().Add(s.rootScope.opts.HealthCheckGlobalTimeout))
		var cancel context.CancelFunc
		ctx, cancel = clockwork.WithTimeout(ctx, clock, s.rootScope.opts.HealthCheckGlobalTimeout)
		defer cancel()
	}

	queued := map[any]*callWaiter{}
	for _, target := range targets {
		if _, ok := queued[target.wrapper]; !ok {
			queued[target.wrapper] = s.rootScope.queueWrapperCheck(ctx, target.scope, target.name, target.wrapper)
		}
	}

	byWrapper := make(map[any]error, len(queued))
	for wrapper, waiter := range queued {
		byWrapper[wrapper] = s.rootScope.health.wait(waiter)
	}

	results := make(map[string]error, len(targets))
	for _, target := range targets {
		results[target.key] = byWrapper[target.wrapper]
	}

	s.logf("got health check results: %v", results)

	return results
}

// serviceHealthCheck performs a health check on a specific service in the current scope.
// This method checks if the service implements the Healthchecker interface and calls
// its health check method if available.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - name: The name of the service to health check
//
// Returns an error if the health check fails, or nil if the service is healthy
// or doesn't implement the Healthchecker interface.
func (s *Scope) serviceHealthCheck(ctx context.Context, name string) error {
	s.mu.RLock()
	serviceAny, ok := s.services[name]
	s.mu.RUnlock()

	if !ok {
		return serviceNotFound(s, ErrServiceNotFound, []string{name})
	}

	// an alias or an export shares the call of its source
	scope, sourceName, source := healthSource(s, name, serviceAny)
	check := scope.wrapperHealthCheckFunc(sourceName, source)
	if check == nil {
		return nil
	}

	target := healthTarget{key: sourceName, name: sourceName, scope: scope, wrapper: source}
	return s.rootScope.health.wait(s.rootScope.joinOrStart(waitSpec{ctx: ctx}, target, check))
}

// wrapperHealthCheckFunc returns the health check of service, registered as
// name in s, or nil when it is not a health checker at this moment.
func (s *Scope) wrapperHealthCheckFunc(name string, serviceAny any) func(context.Context) checkOutcome {
	service, ok := serviceAny.(interface {
		serviceWrapperIsHealthchecker
		serviceWrapperHealthcheck
	})
	if ok {
		if !service.isHealthchecker() {
			return nil
		}

		root := s.rootScope
		return func(ctx context.Context) checkOutcome {
			return runHealthCheck(ctx, root, service.healthcheck)
		}
	}

	// Should never happen.
	panic(fmt.Errorf("DI: service `%s` is not healthchecker", name))
}

// rootForcedCheckers lists the services a root health check covers: those
// of the root scope, then those of the installed extension scopes, keyed
// `<scope name>/<name>`. An export from an extension scope is checked there,
// under the scope's key; a yielded default keeps the result of the service
// it yielded to.
func rootForcedCheckers(s *RootScope) []healthTarget {
	scopes := s.extensionScopes()
	targets := []healthTarget{}

	for _, entry := range s.self.servicesSnapshot() {
		if isYieldedDefaultIn(s.self, entry.service) ||
			s.self.exportCount() > 0 && slices.ContainsFunc(scopes, func(scope *Scope) bool { return readerSeesSource(scope, entry.service) }) {
			continue
		}
		targets = append(targets, newHealthTarget(entry.name, entry.name, s.self, entry.service))
	}

	for _, scope := range scopes {
		for _, entry := range scope.servicesSnapshot() {
			if isYieldedDefaultIn(scope, entry.service) {
				continue
			}
			targets = append(targets, newHealthTarget(scope.name+"/"+entry.name, entry.name, scope, entry.service))
		}
	}

	return targets
}

// queueWrapperCheck joins the health check in flight for service,
// registered as name in scope, or starts one, bounded by HealthCheckTimeout.
// It returns nil for a service that is not a health checker.
func (s *RootScope) queueWrapperCheck(ctx context.Context, scope *Scope, name string, service any) *callWaiter {
	check := scope.wrapperHealthCheckFunc(name, service)
	if check == nil {
		return nil
	}

	return s.joinOrStart(waitSpec{ctx: ctx, bound: s.opts.HealthCheckTimeout, pooled: true}, healthTarget{key: name, name: name, scope: scope, wrapper: service}, check)
}

// waitSpec is how a caller waits for a health check: ctx and bound, on the
// injector's clock, limit its wait and the check it starts, and pooled makes
// that check take a slot of the health check pool. A bound of 0 or less
// means none.
type waitSpec struct {
	ctx    context.Context
	bound  time.Duration
	pooled bool
	// reportPath records a timeout when bound elapses before the check
	// returns, and discards the check's later result.
	reportPath bool
}

// healthMonitor holds the health checks in flight of a root container, by
// service wrapper. Its mutex is never held while a check or user code runs.
type healthMonitor struct {
	root     *RootScope
	mu       sync.Mutex
	inflight map[any]*sharedCall
	records  map[any]healthRecord // the last result of each service checked

	// scheduling state, see startScheduling and stopScheduling
	sched       schedState
	stop        func(context.Context) error // stops the schedule, once scheduled
	roundCtx    context.Context             // cancelled when scheduling stops
	roundCancel context.CancelFunc
	scheduling  chan struct{} // closed once ScheduleHealthRounds returned
	closing     bool          // root Shutdown began: nothing is recorded, no hook is called

	// status-change hooks, see deliverTransitions
	members        map[any]healthTarget // the health checkers of the container, as last listed
	counts         memberCounts         // of members, by state and severity
	containerState HealthState          // overall state of members, without the phase gate
	transitions    []healthTransition   // waiting for delivery, in recording order
	delivering     bool                 // a goroutine is delivering transitions
}

// healthRecord is the last recorded result of a service's health check.
type healthRecord struct {
	target    healthTarget
	state     HealthState
	err       error
	checkedAt time.Time
	duration  time.Duration
}

// recordLocked records the result of a check of target: the single write
// path of records. A change of the service's state, and of the container's,
// is queued for the hooks. m.mu must be held.
func (m *healthMonitor) recordLocked(target healthTarget, record healthRecord) {
	if m.closing {
		return
	}

	_, optional := healthClassification(wrapperOptions(target.wrapper))
	record.target = target
	record.state = stateOf(record.err, optional)

	previous, recorded := m.records[target.wrapper]
	if _, member := m.members[target.wrapper]; member {
		m.memberCountsLocked(target.wrapper, -1)
	}
	if m.records == nil {
		m.records = map[any]healthRecord{}
	}
	m.records[target.wrapper] = record

	if !recorded || previous.state != record.state {
		m.transitions = append(m.transitions, healthTransition{
			service:  true,
			target:   target,
			previous: healthEntry(target, previous, recorded),
			current:  healthEntry(target, record, true),
		})
	}
	if m.members == nil {
		m.members = map[any]healthTarget{}
	}
	m.members[target.wrapper] = target
	m.memberCountsLocked(target.wrapper, 1)
	m.containerChangedLocked()
}

// forget drops the record of service, which left the container, and marks
// its call in flight as left, so its result is not recorded. With notify, a
// change of the container's state is queued for the hooks.
func (m *healthMonitor) forget(service any, notify bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if call, ok := m.inflight[service]; ok {
		call.left = true
	}
	if _, member := m.members[service]; member {
		m.memberCountsLocked(service, -1)
	}
	delete(m.records, service)
	delete(m.members, service)
	if notify {
		m.containerChangedLocked()
	}
}

// keepOnly makes members the health checkers of the container, dropping the
// records of the others, and queues a change of the container's state.
func (m *healthMonitor) keepOnly(members map[any]healthTarget) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for service := range m.members {
		if _, ok := members[service]; !ok {
			m.memberCountsLocked(service, -1)
			delete(m.records, service)
		}
	}
	for service := range members {
		if _, ok := m.members[service]; !ok {
			m.memberCountsLocked(service, 1)
		}
	}
	m.members = members
	m.containerChangedLocked()
}

// memberCounts counts the members of the container that decide its overall
// state: the unhealthy, the degraded, and the critical ones with no result.
type memberCounts struct {
	unhealthy, degraded, unknownCritical int
}

// state returns the overall state of the counted members, as
// overallHealthState does for their entries.
func (c memberCounts) state() HealthState {
	switch {
	case c.unhealthy > 0:
		return HealthUnhealthy
	case c.degraded > 0:
		return HealthDegraded
	case c.unknownCritical > 0:
		return HealthUnknown
	default:
		return HealthHealthy
	}
}

// memberCountsLocked adds delta to the count of the member service, from its
// current record. m.mu must be held.
func (m *healthMonitor) memberCountsLocked(service any, delta int) {
	record, recorded := m.records[service]
	switch {
	case !recorded:
		if opts := wrapperOptions(service); opts == nil || !opts.optionalHealth {
			m.counts.unknownCritical += delta
		}
	case record.state == HealthUnhealthy:
		m.counts.unhealthy += delta
	case record.state == HealthDegraded:
		m.counts.degraded += delta
	}
}

// sharedCall is a health check in flight, shared by every caller waiting
// for its result. A pooled call is queued until it takes a slot of the
// limiter, or a single-service check takes it over; it then runs until its
// check returns. Its fields after stop are guarded by the monitor's mutex.
type sharedCall struct {
	target  healthTarget // the service checked
	service any          // target.wrapper
	check   func(context.Context) checkOutcome
	pooled  bool          // takes a slot of the limiter
	bound   time.Duration // of a pooled call, started when it is claimed

	base   context.Context // the starter's context, see newHealthCheckContext
	cancel context.CancelCauseFunc
	stop   func() bool // stops the starter's deadline from cancelling base

	ctx          context.Context    // the check's context: base, bounded once claimed when pooled
	releaseBound context.CancelFunc // releases the bound of ctx
	claimed      chan struct{}      // closed once the check may run
	started      bool               // claimed: by the limiter, or taken over
	queuedAt     time.Time          // when the call was created, on the injector's clock
	startedAt    time.Time          // when the check began, once claimed

	waiters map[*callWaiter]struct{} // waiting for its result
	// next holds the callers that arrived once the call could no longer be
	// joined; they wait for it to return, then share a fresh call.
	next map[*callWaiter]struct{}
	// abandoned is set once every waiter stopped waiting: base is cancelled
	// and the call is never joined again.
	abandoned bool
	// timedOut is set once a report-path waiter recorded its timeout: the
	// check's result is then discarded.
	timedOut bool
	// left is set once the service left the container: nothing is recorded
	// for the call, and the callers in next get its result.
	left  bool
	clock clockwork.Clock
}

// joinable reports whether a caller arriving now may share the call's
// result: not once it was abandoned or its context ended.
func (c *sharedCall) joinable() bool {
	return !c.abandoned && contextDone(c.ctx) == nil
}

// claimLocked lets the check of c run, bounding its context from now when
// it is pooled. It reports false when c was claimed already. The monitor's
// mutex must be held.
func (c *sharedCall) claimLocked(clock clockwork.Clock) bool {
	if c.started {
		return false
	}

	c.started = true
	c.startedAt = clock.Now()
	if c.pooled {
		c.ctx, c.releaseBound = healthCheckContextWithin(c.base, clock, c.bound)
	}
	close(c.claimed)

	return true
}

// callWaiter is a caller waiting for a health check. Its result is decided
// when the check returns, against ctx at that moment, so a caller that reads
// it later still gets the result it would have seen then.
type callWaiter struct {
	ctx        context.Context    // bounds the wait
	parent     context.Context    // the caller's context, before the bound
	reportPath bool               // records a timeout at the bound
	release    context.CancelFunc // releases ctx once the wait is over
	call       *sharedCall        // the call waited for; guarded by the monitor's mutex
	done       chan struct{}      // closed once result is decided
	result     checkOutcome
}

// completed reports whether the waiter got the result of its check.
func (w *callWaiter) completed() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

// wait returns the result of the check waiter waits for, or a timeout error
// once the waiter's context is done first; nil for a nil waiter, which waits
// for no check. The last waiter to leave a check cancels it.
func (m *healthMonitor) wait(waiter *callWaiter) error {
	if waiter == nil {
		return nil
	}
	defer waiter.release()

	select {
	case <-waiter.done:
		return waiter.result.unwrap()
	case <-waiter.ctx.Done():
	}

	defer m.root.deliverTransitions() // runs after m.mu is released
	m.mu.Lock()
	defer m.mu.Unlock()

	select {
	case <-waiter.done:
		return waiter.result.unwrap()
	default:
	}

	return m.leaveLocked(waiter)
}

// leaveLocked removes waiter, whose context is done, from the call it waits
// for, joined or not, and returns its timeout error. A report-path waiter
// whose bound elapsed, not its caller's context, records the timeout. The
// last waiter to leave a joined call cancels it. m.mu must be held.
func (m *healthMonitor) leaveLocked(waiter *callWaiter) error {
	call := waiter.call
	err := timeoutError(waiter.ctx.Err())
	if waiter.reportPath && contextDone(waiter.parent) == nil {
		m.recordTimeoutLocked(call, err)
	}

	if _, ok := call.waiters[waiter]; !ok {
		delete(call.next, waiter)
		return err
	}
	delete(call.waiters, waiter)
	if len(call.waiters) == 0 && !call.abandoned {
		call.abandoned = true
		call.cancel(context.Canceled)
	}

	return err
}

// recordTimeoutLocked records err, the timeout of a report-path waiter, for
// the check of call, once: its later result is discarded. m.mu must be held.
func (m *healthMonitor) recordTimeoutLocked(call *sharedCall, err error) {
	if call.timedOut || call.left {
		return
	}
	call.timedOut = true

	now := call.clock.Now()
	m.recordLocked(call.target, healthRecord{err: err, checkedAt: now, duration: now.Sub(call.queuedAt)})
}

// joinOrStart returns a waiter, bounded as spec says, for the health check
// of service: it joins the call in flight, or starts one running check. A
// waiter that does not take a slot runs a joined call still queued for one
// at once, so composite checks complete under any limit.
func (s *RootScope) joinOrStart(spec waitSpec, target healthTarget, check func(context.Context) checkOutcome) *callWaiter {
	service := target.wrapper
	m := &s.health
	ctx, release := healthCheckContextWithin(spec.ctx, s.opts.Clock, spec.bound)
	waiter := &callWaiter{ctx: ctx, parent: spec.ctx, reportPath: spec.reportPath, release: release, done: make(chan struct{})}

	m.mu.Lock()
	call, ok := m.inflight[service]
	switch {
	case !ok:
		call = m.newCallLocked(target, check, spec, waiter, s.healthSlots != nil, s.opts.Clock)
		m.mu.Unlock()
		s.startCall(call)
		return waiter
	case call.joinable():
		call.waiters[waiter] = struct{}{}
		waiter.call = call
		takeOver := !spec.pooled && call.claimLocked(s.opts.Clock)
		m.mu.Unlock()
		if takeOver {
			go s.runCall(call)
		}
		return waiter
	default:
		call.next[waiter] = struct{}{}
		waiter.call = call
		m.mu.Unlock()
		return waiter
	}
}

// newCallLocked registers a call for service started by starter, waiting as
// spec says. With limited, a pooled call waits for a slot and bounds its
// check from then on; any other call runs at once, bounded by the starter's
// context. m.mu must be held.
func (m *healthMonitor) newCallLocked(target healthTarget, check func(context.Context) checkOutcome, spec waitSpec, starter *callWaiter, limited bool, clock clockwork.Clock) *sharedCall {
	service := target.wrapper
	call := &sharedCall{
		target:       target,
		clock:        clock,
		queuedAt:     clock.Now(),
		service:      service,
		check:        check,
		pooled:       spec.pooled && limited,
		bound:        spec.bound,
		releaseBound: func() {},
		claimed:      make(chan struct{}),
		waiters:      map[*callWaiter]struct{}{starter: {}},
		next:         map[*callWaiter]struct{}{},
	}
	if call.pooled {
		call.base, call.cancel, call.stop = newHealthCheckContext(spec.ctx)
	} else {
		call.base, call.cancel, call.stop = newHealthCheckContext(starter.ctx)
		call.started = true
		call.startedAt = call.queuedAt
		close(call.claimed)
	}
	call.ctx = call.base
	starter.call = call

	if m.inflight == nil {
		m.inflight = map[any]*sharedCall{}
	}
	m.inflight[service] = call

	return call
}

// startCall runs call, just created, on its own goroutine. A pooled call
// first waits there for a slot of the limiter, holds no lock meanwhile, and
// gives up once it is taken over or abandoned.
func (s *RootScope) startCall(call *sharedCall) {
	call.target.scope.logf("requested health check for service %s", call.target.name)

	if !call.pooled {
		go s.runCall(call)
		return
	}

	go func() {
		acquired := false
		select {
		case s.healthSlots <- struct{}{}:
			acquired = true
			defer func() { <-s.healthSlots }()
		case <-call.claimed:
		case <-call.base.Done():
		}

		s.health.mu.Lock()
		if call.started {
			s.health.mu.Unlock()
			return // taken over
		}
		if !acquired || contextDone(call.base) != nil {
			// abandoned, or its starter's deadline passed, while queued: the
			// check never runs
			call.started = true
			s.health.mu.Unlock()
			s.finishCall(call, checkOutcome{err: call.base.Err()}, false)
			return
		}
		call.claimLocked(s.opts.Clock)
		s.health.mu.Unlock()

		s.runCall(call)
	}()
}

// runCall runs the check of call and finishes it.
func (s *RootScope) runCall(call *sharedCall) {
	s.finishCall(call, call.check(call.ctx), true)
}

// finishCall decides the result of each waiter of call, whose check
// returned err, and records it when the check ran and was not abandoned.
// The callers that arrived once it could no longer be joined then share a
// fresh call, or get its result when its service left the container.
func (s *RootScope) finishCall(call *sharedCall, outcome checkOutcome, ran bool) {
	call.stop()
	now := s.opts.Clock.Now()
	err := outcome.err
	waited := false

	m := &s.health
	m.mu.Lock()
	delete(m.inflight, call.service)
	if ran && !call.abandoned && !call.timedOut && !call.left {
		m.recordLocked(call.target, healthRecord{
			err:       healthCheckResult(call.ctx, s.opts.Clock, err),
			checkedAt: now,
			duration:  now.Sub(call.startedAt),
		})
	}
	for waiter := range call.waiters {
		waiter.result = checkOutcome{err: callResult(waiter, call, s.opts.Clock, err), hookPanic: outcome.hookPanic}
		close(waiter.done)
		waited = true
	}

	var fresh *sharedCall
	for waiter := range call.next {
		if call.left {
			waiter.result = checkOutcome{err: callResult(waiter, call, s.opts.Clock, err), hookPanic: outcome.hookPanic}
			close(waiter.done)
			waited = true
			continue
		}
		if fresh == nil {
			fresh = m.newCallLocked(call.target, call.check, waitSpec{ctx: waiter.ctx, pooled: call.pooled, bound: call.bound}, waiter, call.pooled, s.opts.Clock)
			continue
		}
		fresh.waiters[waiter] = struct{}{}
		waiter.call = fresh
	}
	releaseBound := call.releaseBound
	m.mu.Unlock()

	releaseBound()
	call.cancel(nil)
	if fresh != nil {
		s.startCall(fresh)
	}
	s.deliverTransitions()

	if outcome.hookPanic != nil && !waited {
		panic(outcome.hookPanic) // no caller to receive it
	}
}

// callResult is the result of a check that returned err for waiter: a
// timeout error once the waiter's context or the check's context is done,
// the check's own result otherwise.
func callResult(waiter *callWaiter, call *sharedCall, clock clockwork.Clock, err error) error {
	if cause := contextDone(waiter.ctx); cause != nil {
		return timeoutError(cause)
	}
	if cause := contextDone(call.ctx); cause != nil {
		return timeoutError(cause)
	}

	return healthCheckResult(waiter.ctx, clock, err)
}

// healthCheckContext is the context of a shared check: it carries the
// values and the deadline of the caller that started the check, and is done
// when that deadline passes or the check is cancelled, but not when that
// caller merely stops waiting.
type healthCheckContext struct {
	context.Context // the starter's values, cancelled by the call
	deadline        time.Time
	hasDeadline     bool
}

func (c healthCheckContext) Deadline() (time.Time, bool) { return c.deadline, c.hasDeadline }

// Err reports context.DeadlineExceeded once the starter's deadline passed.
func (c healthCheckContext) Err() error {
	err := c.Context.Err()
	if err != nil && errors.Is(context.Cause(c.Context), context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}

	return err
}

// newHealthCheckContext returns the context of a check started by a caller
// waiting with starter, its cancel function, and the function that stops
// starter's deadline from cancelling it.
func newHealthCheckContext(starter context.Context) (context.Context, context.CancelCauseFunc, func() bool) {
	base, cancel := context.WithCancelCause(context.WithoutCancel(starter))
	deadline, hasDeadline := starter.Deadline()

	stop := context.AfterFunc(starter, func() {
		if errors.Is(starter.Err(), context.DeadlineExceeded) {
			cancel(context.DeadlineExceeded)
		}
	})

	return healthCheckContext{Context: base, deadline: deadline, hasDeadline: hasDeadline}, cancel, stop
}

// runHealthCheck runs check, recovering its panic as an error matching
// ErrHealthCheckPanic. The panic is passed to HookContainerPanic of root
// when set; a panic that hook raises is recovered too and kept in the
// outcome, for the callers waiting for the check to re-raise.
func runHealthCheck(ctx context.Context, root *RootScope, check func(context.Context) error) (outcome checkOutcome) {
	defer func() {
		if r := recover(); r != nil {
			outcome.err = healthCheckPanicError(r)
			if root.opts.HookContainerPanic != nil {
				outcome.hookPanic, _ = callHookSafely(func() { root.opts.HookContainerPanic(root, "healthcheck", r) })
			}
		}
	}()

	return checkOutcome{err: check(ctx)}
}

// checkOutcome is how a health check ended: err is its result, and
// hookPanic, when not nil, the panic HookContainerPanic raised for its own
// panic, which each caller waiting for the check re-raises.
type checkOutcome struct {
	err       error
	hookPanic any
}

// unwrap returns the outcome's error, or re-raises its hook panic.
func (o checkOutcome) unwrap() error {
	if o.hookPanic != nil {
		panic(o.hookPanic)
	}

	return o.err
}

// healthCheckPanicError returns the result of a health check that panicked
// with value: an error matching ErrHealthCheckPanic and, for an error value
// (such as a *runtime.PanicNilError), that error too.
func healthCheckPanicError(value any) error {
	if err, ok := value.(error); ok {
		return fmt.Errorf("%w: %w", ErrHealthCheckPanic, err)
	}

	return fmt.Errorf("%w: %v", ErrHealthCheckPanic, value)
}
