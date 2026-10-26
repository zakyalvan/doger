package doger

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
)

// startPhase is the start state of a root container.
type startPhase int

const (
	startIdle startPhase = iota
	startStarting
	startStarted
	startReady // every Started hook returned
	startFailed
)

// Start builds and starts the container with context.Background(); see
// [RootScope.StartWithContext].
func (s *RootScope) Start() error {
	return s.StartWithContext(context.Background())
}

// StartWithContext starts the container: it builds every candidate service,
// then starts the Starters.
//
// Candidates are the non-transient, non-alias services of the root scope and
// of every descendant scope that exists when Start takes its snapshot, whose
// registered type implements a Starter variant (unless
// [InjectorOpts.DisableStarterAutoDetection] is set) or that were registered
// with [AsStarter]. Services registered after Start, and scopes created after
// it, are not started until [RootScope.Refresh] or [Scope.Start].
//
// A container starts at most once: any later call returns an error matching
// [ErrAlreadyStarted], even when the first one failed, and a call after root
// Shutdown began returns an error matching [ErrStartAfterShutdown]. A clone
// starts independently of its source; clones share eager instances, so an
// eager Starter is started again by the clone.
//
// Context: Start derives its own cancellable context from ctx (a nil ctx is
// treated as context.Background()). Context-aware Start(ctx) methods, kind
// handlers and runners receive that derived context, which is cancelled when
// Start returns: do not retain it for long-running work; start a goroutine
// with its own context and stop it from a Shutdowner method instead. Start
// checks the context before each candidate build, each handler call and each
// Starter, and once at the end; a cancelled or expired ctx stops Start with
// an error wrapping ctx.Err().
//
// On the first failure (a build error, a Start error, or a panic in a Start
// method, recovered as an error), Start stops and returns a [*StartError]
// that names the service, its scope and the failed step, and wraps the
// cause, so errors.Is and errors.As reach it. Starters not reached yet are
// not started. Nothing is rolled back: cleanup is the caller's job,
// typically by calling Shutdown, so services must tolerate Shutdown without
// a prior Start. A panic in a per-service hook is not recovered as an error:
// Start runs the Failed hooks, then re-raises it.
//
// Shutdown during Start: a root Shutdown that begins while Start runs
// cancels the Start-time context with cause [ErrStartAfterShutdown] and waits
// for Start to return, bounded by its own context. Start stops before its
// next Starter or handler and returns an error matching
// ErrStartAfterShutdown, and also ctx's cause when ctx was cancelled before
// Shutdown began. A Starter that ignores its context and finishes
// after Shutdown stopped waiting is not rolled back: honour ctx.
//
// Never call Shutdown synchronously from code that runs during Start (a
// Starter, a kind handler, a runner, a provider or a hook): Shutdown waits
// for Start, which waits for that code, so it deadlocks unless Shutdown's
// context has a deadline. Return an error instead. Calling Start from such
// code returns [ErrAlreadyStarted] immediately.
//
// Only [RootScope] methods are container-level: they fire the container
// hooks and own the one-shot Start. [Scope.Start] starts a scope subtree
// incrementally, as [RootScope.Refresh] does, once Start succeeded; on the
// root's own *Scope (for example [Component.Scope] of a root service) it is
// Refresh. A child scope's Shutdown waits for a run
// that is starting its services (see [Scope.ShutdownWithContext]); a
// Shutdown reached through the root's own *Scope bypasses this
// coordination.
func (s *RootScope) StartWithContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	startCtx, err := s.beginStart(ctx)
	if err != nil {
		return err
	}

	recovered, panicked, err := s.runStart(startCtx)

	// pointAfterStart: startDone is closed, so code here may call Shutdown().
	if panicked {
		s.failAndRepanic(ctx, recovered, err)
	}
	var first hookPanic
	s.dispatchAfterStart(ctx, err, &first)
	s.dispatchFirstPanic(first)

	return err
}

