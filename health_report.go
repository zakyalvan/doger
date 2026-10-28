package doger

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	// DefaultHealthReportCacheTTL is the HealthReportCacheTTL used when it
	// is 0.
	DefaultHealthReportCacheTTL = time.Second
	// DefaultHealthReportCheckTimeout bounds each check a HealthReport read
	// or a scheduled round waits for when HealthCheckTimeout is 0, so a read
	// never hangs on a check that ignores its context.
	DefaultHealthReportCheckTimeout = 10 * time.Second
)

// HealthState is the health of a service or of a container in a
// HealthReport. Its zero value is HealthUnknown.
type HealthState int

const (
	// HealthUnknown is a service with no recorded result yet, or a report
	// whose critical services are not all known.
	HealthUnknown HealthState = iota
	// HealthHealthy is a service whose last check passed.
	HealthHealthy
	// HealthDegraded is an optional service whose last check failed.
	HealthDegraded
	// HealthUnhealthy is a critical service whose last check failed.
	HealthUnhealthy
)

// String returns the lower-case state name, or "HealthState(n)" for an
// unknown value.
func (s HealthState) String() string {
	switch s {
	case HealthUnknown:
		return "unknown"
	case HealthHealthy:
		return "healthy"
	case HealthDegraded:
		return "degraded"
	case HealthUnhealthy:
		return "unhealthy"
	default:
		return fmt.Sprintf("HealthState(%d)", int(s))
	}
}

// ServiceHealth is the entry of one health-checked service in a
// HealthReport.
type ServiceHealth struct {
	// Service names the service and its scope.
	Service ServiceDescription
	// Tags are the service's health tags, sorted.
	Tags []string
	// Optional reports whether a failure of the service degrades the report
	// instead of making it unhealthy.
	Optional bool
	// State is the state of the last recorded result.
	State HealthState
	// Err is the error of the last recorded result, nil when it passed.
	Err error
	// CheckedAt is when the last recorded check returned, on the injector's
	// clock; zero when none was recorded.
	CheckedAt time.Time
	// Duration is how long the last recorded check took.
	Duration time.Duration
	// PendingSince is when the check in flight started; zero when none is.
	PendingSince time.Time
}

// HealthReport is the health of the services a scope or a container covers.
type HealthReport struct {
	// State is the overall state of the covered services, then gated by the
	// container phase.
	State HealthState
	// Phase is the container phase at the read.
	Phase ContainerPhase
	// Monitored reports whether a HealthScheduler keeps the results fresh.
	Monitored bool
	// CheckedAt is the latest CheckedAt of the entries.
	CheckedAt time.Time
	// Services holds one entry per covered health check.
	Services []ServiceHealth
}

// stateOf returns the state of a check that returned result: healthy for
// nil, degraded for an optional service's error, unhealthy otherwise.
func stateOf(result error, optional bool) HealthState {
	switch {
	case result == nil:
		return HealthHealthy
	case optional:
		return HealthDegraded
	default:
		return HealthUnhealthy
	}
}

// overallHealthState summarizes services: unhealthy when any is unhealthy,
// else degraded when any is degraded, else unknown when a critical one has
// no result yet, else healthy, including when there is none.
func overallHealthState(services []ServiceHealth) HealthState {
	var degraded, unknown bool
	for _, service := range services {
		switch service.State {
		case HealthUnhealthy:
			return HealthUnhealthy
		case HealthDegraded:
			degraded = true
		case HealthUnknown:
			unknown = unknown || !service.Optional
		case HealthHealthy:
		}
	}

	switch {
	case degraded:
		return HealthDegraded
	case unknown:
		return HealthUnknown
	default:
		return HealthHealthy
	}
}

// HealthReport returns the health of the services visible from s: its own
// and its ancestors', the nearest scope winning for a name, and the sources
// of the exports s sees. It is HealthReportWithContext with
// context.Background().
func (s *Scope) HealthReport(opts ...HealthReportOption) HealthReport {
	return s.HealthReportWithContext(context.Background(), opts...)
}

// HealthReportWithContext returns the health of the services visible from
// s, one entry per underlying check: an alias or an export adds no entry of
// its own, a default that yielded is left out, and so are services that are
// not health checkers at the read (plain values, transient services, lazy
// services not built yet).
//
// A recorded result is reused for HealthReportCacheTTL; a service whose
// result is missing or older is checked during the read, sharing any check
// in flight with other callers, within ctx and the report bound:
// HealthCheckTimeout when positive, DefaultHealthReportCheckTimeout when 0,
// none when negative; HealthCheckGlobalTimeout does not apply. A check still
// running at the report bound is recorded as timed out then, and its later
// result is discarded; a ctx ending records nothing. Every completed check is
// recorded, whichever path ran it, so HealthCheck() and the single-service
// functions refresh the report too. A nil ctx means context.Background().
func (s *Scope) HealthReportWithContext(ctx context.Context, opts ...HealthReportOption) HealthReport {
	return s.rootScope.healthReport(ctx, newReportQuery(visibleCheckers(s), false, opts))
}

