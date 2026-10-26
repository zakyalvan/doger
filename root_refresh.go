package doger

import (
	"context"
	"fmt"
	"slices"
)

// Refresh starts what is not started yet, with context.Background(); see
// [RootScope.RefreshWithContext].
func (s *RootScope) Refresh() error {
	return s.RefreshWithContext(context.Background())
}

// RefreshWithContext runs the start pipeline over the services and scopes of
// the whole container that are not started or handled yet: services
// registered after Start, and scopes created after it. It builds them, runs
// kind handlers for their new components and starts their Starters, with the
// same order, context, error and per-service hook rules as Start. Each
// service is started at most once per container. Refresh fires no container
// hook.
//
// It returns an error matching [ErrNotStarted] until the first Start
// succeeded, and one matching [ErrStartAfterShutdown] once root Shutdown
// began, which takes precedence; it starts nothing then. Concurrent Refresh
// and [Scope.Start] calls run one at a time. Never call Refresh
// synchronously from code that runs during a Refresh or Scope.Start: it
// waits for itself.
func (s *RootScope) RefreshWithContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	return s.startIncremental(ctx, s.self)
}

// startIncremental runs the pipeline over the subtree of root. For a child
// scope, the scope Starting hooks run first and can veto it.
func (s *RootScope) startIncremental(ctx context.Context, root *Scope) (err error) {
	s.pipelineMu.Lock()
	defer s.pipelineMu.Unlock()

	incCtx, err := s.beginIncremental(ctx)
	if err != nil {
		return err
	}
	defer func() { s.finishIncremental(incCtx, &err) }()

	if root != s.self {
		if err := s.runScopeStartingHooks(incCtx, root); err != nil {
			return err
		}
	}

	return s.runPipeline(incCtx, []*Scope{root})
}

// runScopeStartingHooks runs the scope Starting hooks for scope in order.
// The first error or panic is a veto: the remaining hooks do not run and
// Scope.Start fails with it.
func (s *RootScope) runScopeStartingHooks(ctx context.Context, scope *Scope) error {
	for _, hook := range slices.Clone(s.opts.HookScopeStarting) {
		if hook == nil {
			continue
		}

		var err error
		if recovered, panicked := callHookSafely(func() { err = hook(ctx, scope) }); panicked {
			err = recoverAsError(recovered)
		}
		if err != nil {
			return fmt.Errorf("DI: scope `%s` start aborted: %w", scope.Name(), err)
		}
	}

	return nil
}

// beginIncremental checks that the container may start incrementally and
// returns the context of the run, derived from ctx.
func (s *RootScope) beginIncremental(ctx context.Context) (context.Context, error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	if s.closePhase != closeOpen {
		return nil, fmt.Errorf("DI: cannot start: %w", ErrStartAfterShutdown)
	}
	if s.startPhase != startStarted && s.startPhase != startReady {
		return nil, ErrNotStarted
	}

	incCtx, incCancel := context.WithCancelCause(ctx)
	s.incCancel = incCancel
	s.incDone = make(chan struct{})

	return incCtx, nil
}

// finishIncremental ends the context of the run and releases its waiters. A
// run that succeeded although Shutdown began after its last abort check is
// reported as aborted.
func (s *RootScope) finishIncremental(incCtx context.Context, err *error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	if *err == nil && s.closePhase != closeOpen {
		*err = fmt.Errorf("DI: start aborted: %w", context.Cause(incCtx))
	}

	s.incCancel(nil)
	close(s.incDone)
	s.incCancel = nil
	s.incDone = nil
}

// isStarted reports whether the Starter service was already started.
func (s *RootScope) isStarted(service any) bool {
	s.startedMu.Lock()
	defer s.startedMu.Unlock()

	_, ok := s.started[service]
	return ok
}

// markStarted records that the Starter service was started.
func (s *RootScope) markStarted(service any) {
	s.startedMu.Lock()
	defer s.startedMu.Unlock()

	if s.started == nil {
		s.started = map[any]struct{}{}
	}
	s.started[service] = struct{}{}
}

// Start starts what is not started yet in this scope and its descendants,
// with context.Background(); see [Scope.StartWithContext].
func (s *Scope) Start() error {
	return s.StartWithContext(context.Background())
}

// StartWithContext does what [RootScope.RefreshWithContext] does, restricted
// to this scope and its descendants: it builds their services that are not
// started or handled yet, runs kind handlers for their new components and
// starts their Starters. Ancestor services the subtree depends on are built
// as needed but not started by it. On the root's own scope it is Refresh.
//
// It follows the rules of Refresh: [ErrNotStarted] before the container's
// first successful Start, [ErrStartAfterShutdown] once root Shutdown began,
// calls serialized with Refresh, and never call it synchronously from code
// running during a Refresh or Scope.Start.
func (s *Scope) StartWithContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if err := s.rootScope.startIncremental(ctx, s); err != nil {
		return err
	}

	if s != s.rootScope.self {
		for _, hook := range slices.Clone(s.rootScope.opts.HookScopeStarted) {
			if hook != nil {
				hook(ctx, s)
			}
		}
	}

	return nil
}

// isHandled reports whether kind id already handled the service.
func (s *RootScope) isHandled(service any, id *kindID) bool {
	s.startedMu.Lock()
	defer s.startedMu.Unlock()

	_, ok := s.handled[service][id]
	return ok
}

// markHandled records that kind id handled the service.
func (s *RootScope) markHandled(service any, id *kindID) {
	s.startedMu.Lock()
	defer s.startedMu.Unlock()

	if s.handled == nil {
		s.handled = map[any]map[*kindID]struct{}{}
	}
	if s.handled[service] == nil {
		s.handled[service] = map[*kindID]struct{}{}
	}
	s.handled[service][id] = struct{}{}
}