// failAndRepanic runs the Failed hooks with err for a panic that escaped the
// start pipeline, then re-raises it. A Failed hook panic meanwhile goes to
// HookContainerPanic when set and is logged otherwise, so that the pipeline
// panic is the one re-raised.
func (s *RootScope) failAndRepanic(ctx context.Context, recovered any, err error) {
	var first hookPanic
	s.dispatchAfterStart(ctx, err, &first)

	switch {
	case !first.panicked:
	case s.opts.HookContainerPanic != nil:
		s.opts.HookContainerPanic(s, first.point, first.value)
	default:
		s.logHookPanic(first)
	}

	panic(recovered)
}

// dispatchAfterStart fires the pointAfterStart hooks from Start's local
// outcome: Started, then Ready, on success; Failed with err otherwise.
func (s *RootScope) dispatchAfterStart(ctx context.Context, err error, first *hookPanic) {
	if err != nil {
		runObserverHooksWith(s, "container", "failed", s.opts.HookContainerFailed, first, func(hook func(context.Context, *RootScope, error)) {
			hook(ctx, s, err)
		})
		return
	}

	s.runObserverHooks(ctx, "started", s.opts.HookContainerStarted, first)
	s.markReady()
	s.runObserverHooks(ctx, "ready", s.opts.HookContainerReady, first)
}

// hookPanic is the first observer hook panic of one Start or Shutdown.
type hookPanic struct {
	subject  string // "container" or "scope"
	point    string // as HookContainerPanic receives it
	value    any
	panicked bool
}

// callObserver calls an observer hook of subject, recovering its panic: the
// first one of the operation is kept in first, later ones are logged.
func (s *RootScope) callObserver(first *hookPanic, subject, point string, call func()) {
	if recovered, panicked := callHookSafely(call); panicked {
		s.keepFirstPanic(first, hookPanic{subject: subject, point: point, value: recovered, panicked: true})
	}
}

// keepFirstPanic keeps p in first when first holds no panic yet, and logs
// it otherwise.
func (s *RootScope) keepFirstPanic(first *hookPanic, p hookPanic) {
	if !p.panicked {
		return
	}

	if first.panicked {
		s.logHookPanic(p)
		return
	}
	*first = p
}

// logHookPanic logs an observer hook panic that is not dispatched.
func (s *RootScope) logHookPanic(p hookPanic) {
	if p.subject == "scope" {
		// scope points name their subject already: "scope-closing"
		s.opts.Logf("DI: %s hook panicked: %v", p.point, p.value)
		return
	}

	s.opts.Logf("DI: container %s hook panicked: %v", p.point, p.value)
}

// dispatchFirstPanic dispatches the first observer hook panic of an
// operation, if any. Call it once the operation is complete.
func (s *RootScope) dispatchFirstPanic(first hookPanic) {
	if first.panicked {
		s.dispatchHookPanic(first.point, first.value)
	}
}

// callHookSafely calls fn and recovers its panic.
func callHookSafely(fn func()) (recovered any, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			recovered, panicked = r, true
		}
	}()

	fn()

	return nil, false
}

// dispatchHookPanic passes an observer hook panic to HookContainerPanic, or
// re-panics with the recovered value when it is nil.
func (s *RootScope) dispatchHookPanic(point string, value any) {
	if s.opts.HookContainerPanic == nil {
		panic(value)
	}

	s.opts.HookContainerPanic(s, point, value)
}

// runObserverHooks runs container hooks in order, on a copy of the list,
// skipping nil entries.
func (s *RootScope) runObserverHooks(ctx context.Context, point string, hooks []func(context.Context, *RootScope), first *hookPanic) {
	runObserverHooksWith(s, "container", point, hooks, first, func(hook func(context.Context, *RootScope)) { hook(ctx, s) })
}

// observerHook is the type of an observer hook: a container or scope hook
// without a veto.
type observerHook interface {
	~func(context.Context, *RootScope) |
		~func(context.Context, *Scope) |
		~func(context.Context, *RootScope, error) |
		~func(context.Context, *RootScope, *ShutdownReport)
}