// HealthReport returns the health of every scope of the container,
// extension scopes included. It is HealthReportWithContext with
// context.Background().
func (s *RootScope) HealthReport(opts ...HealthReportOption) HealthReport {
	return s.HealthReportWithContext(context.Background(), opts...)
}

// HealthReportWithContext returns the health of every scope of the
// container; see [Scope.HealthReportWithContext].
func (s *RootScope) HealthReportWithContext(ctx context.Context, opts ...HealthReportOption) HealthReport {
	return s.healthReport(ctx, newReportQuery(containerCheckers(s), true, opts))
}

// HealthReportOption selects what a HealthReport read covers. Values come
// only from the constructors of this package, such as [MatchHealthTags]; the
// zero value is ignored.
type HealthReportOption struct {
	apply func(query *reportQuery)
}

// reportQuery is what a report read covers: targets, the services listed;
// prune, set when they list the whole container; tags, the tags matched,
// none meaning every service.
type reportQuery struct {
	targets     []healthTarget
	prune       bool
	tags        map[string]struct{}
	ignorePhase bool // IgnoreContainerPhase: no phase gate
}

// newReportQuery returns the query of a read of targets with opts.
func newReportQuery(targets []healthTarget, prune bool, opts []HealthReportOption) reportQuery {
	query := reportQuery{targets: targets, prune: prune, tags: map[string]struct{}{}}
	for _, opt := range opts {
		if opt.apply != nil {
			opt.apply(&query)
		}
	}

	return query
}

// covers reports whether the query covers tag: it has no tag filter, or its
// filter includes tag.
func (q reportQuery) covers(tag string) bool {
	if len(q.tags) == 0 {
		return true
	}

	_, ok := q.tags[tag]
	return ok
}

// matches reports whether a service with opts is covered by the query.
func (q reportQuery) matches(opts *serviceOptions) bool {
	if len(q.tags) == 0 {
		return true
	}

	tags, _ := healthClassification(opts)
	return slices.ContainsFunc(tags, func(tag string) bool {
		_, ok := q.tags[tag]
		return ok
	})
}

// IgnoreContainerPhase turns the container-phase gate off for a read: its
// State is then the overall state of the covered services in every phase
// but PhaseClosed, whose report stays empty and HealthUnknown.
func IgnoreContainerPhase() HealthReportOption {
	return HealthReportOption{apply: func(query *reportQuery) { query.ignorePhase = true }}
}

// MatchHealthTags limits a report, its overall state and the checks it runs
// on demand to the services carrying at least one of tags. Repeated options
// accumulate; with no tag, or no option, a report covers every health
// checker. A tag no service carries covers none, and such a report is
// HealthHealthy: use the HealthTag constants to avoid a mistyped tag.
func MatchHealthTags(tags ...string) HealthReportOption {
	return HealthReportOption{apply: func(query *reportQuery) {
		for _, tag := range tags {
			query.tags[tag] = struct{}{}
		}
	}}
}

// containerCheckers lists the services of every scope of the container,
// each under its own scope. Aliases and exports are left out, as their
// sources are listed where they are registered, and so are defaults that
// yielded.
func containerCheckers(s *RootScope) []healthTarget {
	targets := []healthTarget{}

	scopes := []*Scope{s.self}
	for len(scopes) > 0 {
		scope := scopes[0]
		scopes = scopes[1:]

		for _, entry := range scope.servicesSnapshot() {
			if _, ok := entry.service.(serviceWrapperAliasTarget); ok {
				continue
			}
			if _, ok := asExport(entry.service); ok {
				continue
			}
			if isYieldedDefaultIn(scope, entry.service) {
				continue
			}
			targets = append(targets, healthTarget{key: entry.name, name: entry.name, scope: scope, wrapper: entry.service})
		}

		scopes = append(scopes, scope.Children()...)
	}

	return targets
}

// reportCheck is a health checker a report covers.
type reportCheck struct {
	target healthTarget
	check  func(context.Context) checkOutcome
	waiter *callWaiter // this read's wait for its check, if it checked it
	// unstarted is set for a Starter that Start, Refresh or Scope.Start
	// would start but did not yet: it is listed unknown, not checked.
	unstarted bool
}

