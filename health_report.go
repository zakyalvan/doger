package doger

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	// DefaultHealthReportCacheTTL is the HealthReportCacheTTL used when it
	// is 0.
	DefaultHealthReportCacheTTL = time.Second
	// DefaultHealthReportCheckTimeout bounds each health check, on every
	// path (HealthCheck(), the single-service functions, HealthReport reads
	// and scheduled rounds), when HealthCheckTimeout is 0, so no call hangs
	// on a check that ignores its context.
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

// healthStateNames are the names of the HealthState constants, by value.
var healthStateNames = []string{"unknown", "healthy", "degraded", "unhealthy"}

// String returns the lower-case state name, or "HealthState(n)" for an
// unknown value.
func (s HealthState) String() string {
	if name, ok := enumText(s, healthStateNames); ok {
		return name
	}

	return fmt.Sprintf("HealthState(%d)", int(s))
}

// MarshalText encodes the state as its String form, so JSON shows it by
// name. A value outside the declared constants fails to marshal, and so
// does a JSON encoding holding it: the library never reports one.
func (s HealthState) MarshalText() ([]byte, error) {
	name, ok := enumText(s, healthStateNames)
	if !ok {
		return nil, libErrorf("invalid health state %d", int(s))
	}

	return []byte(name), nil
}

// UnmarshalText decodes the String form of a state. Any other text fails
// and leaves s unchanged.
func (s *HealthState) UnmarshalText(text []byte) error {
	value, ok := enumValue[HealthState](text, healthStateNames)
	if !ok {
		return libErrorf("unknown health state %q", text)
	}
	*s = value

	return nil
}

// enumText returns the name of value among names, indexed by value, and
// false for a value outside them.
func enumText[E ~int](value E, names []string) (string, bool) {
	if value < 0 || int(value) >= len(names) {
		return "", false
	}

	return names[value], true
}

// enumValue returns the value whose name among names is text, and false
// when none is.
func enumValue[E ~int](text []byte, names []string) (E, bool) {
	index := slices.Index(names, string(text))
	if index < 0 {
		return 0, false
	}

	return E(index), true
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
	// Duration is how long the last recorded check ran, from when it started
	// running to its result, or to its timeout when one was recorded; a wait
	// for a slot of InjectorOpts.HealthCheckParallelism is not included.
	Duration time.Duration
	// PendingSince is when the check in flight was requested, including any
	// wait for a slot of InjectorOpts.HealthCheckParallelism; zero when no
	// check is in flight.
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
	// A monitored read returns the recorded results and checks only the
	// covered services never checked yet; one whose first check is in
	// flight, started by a round or another read, is joined: the read waits
	// for it within its context and the report bound.
	Monitored bool
	// CheckedAt is the latest CheckedAt of the entries.
	CheckedAt time.Time
	// Services holds one entry per covered health check.
	Services []ServiceHealth
}

// serviceHealthJSON is the JSON form of a ServiceHealth.
type serviceHealthJSON struct {
	Service      ServiceDescription `json:"service"`
	Tags         []string           `json:"tags"`
	Optional     bool               `json:"optional"`
	State        HealthState        `json:"state"`
	Error        *string            `json:"error,omitempty"` // nil only when the check passed
	CheckedAt    *time.Time         `json:"checkedAt,omitempty"`
	PendingSince *time.Time         `json:"pendingSince,omitempty"`
	Duration     string             `json:"duration"`
}

// MarshalJSON encodes the entry with lower-camel keys: the state by name,
// the error as its message (omitted only when Err is nil, so an error with an
// empty message keeps the key), CheckedAt and PendingSince in RFC 3339
// (omitted when zero) and Duration as a Go duration string. It fails for a
// State outside the declared constants, which only an entry built by hand
// can hold.
func (h ServiceHealth) MarshalJSON() ([]byte, error) {
	entry := serviceHealthJSON{
		Service:      h.Service,
		Tags:         h.Tags,
		Optional:     h.Optional,
		State:        h.State,
		CheckedAt:    nonZeroTime(h.CheckedAt),
		PendingSince: nonZeroTime(h.PendingSince),
		Duration:     h.Duration.String(),
	}
	if entry.Tags == nil {
		entry.Tags = []string{}
	}
	if h.Err != nil {
		message := h.Err.Error()
		entry.Error = &message
	}

	return json.Marshal(entry)
}

// healthReportJSON is the JSON form of a HealthReport.
type healthReportJSON struct {
	State     HealthState     `json:"state"`
	Phase     ContainerPhase  `json:"phase"`
	Monitored bool            `json:"monitored"`
	CheckedAt *time.Time      `json:"checkedAt,omitempty"`
	Services  []ServiceHealth `json:"services"`
}

// MarshalJSON encodes the report for monitoring, with lower-camel keys: the
// state and phase by name, CheckedAt in RFC 3339 (omitted when zero) and
// services as an array, [] when there is none; see ServiceHealth.MarshalJSON
// for each entry. It never fails for a report the library returns; a report
// built by hand fails when its State, its Phase or an entry's State is
// outside the declared constants.
func (r HealthReport) MarshalJSON() ([]byte, error) {
	report := healthReportJSON{
		State:     r.State,
		Phase:     r.Phase,
		Monitored: r.Monitored,
		CheckedAt: nonZeroTime(r.CheckedAt),
		Services:  r.Services,
	}
	if report.Services == nil {
		report.Services = []ServiceHealth{}
	}

	return json.Marshal(report)
}