// runObserverHooksWith calls each non-nil observer hook of subject
// ("container" or "scope") through call, in order, on a copy of the list,
// recovering their panics into first under point.
func runObserverHooksWith[H observerHook](root *RootScope, subject, point string, hooks []H, first *hookPanic, call func(hook H)) {
	for _, hook := range slices.Clone(hooks) {
		if hook != nil {
			root.callObserver(first, subject, point, func() { call(hook) })
		}
	}
}

// markReady moves the start phase from started to ready.
func (s *RootScope) markReady() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	if s.startPhase == startStarted {
		s.startPhase = startReady
	}
}

// beginStart moves the start phase from idle to starting.
// It returns the Start-time context, derived from ctx and cancelled when
// Start returns.
func (s *RootScope) beginStart(ctx context.Context) (context.Context, error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	if s.closePhase != closeOpen {
		return nil, fmt.Errorf("DI: cannot start: %w", ErrStartAfterShutdown)
	}
	if s.startPhase != startIdle {
		return nil, ErrAlreadyStarted
	}

	startCtx, startCancel := context.WithCancelCause(ctx)
	s.startPhase = startStarting
	s.startCancel = startCancel
	s.startDone = make(chan struct{})

	return startCtx, nil
}

// runStart runs the pipeline. A panic that escapes it, such as one from a
// per-service hook, is recovered, and err is then the error the Failed
// hooks receive. Its deferred finishStart runs either way, so the lifecycle
// state is always released and the outcome carries the abort cause.
func (s *RootScope) runStart(startCtx context.Context) (recovered any, panicked bool, err error) {
	panicked = true
	defer func() {
		if panicked {
			recovered = recover()
			err = recoverAsError(recovered)
		}
		s.finishStart(startCtx, &err)
	}()

	s.pipelineMu.Lock()
	defer s.pipelineMu.Unlock()

	err = s.runStartPipeline(startCtx)
	panicked = false

	return nil, false, err
}

// finishStart records the outcome of Start, ends the Start-time context and
// releases its waiters. Once Shutdown began, the outcome is abortOutcome's.
func (s *RootScope) finishStart(startCtx context.Context, err *error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	if s.closePhase != closeOpen {
		*err = abortOutcome(startCtx, *err)
	}

	if *err != nil {
		s.startPhase = startFailed
	} else {
		s.startPhase = startStarted
	}
	s.startCancel(nil)
	close(s.startDone)
	s.runScopes = nil
}

// abortOutcome is the outcome of a run that Shutdown began during: a run
// that succeeded after its last abort check is reported as aborted, and a
// failure also carries the abort cause unless it already does. The abort
// cause always matches ErrStartAfterShutdown, even when the caller cancelled
// first. lifecycleMu must be held, so that beginClose has set the cause.
func abortOutcome(ctx context.Context, err error) error {
	cause := shutdownCause(ctx)
	switch {
	case err == nil:
		return fmt.Errorf("DI: start aborted: %w", cause)
	case errors.Is(err, ErrStartAfterShutdown):
		return err
	case errors.Is(err, context.Cause(ctx)):
		// carries the caller's cause already
		return fmt.Errorf("%w (start aborted: %w)", err, ErrStartAfterShutdown)
	default:
		return fmt.Errorf("%w (start aborted: %w)", err, cause)
	}
}

// shutdownCause returns the cause of the run's cancellation, made to match
// ErrStartAfterShutdown when the caller cancelled before Shutdown did.
func shutdownCause(ctx context.Context) error {
	cause := context.Cause(ctx)
	if errors.Is(cause, ErrStartAfterShutdown) {
		return cause
	}

	return fmt.Errorf("%w: %w", ErrStartAfterShutdown, cause)
}

// startCandidate is a service discovered by Start.
type startCandidate struct {
	placement
	scope    *Scope
	service  serviceWrapperAny
	starter  bool
	kinds    []kindBinding
	instance any
}

