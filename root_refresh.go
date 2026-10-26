package doger

import (
	"context"
	"fmt"
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

// startIncremental runs the pipeline over the subtree of root.
func (s *RootScope) startIncremental(ctx context.Context, root *Scope) (err error) {
	s.pipelineMu.Lock()
	defer s.pipelineMu.Unlock()

	incCtx, err := s.beginIncremental(ctx)
	if err != nil {
		return err
	}
	defer func() { s.finishIncremental(incCtx, &err) }()

	return s.runPipeline(incCtx, []*Scope{root})
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
