package doger

import (
	"cmp"
	"context"
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
// it, are not started.
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
// cause, so errors.Is and errors.As reach it. Starters not reached yet are not started. Nothing is rolled
// back: cleanup is the caller's job, typically by calling Shutdown, so
// services must tolerate Shutdown without a prior Start.
//
// Shutdown during Start: a root Shutdown that begins while Start runs
// cancels the Start-time context with cause [ErrStartAfterShutdown] and waits
// for Start to return, bounded by its own context. Start stops before its
// next Starter or handler and returns an error matching
// ErrStartAfterShutdown. A Starter that ignores its context and finishes
// after Shutdown stopped waiting is not rolled back: honour ctx.
//
// Never call Shutdown synchronously from code that runs during Start (a
// Starter, a kind handler, a runner, a provider or a hook): Shutdown waits
// for Start, which waits for that code, so it deadlocks unless Shutdown's
// context has a deadline. Return an error instead. Calling Start from such
// code returns [ErrAlreadyStarted] immediately.
//
// Only [RootScope] methods are container-level: the root's *Scope (the
// injector a provider receives, or [Component.Scope] of a root service) and
// child scopes have no Start, and a Shutdown reached through them bypasses
// this coordination.
func (s *RootScope) StartWithContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	startCtx, err := s.beginStart(ctx)
	if err != nil {
		return err
	}

	err = s.runStart(startCtx)

	// pointAfterStart: startDone is closed, so code here may call Shutdown().
	var first hookPanic
	s.dispatchAfterStart(ctx, err, &first)
	if first.panicked {
		s.dispatchHookPanic(first.point, first.value)
	}

	return err
}

// dispatchAfterStart fires the pointAfterStart hooks from Start's local
// outcome: Started, then Ready, on success; Failed with err otherwise.
func (s *RootScope) dispatchAfterStart(ctx context.Context, err error, first *hookPanic) {
	if err != nil {
		for _, hook := range slices.Clone(s.opts.HookContainerFailed) {
			if hook != nil {
				s.callObserver(first, "failed", func() { hook(ctx, s, err) })
			}
		}
		return
	}

	s.runObserverHooks(ctx, "started", s.opts.HookContainerStarted, first)
	s.markReady()
	s.runObserverHooks(ctx, "ready", s.opts.HookContainerReady, first)
}

// hookPanic is the first observer hook panic of one Start or Shutdown.
type hookPanic struct {
	point    string
	value    any
	panicked bool
}