func (s *RootScope) runStartPipeline(startCtx context.Context) error {
	// pointStarting
	if err := s.runStartingHooks(startCtx); err != nil {
		return err
	}

	if err := checkAbort(startCtx); err != nil {
		return err
	}

	_, err := s.runPipeline(startCtx, []*Scope{s.self})
	return err
}

// runPipeline builds, handles and starts what is not started or handled yet
// in the subtrees of roots, with the abort checks of start-lifecycle D7.
// Start, Refresh and Scope.Start share it. closing reports that every root
// was shutting down, or under a scope that was, when the candidates were
// claimed, so that nothing of them was started.
func (s *RootScope) runPipeline(ctx context.Context, roots []*Scope) (closing bool, err error) {
	candidates, closing := s.claimCandidates(roots, s.discoverCandidates(roots))

	if err := buildCandidates(ctx, candidates); err != nil {
		return closing, err
	}

	if err := s.runStage(ctx, StageBeforeStart, candidates); err != nil {
		return closing, err
	}

	if err := s.checkCycles(candidates); err != nil {
		return closing, withAbortCause(ctx, err)
	}

	if err := s.startStarters(ctx, s.orderByDependencies(starterCandidates(candidates))); err != nil {
		return closing, err
	}

	// pointStartersUp
	if err := s.runStage(ctx, StageAfterStart, candidates); err != nil {
		return closing, err
	}

	return closing, checkAbort(ctx)
}

// runStartingHooks runs the container Starting hooks in order. The first
// error or panic is a veto: the remaining hooks do not run and Start fails
// with it.
func (s *RootScope) runStartingHooks(startCtx context.Context) error {
	if err := runVetoHooks(startCtx, s.opts.HookContainerStarting, s, startingHookPanicError("container")); err != nil {
		return withAbortCause(startCtx, fmt.Errorf("DI: container start aborted: %w", err))
	}

	return nil
}

// runVetoHooks calls each non-nil hook with arg, in order. The first error,
// or a panic turned into an error by panicError, stops the loop and is
// returned.
func runVetoHooks[T any](ctx context.Context, hooks []func(context.Context, T) error, arg T, panicError func(recovered any) error) error {
	for _, hook := range slices.Clone(hooks) {
		if hook == nil {
			continue
		}

		var err error
		if recovered, panicked := callHookSafely(func() { err = hook(ctx, arg) }); panicked {
			err = panicError(recovered)
		}
		if err != nil {
			return err
		}
	}

	return nil
}

// startingHookPanicError returns how a panic in a Starting hook of subject
// ("container" or "scope") becomes a veto error. An error value is wrapped,
// so errors.Is and errors.As reach it.
func startingHookPanicError(subject string) func(recovered any) error {
	return func(recovered any) error {
		if err, ok := recovered.(error); ok {
			return fmt.Errorf("DI: %s starting hook panicked: %w", subject, err)
		}

		return fmt.Errorf("DI: %s starting hook panicked: %v", subject, recovered)
	}
}

func (c *startCandidate) desc() ServiceDescription {
	return newServiceDescription(c.scope.ID(), c.scope.Name(), c.name)
}

// discoverCandidates snapshots the subtrees of roots breadth-first and
// returns the services the pipeline must build, ordered by absolute scope
// depth then registration order. Starters already started are skipped.
func (s *RootScope) discoverCandidates(roots []*Scope) []*startCandidate {
	candidates := []*startCandidate{}

	type level struct {
		scope *Scope
		depth int
	}
	queue := make([]level, 0, len(roots))
	for _, root := range roots {
		queue = append(queue, level{scope: root, depth: scopeDepth(root)})
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		current.scope.serviceForEach(func(name string, scope *Scope, serviceAny any) bool {
			if candidate := s.newStartCandidate(scope, name, current.depth, serviceAny); candidate != nil {
				candidates = append(candidates, candidate)
			}
			return true
		})

		for _, child := range current.scope.Children() {
			queue = append(queue, level{scope: child, depth: current.depth + 1})
		}
	}

	slices.SortFunc(candidates, func(a, b *startCandidate) int {
		return a.compare(b.placement)
	})

	return candidates
}

