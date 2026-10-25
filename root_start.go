package doger

import (
	"cmp"
	"context"
	"fmt"
	"slices"
)

// startPhase is the start state of a root container.
type startPhase int

const (
	startIdle startPhase = iota
	startStarting
	startStarted
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
// Only [RootScope] methods are container-level: the root's *Scope (the
// injector a provider receives) and child scopes have no Start.
func (s *RootScope) StartWithContext(ctx context.Context) error {
	if err := s.beginStart(); err != nil {
		return err
	}

	err := s.runStart(ctx)

	// pointAfterStart: startDone is closed, so code here may call Shutdown().
	return err
}

// beginStart moves the start phase from idle to starting.
func (s *RootScope) beginStart() error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	if s.closePhase != closeOpen {
		return fmt.Errorf("DI: cannot start: %w", ErrStartAfterShutdown)
	}
	if s.startPhase != startIdle {
		return ErrAlreadyStarted
	}

	s.startPhase = startStarting
	s.startDone = make(chan struct{})

	return nil
}

// runStart runs the pipeline. Its deferred finishStart runs even when the
// pipeline panics, so the lifecycle state is always released.
func (s *RootScope) runStart(ctx context.Context) (err error) {
	panicked := true
	defer func() { s.finishStart(err, panicked) }()

	err = s.runStartPipeline(ctx)
	panicked = false

	return err
}

// finishStart records the outcome of Start and releases its waiters.
func (s *RootScope) finishStart(err error, panicked bool) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	if err != nil || panicked {
		s.startPhase = startFailed
	} else {
		s.startPhase = startStarted
	}
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
	instance any
}

func (s *RootScope) runStartPipeline(ctx context.Context) error {
	candidates := s.discoverCandidates()

	if err := buildCandidates(candidates); err != nil {
		return err
	}

	return startStarters(ctx, candidates)
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
	if !starter {
		return nil
	}

	return &startCandidate{
		scope:    scope,
		name:     name,
		depth:    depth,
		regIndex: service.getRegistrationIndex(),
		service:  service,
		starter:  starter,
	}
}

// buildCandidates invokes every candidate, keeping its instance.
func buildCandidates(candidates []*startCandidate) error {
	for _, candidate := range candidates {
		instance, err := invokeAnyByName(candidate.scope, candidate.name)
		if err != nil {
			return err
		}
		candidate.instance = instance
	}

	return nil
}

// startStarters starts the Starter candidates in order, stopping at the
// first error.
func startStarters(ctx context.Context, candidates []*startCandidate) error {
	for _, candidate := range candidates {
		if !candidate.starter {
			continue
		}

		if err := startInstance(ctx, candidate.instance); err != nil {
			return err
		}
	}

	return nil
}

// startInstance calls the Start method of the instance's Starter variant.
// The type switch mirrors the one of the shutdown path.
func startInstance(ctx context.Context, instance any) error {
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