// healthReport builds the report of query, checking on demand the covered
// services whose recorded result is missing or stale. With query.prune, the
// records of services not in the container are dropped.
func (s *RootScope) healthReport(ctx context.Context, query reportQuery) HealthReport {
	if ctx == nil {
		ctx = context.Background()
	}

	report := HealthReport{Phase: s.Phase(), Services: []ServiceHealth{}}
	if report.Phase == PhaseClosed {
		return report
	}

	covered := s.coveredChecks(query.targets)
	if query.prune {
		s.health.keepOnly(coveredMembers(covered))
		s.deliverTransitions()
	}
	covered = slices.DeleteFunc(covered, func(c *reportCheck) bool { return !query.matches(wrapperOptions(c.target.wrapper)) })
	slices.SortStableFunc(covered, func(a, b *reportCheck) int { return compareHealthTargets(a.target, b.target) })

	if report.Phase != PhaseIdle {
		for _, c := range covered {
			c.unstarted = !eligibleForReport(s, c.target.wrapper)
		}
	}

	report.Monitored = s.health.monitored(report.Phase)
	switch {
	case report.Monitored:
		s.checkOnDemand(ctx, covered, s.health.neverChecked)
	case report.Phase == PhaseIdle || report.Phase == PhaseFailed || report.Phase == PhaseReady:
		ttl, now := s.opts.healthReportCacheTTL(), s.opts.Clock.Now()
		s.checkOnDemand(ctx, covered, func(service any) bool { return s.health.stale(service, now, ttl) })
	}

	report.Services = s.reportEntries(covered)
	report.State = overallHealthState(report.Services)
	if state, gated := phaseGate(report.Phase, query); gated {
		report.State = state
	}
	report.CheckedAt = latestCheck(report.Services)

	return report
}

// eligibleForReport reports whether a started container's reports and
// rounds check service: not while it is a Starter that Start, Refresh or
// Scope.Start would start and has not started yet.
func eligibleForReport(root *RootScope, service any) bool {
	wrapper, ok := service.(serviceWrapperAny)
	return !ok || !root.declaresStarter(wrapper) || root.isStarted(wrapper)
}

// coveredMembers returns the targets of covered, by wrapper.
func coveredMembers(covered []*reportCheck) map[any]healthTarget {
	members := make(map[any]healthTarget, len(covered))
	for _, c := range covered {
		members[c.target.wrapper] = c.target
	}

	return members
}

// checkOnDemand checks, within ctx and the report bound, each of covered
// that is eligible and that due accepts, sharing checks in flight.
func (s *RootScope) checkOnDemand(ctx context.Context, covered []*reportCheck, due func(service any) bool) {
	bound := reportBound(s.opts)

	for _, c := range covered {
		if !c.unstarted && due(c.target.wrapper) {
			c.waiter = s.joinOrStart(waitSpec{ctx: ctx, bound: bound, pooled: true, reportPath: true}, c.target, c.check)
		}
	}
}

// reportEntries waits for the checks covered started, then returns their
// entries: a check this read stopped waiting for shows the read's timeout.
func (s *RootScope) reportEntries(covered []*reportCheck) []ServiceHealth {
	results := make([]error, len(covered))
	for n, c := range covered {
		results[n] = s.health.wait(c.waiter)
	}

	s.health.mu.Lock()
	defer s.health.mu.Unlock()

	entries := make([]ServiceHealth, 0, len(covered))
	for n, c := range covered {
		entry := s.health.entryLocked(c.target)
		if c.unstarted {
			entry.State, entry.Err, entry.CheckedAt, entry.Duration = HealthUnknown, nil, time.Time{}, 0
		}
		if c.waiter != nil && !c.waiter.completed() {
			entry.State, entry.Err = stateOf(results[n], entry.Optional), results[n]
		}
		entries = append(entries, entry)
	}

	return entries
}

// phaseGate returns the state the container phase imposes on a report of
// query, and whether it imposes one: a report covering readiness is
// unhealthy while the container is starting, started, failed or closing, and
// one covering liveness is unhealthy once Start failed. IgnoreContainerPhase
// turns the gate off.
func phaseGate(phase ContainerPhase, query reportQuery) (HealthState, bool) {
	if query.ignorePhase {
		return HealthUnknown, false
	}

	switch {
	case query.covers(HealthTagReadiness) && slices.Contains([]ContainerPhase{PhaseStarting, PhaseStarted, PhaseFailed, PhaseClosing}, phase):
		return HealthUnhealthy, true
	case query.covers(HealthTagLiveness) && phase == PhaseFailed:
		return HealthUnhealthy, true
	default:
		return HealthUnknown, false
	}
}