// claimCandidates drops the candidates of scopes that are shutting down, or
// whose ancestor is, and those their scope no longer holds (a shutdown that
// ran entirely after discovery), and records the scopes of the others, so
// that a child scope shutdown waits for this run (see
// Scope.ShutdownWithContext). closing reports that every root was shutting
// down, or under a scope that was.
func (s *RootScope) claimCandidates(roots []*Scope, candidates []*startCandidate) (claimed []*startCandidate, closing bool) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	// a shutdown marks its scope closing under lifecycleMu before it removes
	// services, so a candidate here is closing, still held, or removed
	candidates = slices.DeleteFunc(candidates, func(c *startCandidate) bool {
		return closingInLine(c.scope) || !stillRegistered(c)
	})
	s.runScopes = make(map[*Scope]struct{}, len(candidates))
	for _, candidate := range candidates {
		s.runScopes[candidate.scope] = struct{}{}
	}

	return candidates, !slices.ContainsFunc(roots, func(root *Scope) bool { return !closingInLine(root) })
}

// closingInLine reports whether scope or one of its ancestors is shutting
// down. lifecycleMu must be held.
func closingInLine(scope *Scope) bool {
	for ; scope != nil; scope = scope.parentScope {
		if scope.closing != nil && scope.closing.count > 0 {
			return true
		}
	}

	return false
}

// newStartCandidate returns the candidate for a registered service, or nil
// when Start has nothing to do with it.
func (s *RootScope) newStartCandidate(scope *Scope, name string, depth int, serviceAny any) *startCandidate {
	service, ok := serviceAny.(serviceWrapperAny)
	if !ok {
		return nil
	}

	serviceType := service.getServiceType()
	if serviceType == ServiceTypeTransient || serviceType == ServiceTypeAlias {
		return nil
	}

	opts := service.getOptions()
	starter := ((opts != nil && opts.starter) ||
		(!s.opts.DisableStarterAutoDetection && typeIsStarter(service.getReflectType()))) &&
		!s.isStarted(service)
	var kinds []kindBinding
	if opts != nil {
		kinds = opts.kinds
	}
	if s.opts.EnableRunnerAutoDetection && typeCanCastToType(service.getReflectType(), runnableKind.target()) &&
		!slices.ContainsFunc(kinds, func(b kindBinding) bool { return b.id == runnableKind.id }) {
		// implicit binding, so a runner opted in explicitly still runs once
		kinds = append(slices.Clone(kinds), kindBinding{id: runnableKind.id, meta: struct{}{}, target: runnableKind.target()})
	}
	// a component is handled at most once per kind per container
	kinds = slices.DeleteFunc(slices.Clone(kinds), func(b kindBinding) bool { return s.isHandled(service, b.id) })
	if !starter && len(kinds) == 0 {
		return nil
	}

	return &startCandidate{
		name:     name,
		depth:    depth,
		regIndex: service.getRegistrationIndex(),
		scope:    scope,
		service:  service,
		starter:  starter,
		kinds:    kinds,
	}
}

// buildCandidates invokes every candidate, keeping its instance.
func buildCandidates(startCtx context.Context, candidates []*startCandidate) error {
	for _, candidate := range candidates {
		if err := checkAbort(startCtx); err != nil {
			return err
		}

		instance, err := invokeAnyByName(candidate.scope, candidate.name)
		if err != nil {
			err = newStartError(StartOpBuild, candidate.scope, candidate.name, "", err)
			return withAbortCause(startCtx, err)
		}
		candidate.instance = instance

		for _, binding := range candidate.kinds {
			if !binding.deferred {
				continue
			}
			if instance == nil || !reflect.TypeOf(instance).Implements(binding.target) {
				err := fmt.Errorf("DI: service `%s` does not implement `%s` required by kind `%s`", candidate.name, binding.target.String(), binding.id.name)
				return withAbortCause(startCtx, newStartError(StartOpBuild, candidate.scope, candidate.name, "", err))
			}
		}
	}

	return nil
}