// nonZeroTime returns &t, or nil for the zero time, so JSON omits it.
func nonZeroTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}

	return &t
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
// On a container whose HealthScheduler keeps the results fresh (see
// HealthReport.Monitored), a recorded result is always reused and only a
// service with no recorded result is checked during the read: its first
// check, when in flight from a round or another read, is joined. Otherwise
// a recorded result is reused for HealthReportCacheTTL and a service whose
// result is missing or older is checked during the read. Either way the read
// shares any check in flight with other callers, within ctx and the report
// bound:
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
// from the constructors of this package, such as [MatchHealthTags] and
// [IgnoreContainerPhase], and a consumer composes them in its own helpers,
// such as func liveness() HealthReportOption { return
// MatchHealthTags(HealthTagLiveness) }. A nil option is ignored.
type HealthReportOption func(query *reportQuery)

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
	applyOptions(&query, opts)

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
	return func(query *reportQuery) { query.ignorePhase = true }
}

// MatchHealthTags limits a report, its overall state and the checks it runs
// on demand to the services carrying at least one of tags. Repeated options
// accumulate; with no tag, or no option, a report covers every health
// checker. A tag no service carries covers none, and such a report is
// HealthHealthy: use the HealthTag constants to avoid a mistyped tag.
func MatchHealthTags(tags ...string) HealthReportOption {
	return func(query *reportQuery) {
		for _, tag := range tags {
			query.tags[tag] = struct{}{}
		}
	}
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
	sortReportChecks(covered)

	if report.Phase != PhaseIdle {
		for _, c := range covered {
			c.unstarted = !eligibleForReport(s, c.target.wrapper)
		}
	}

	report.Monitored = s.health.monitored(report.Phase)
	if needsWait := s.needsWait(report.Monitored, report.Phase); needsWait != nil {
		s.checkOnDemand(ctx, covered, needsWait)
	}

	report.Services = s.reportEntries(covered)
	report.State = overallHealthState(report.Services)
	if state, gated := phaseGate(report.Phase, query); gated {
		report.State = state
	}
	report.CheckedAt = latestCheck(report.Services)

	return report
}

// needsWait returns the predicate selecting the services a read in phase
// checks, or waits for when their check is in flight: on a monitored
// container those never checked, otherwise those whose result is missing or
// older than HealthReportCacheTTL. It returns nil in a phase whose reads
// check nothing.
func (s *RootScope) needsWait(monitored bool, phase ContainerPhase) func(service any) bool {
	switch {
	case monitored:
		return s.health.neverChecked
	case phase == PhaseIdle || phase == PhaseFailed || phase == PhaseReady:
		ttl, now := s.opts.healthReportCacheTTL(), s.opts.Clock.Now()
		return func(service any) bool { return s.health.stale(service, now, ttl) }
	default:
		return nil
	}
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
	bound := serviceBound(s.opts, boundScopeWide)

	for _, c := range covered {
		if !c.unstarted && due(c.target.wrapper) {
			s.startReportCheck(ctx, bound, c)
		}
	}
}

// startReportCheck joins the check in flight of c for a report read or a
// round, or starts one, within ctx and bound, unless its scope no longer
// holds it: a service that left once listed is not checked.
func (s *RootScope) startReportCheck(ctx context.Context, bound time.Duration, c *reportCheck) {
	if !c.target.stillRegistered() {
		return
	}

	c.waiter = s.joinOrStart(waitSpec{ctx: ctx, bound: bound, pooled: true, reportPath: true}, c.target, c.check)
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

// boundPath is the path a health check is waited for on, which decides its
// per-service bound (see serviceBound).
type boundPath int

const (
	// boundScopeWide: HealthCheck(), report reads and scheduled rounds
	boundScopeWide boundPath = iota
	// boundSingle: HealthCheck[T], HealthCheckNamed and their WithContext forms
	boundSingle
)

// serviceBound returns the per-service bound of a health check waited for on
// path: HealthCheckSingleTimeout for a single-service path when it is not 0,
// otherwise HealthCheckTimeout, or DefaultHealthReportCheckTimeout when it is
// 0. A negative value means none.
func serviceBound(opts *InjectorOpts, path boundPath) time.Duration {
	if path == boundSingle && opts.HealthCheckSingleTimeout != 0 {
		return opts.HealthCheckSingleTimeout
	}
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
	optional = optionalHealth(opts)
	if opts == nil || !opts.healthTagsSet {
		return []string{HealthTagReadiness}, optional
	}

	tags = slices.Sorted(slices.Values(opts.healthTags))
	if tags == nil {
		tags = []string{}
	}

	return tags, optional
}

// optionalHealth is the optional flag of healthClassification, without
// building the tags.
func optionalHealth(opts *serviceOptions) bool {
	return opts != nil && opts.optionalHealth
}

// sortReportChecks orders covered by the path of scope names from the root,
// shallower first, then by service name. Each covered scope's path is
// computed once, not per comparison.
func sortReportChecks(covered []*reportCheck) {
	paths := map[*Scope][]string{}
	for _, c := range covered {
		if _, ok := paths[c.target.scope]; !ok {
			paths[c.target.scope] = scopeNamePath(c.target.scope)
		}
	}

	slices.SortStableFunc(covered, func(a, b *reportCheck) int {
		pathA, pathB := paths[a.target.scope], paths[b.target.scope]
		return cmp.Or(
			cmp.Compare(len(pathA), len(pathB)),
			slices.Compare(pathA, pathB),
			strings.Compare(a.target.name, b.target.name),
		)
	})
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