// latestCheck returns the latest CheckedAt of entries.
func latestCheck(entries []ServiceHealth) time.Time {
	var latest time.Time
	for _, entry := range entries {
		if entry.CheckedAt.After(latest) {
			latest = entry.CheckedAt
		}
	}

	return latest
}

// coveredChecks returns the health checkers among targets, once per
// wrapper.
func (s *RootScope) coveredChecks(targets []healthTarget) []*reportCheck {
	covered := []*reportCheck{}
	seen := map[any]struct{}{}

	for _, target := range targets {
		if _, ok := seen[target.wrapper]; ok {
			continue
		}
		seen[target.wrapper] = struct{}{}

		if check := target.scope.wrapperHealthCheckFunc(target.name, target.wrapper); check != nil {
			covered = append(covered, &reportCheck{target: target, check: check})
		}
	}

	return covered
}

// reportBound returns the bound of a check a report read or a round waits
// for: HealthCheckTimeout, or DefaultHealthReportCheckTimeout when it is 0. A
// negative value means none.
func reportBound(opts *InjectorOpts) time.Duration {
	if opts.HealthCheckTimeout == 0 {
		return DefaultHealthReportCheckTimeout
	}

	return opts.HealthCheckTimeout
}

// healthReportCacheTTL returns HealthReportCacheTTL, or its default when 0.
func (o *InjectorOpts) healthReportCacheTTL() time.Duration {
	if o.HealthReportCacheTTL == 0 {
		return DefaultHealthReportCacheTTL
	}

	return o.HealthReportCacheTTL
}

// stale reports whether the recorded result of service is missing, or at
// least ttl old at now; always with a negative ttl.
func (m *healthMonitor) stale(service any, now time.Time, ttl time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	record, ok := m.records[service]
	return !ok || ttl < 0 || now.Sub(record.checkedAt) >= ttl
}

// entryLocked returns the report entry of target from its record and its
// check in flight. m.mu must be held.
func (m *healthMonitor) entryLocked(target healthTarget) ServiceHealth {
	record, recorded := m.records[target.wrapper]
	entry := healthEntry(target, record, recorded)
	if call, ok := m.inflight[target.wrapper]; ok {
		entry.PendingSince = call.queuedAt
	}

	return entry
}

// healthEntry returns the report entry of target with its record, when
// recorded; HealthUnknown otherwise.
func healthEntry(target healthTarget, record healthRecord, recorded bool) ServiceHealth {
	entry := ServiceHealth{Service: newServiceDescription(target.scope.id, target.scope.name, target.name)}
	entry.Tags, entry.Optional = healthClassification(wrapperOptions(target.wrapper))
	if recorded {
		entry.State, entry.Err, entry.CheckedAt, entry.Duration = record.state, record.err, record.checkedAt, record.duration
	}

	return entry
}

// wrapperOptions returns the options of a registration's wrapper, nil when
// it has none.
func wrapperOptions(wrapper any) *serviceOptions {
	if service, ok := wrapper.(serviceWrapperAny); ok {
		return service.getOptions()
	}

	return nil
}

// healthClassification returns the effective health tags of a registration
// with opts, sorted and owned by the caller, and whether it is optional: an
// unclassified checker carries HealthTagReadiness and is critical.
func healthClassification(opts *serviceOptions) (tags []string, optional bool) {
	if opts == nil || !opts.healthTagsSet {
		return []string{HealthTagReadiness}, opts != nil && opts.optionalHealth
	}

	tags = slices.Sorted(slices.Values(opts.healthTags))
	if tags == nil {
		tags = []string{}
	}

	return tags, opts.optionalHealth
}

// compareHealthTargets orders targets by the path of scope names from the
// root, shallower first, then by service name.
func compareHealthTargets(a, b healthTarget) int {
	pathA, pathB := scopeNamePath(a.scope), scopeNamePath(b.scope)

	return cmp.Or(
		cmp.Compare(len(pathA), len(pathB)),
		slices.Compare(pathA, pathB),
		strings.Compare(a.name, b.name),
	)
}

// scopeNamePath returns the names of the scopes from the root to scope.
func scopeNamePath(scope *Scope) []string {
	path := []string{}
	for ; scope != nil; scope = scope.parentScope {
		path = append(path, scope.name)
	}
	slices.Reverse(path)

	return path
}