// starterCandidates returns the Starter candidates, keeping their order.
func starterCandidates(candidates []*startCandidate) []*startCandidate {
	starters := make([]*startCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.starter {
			starters = append(starters, candidate)
		}
	}

	return starters
}

// startStarters starts the Starter candidates in order, stopping at the
// first error. With StartParallelism n > 1, independent Starters start
// concurrently.
func (s *RootScope) startStarters(startCtx context.Context, candidates []*startCandidate) error {
	// more workers than Starters is pointless; clamping keeps the int
	// conversion in range for huge values such as ^uint(0)
	if n := min(s.opts.StartParallelism, uint(len(candidates))); n > 1 {
		return s.startStartersParallel(startCtx, candidates, int(n))
	}

	for _, candidate := range candidates {
		if err := checkAbort(startCtx); err != nil {
			return err
		}

		if err := s.startCandidate(startCtx, candidate); err != nil {
			return err
		}
	}

	return nil
}

// startStartersParallel starts up to n Starters at a time, each once every
// Starter it depends on has started, picking the ready one with the smallest
// scope depth, then registration index. The first failure stops scheduling;
// running Starters are awaited before it is returned. A panic in a
// per-service hook stops scheduling too, and is re-raised on the calling
// goroutine once the running Starters have returned. Every goroutine has
// returned when it returns.
func (s *RootScope) startStartersParallel(startCtx context.Context, candidates []*startCandidate, n int) error {
	requires := s.dependencyRequirements(candidates)
	pending := slices.Clone(candidates)
	slices.SortFunc(pending, func(a, b *startCandidate) int {
		return a.compare(b.placement)
	})
	started := map[ServiceDescription]bool{}

	type outcome struct {
		candidate *startCandidate
		err       error
		panicked  bool
		recovered any
	}
	outcomes := make(chan outcome)
	running := 0
	var firstErr error
	var firstPanic *outcome

	for {
		for firstErr == nil && firstPanic == nil && running < n {
			ready := slices.IndexFunc(pending, func(c *startCandidate) bool {
				return !slices.ContainsFunc(requires[c.desc()], func(d ServiceDescription) bool { return !started[d] })
			})
			if ready < 0 {
				break
			}
			if err := checkAbort(startCtx); err != nil {
				firstErr = err
				break
			}

			candidate := pending[ready]
			pending = slices.Delete(pending, ready, ready+1)
			running++
			go func() {
				o := outcome{candidate: candidate, panicked: true}
				defer func() {
					if o.panicked {
						o.recovered = recover()
					}
					outcomes <- o
				}()
				o.err = s.startCandidate(startCtx, candidate)
				o.panicked = false
			}()
		}

		if running == 0 {
			if firstPanic != nil {
				panic(firstPanic.recovered)
			}
			return firstErr
		}

		o := <-outcomes
		running--
		if o.panicked {
			if firstPanic == nil {
				firstPanic = &o
			}
			continue
		}
		if o.err != nil && firstErr == nil {
			firstErr = o.err
		}
		if o.err == nil {
			started[o.candidate.desc()] = true
		}
	}
}

// startCandidate starts one Starter candidate with its hooks.
func (s *RootScope) startCandidate(startCtx context.Context, candidate *startCandidate) error {
	if !instanceIsStarter(candidate.instance) {
		startErr := newStartError(StartOpStart, candidate.scope, candidate.name, "", fmt.Errorf("DI: service `%s` was opted in with AsStarter but does not implement any Starter interface", candidate.name))
		startErr.unprefixed = true // the cause names the service already
		return startErr
	}

	s.opts.onBeforeStart(candidate.scope, candidate.name)
	err := startInstance(startCtx, candidate.instance)
	s.opts.onAfterStart(candidate.scope, candidate.name, err)
	if err != nil {
		err = newStartError(StartOpStart, candidate.scope, candidate.name, "", err)
		return withAbortCause(startCtx, err)
	}
	// a service shut down meanwhile (a bounded scope shutdown gave up
	// waiting) stays out of the bookkeeping
	if stillRegistered(candidate) {
		s.markStarted(candidate.service)
	}

	return nil
}