// callObserver calls an observer hook, recovering its panic: the first one
// of the operation is kept in first, later ones are logged.
func (s *RootScope) callObserver(first *hookPanic, point string, call func()) {
	recovered, panicked := callHookSafely(call)
	if !panicked {
		return
	}

	if first.panicked {
		s.opts.Logf("DI: container %s hook panicked: %v", point, recovered)
		return
	}
	*first = hookPanic{point: point, value: recovered, panicked: true}
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
	for _, hook := range slices.Clone(hooks) {
		if hook != nil {
			s.callObserver(first, point, func() { hook(ctx, s) })
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

// runStart runs the pipeline. Its deferred finishStart runs even when the
// pipeline panics, so the lifecycle state is always released.
func (s *RootScope) runStart(startCtx context.Context) (err error) {
	panicked := true
	defer func() { s.finishStart(startCtx, &err, panicked) }()

	err = s.runStartPipeline(startCtx)
	panicked = false

	return err
}

// finishStart records the outcome of Start, ends the Start-time context and
// releases its waiters. A Start that succeeded although Shutdown began after
// its last abort check is reported as aborted.
func (s *RootScope) finishStart(startCtx context.Context, err *error, panicked bool) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	if *err == nil && !panicked && s.closePhase != closeOpen {
		*err = fmt.Errorf("DI: start aborted: %w", context.Cause(startCtx))
	}

	if *err != nil || panicked {
		s.startPhase = startFailed
	} else {
		s.startPhase = startStarted
	}
	s.startCancel(nil)
	close(s.startDone)
}

// startCandidate is a service discovered by Start.
type startCandidate struct {
	scope    *Scope
	name     string
	depth    int
	regIndex uint64
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

	candidates := s.discoverCandidates()

	if err := buildCandidates(startCtx, candidates); err != nil {
		return err
	}

	if err := s.runStage(startCtx, StageBeforeStart, candidates); err != nil {
		return err
	}

	if err := s.checkCycles(candidates); err != nil {
		return err
	}

	if err := s.startStarters(startCtx, s.orderByDependencies(starterCandidates(candidates))); err != nil {
		return err
	}

	// pointStartersUp
	if err := s.runStage(startCtx, StageAfterStart, candidates); err != nil {
		return err
	}

	if err := checkAbort(startCtx); err != nil {
		return err
	}

	return nil
}

// runStartingHooks runs the container Starting hooks in order. The first
// error or panic is a veto: the remaining hooks do not run and Start fails
// with it.
func (s *RootScope) runStartingHooks(startCtx context.Context) error {
	for _, hook := range slices.Clone(s.opts.HookContainerStarting) {
		if hook == nil {
			continue
		}

		var err error
		if recovered, panicked := callHookSafely(func() { err = hook(startCtx, s) }); panicked {
			err = startingHookPanicError(recovered)
		}
		if err != nil {
			return fmt.Errorf("DI: container start aborted: %w", err)
		}
	}

	return nil
}

// startingHookPanicError turns a Starting hook panic into a veto error. An
// error value is wrapped, so errors.Is and errors.As reach it.
func startingHookPanicError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return fmt.Errorf("DI: container starting hook panicked: %w", err)
	}

	return fmt.Errorf("DI: container starting hook panicked: %v", recovered)
}

func (c *startCandidate) desc() ServiceDescription {
	return newServiceDescription(c.scope.ID(), c.scope.Name(), c.name)
}

// discoverCandidates snapshots the scope tree breadth-first, root first, and
// returns the services Start must build, ordered by scope depth then
// registration order.
func (s *RootScope) discoverCandidates() []*startCandidate {
	candidates := []*startCandidate{}

	type level struct {
		scope *Scope
		depth int
	}
	queue := []level{{scope: s.self, depth: 0}}
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
		return cmp.Or(cmp.Compare(a.depth, b.depth), cmp.Compare(a.regIndex, b.regIndex))
	})

	return candidates
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
	starter := (opts != nil && opts.starter) ||
		(!s.opts.DisableStarterAutoDetection && typeIsStarter(service.getReflectType()))
	var kinds []kindBinding
	if opts != nil {
		kinds = opts.kinds
	}
	if s.opts.EnableRunnerAutoDetection && typeCanCastToType(service.getReflectType(), runnableKind.target()) &&
		!slices.ContainsFunc(kinds, func(b kindBinding) bool { return b.id == runnableKind.id }) {
		// implicit binding, so a runner opted in explicitly still runs once
		kinds = append(slices.Clone(kinds), kindBinding{id: runnableKind.id, meta: struct{}{}, target: runnableKind.target()})
	}
	if !starter && len(kinds) == 0 {
		return nil
	}

	return &startCandidate{
		scope:    scope,
		name:     name,
		depth:    depth,
		regIndex: service.getRegistrationIndex(),
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
				return newStartError(StartOpBuild, candidate.scope, candidate.name, "", err)
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
// first error.
func (s *RootScope) startStarters(startCtx context.Context, candidates []*startCandidate) error {
	for _, candidate := range candidates {
		if !candidate.starter {
			continue
		}

		if err := checkAbort(startCtx); err != nil {
			return err
		}

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
	}

	return nil
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
	entries := slices.Clone(s.kinds)
	s.kindsMu.Unlock()

	if !slices.ContainsFunc(entries, func(e kindEntry) bool { return e.id == runnableKind.id }) {
		// the built-in Runnable kind has an implicit default handler
		entries = slices.Insert(entries, 0, defaultRunnableEntry)
	}

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