// stillRegistered reports whether the candidate's scope still holds its
// wrapper.
func stillRegistered(candidate *startCandidate) bool {
	candidate.scope.mu.RLock()
	defer candidate.scope.mu.RUnlock()

	return candidate.scope.services[candidate.name] == candidate.service
}

// startInstance calls the Start method of the instance's Starter variant.
// The type switch mirrors the one of the shutdown path.
func startInstance(ctx context.Context, instance any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = recoverAsError(r)
		}
	}()

	switch instance := instance.(type) {
	case StarterWithContextAndError:
		return instance.Start(ctx)
	case StarterWithError:
		return instance.Start()
	case StarterWithContext:
		instance.Start(ctx)
	case Starter:
		instance.Start()
	}

	return nil
}

// instanceIsStarter reports whether instance implements a Starter variant.
func instanceIsStarter(instance any) bool {
	switch instance.(type) {
	case StarterWithContextAndError, StarterWithError, StarterWithContext, Starter:
		return true
	}

	return false
}

// recoverAsError turns a recovered panic value into an error. An error value
// is wrapped, so errors.Is and errors.As reach it.
func recoverAsError(r any) error {
	if err, ok := r.(error); ok {
		return fmt.Errorf("DI: recovered panic: %w", err)
	}

	return fmt.Errorf("DI: recovered panic: %v", r)
}

// checkAbort returns an error wrapping the cause when the Start-time context
// is done: the caller's ctx.Err(), or ErrStartAfterShutdown when Shutdown
// cancelled Start.
func checkAbort(startCtx context.Context) error {
	if startCtx.Err() != nil {
		return fmt.Errorf("DI: start aborted: %w", context.Cause(startCtx))
	}

	return nil
}

// withAbortCause additionally wraps the abort cause into a step error when
// the step failed while the Start-time context was done.
func withAbortCause(startCtx context.Context, err error) error {
	if startCtx.Err() != nil {
		return fmt.Errorf("%w (start aborted: %w)", err, context.Cause(startCtx))
	}

	return err
}

// runStage runs the handlers registered for stage, kind by kind in
// order of first Handle, each over its components in dependency order.
func (s *RootScope) runStage(startCtx context.Context, stage Stage, candidates []*startCandidate) error {
	s.kindsMu.Lock()
	entries := s.kinds // never written in place
	s.kindsMu.Unlock()

	for _, entry := range entries {
		if entry.stage != stage {
			continue
		}

		components := []*startCandidate{}
		metas := map[*startCandidate]any{}
		for _, candidate := range candidates {
			for _, binding := range candidate.kinds {
				if binding.id == entry.id {
					components = append(components, candidate)
					metas[candidate] = binding.meta
				}
			}
		}

		for _, component := range s.orderByDependencies(components) {
			if err := checkAbort(startCtx); err != nil {
				return err
			}

			if err := callKindHandler(startCtx, entry, component, metas[component]); err != nil {
				err = newStartError(StartOpHandle, component.scope, component.name, entry.id.name, err)
				return withAbortCause(startCtx, err)
			}
			if stillRegistered(component) {
				s.markHandled(component.service, entry.id)
			}
		}
	}

	return nil
}

// callKindHandler calls entry's handler for one component, recovering a panic
// as an error.
func callKindHandler(startCtx context.Context, entry kindEntry, component *startCandidate, meta any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = recoverAsError(r)
		}
	}()

	return entry.run(startCtx, component.scope, component.name, component.instance, meta)
}
