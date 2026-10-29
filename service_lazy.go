package doger

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/kartaladev/doger/stacktrace"
)

var (
	_ serviceWrapper[int]       = (*serviceLazy[int])(nil)
	_ serviceWrapperHealthcheck = (*serviceLazy[int])(nil)
	_ serviceWrapperShutdown    = (*serviceLazy[int])(nil)
	_ serviceWrapperClone       = (*serviceLazy[int])(nil)
)

type serviceLazy[T any] struct {
	opts     *serviceOptions // nil when registered without options
	regIndex uint64          // registration order, unique per root container
	mu       sync.RWMutex
	name     string
	typeName string
	instance T

	// lazy loading
	built bool
	// published holds the instance once built, nil otherwise; health
	// checks read it without s.mu, which a build holds for the whole
	// provider call.
	published atomic.Pointer[T]
	buildTime time.Duration
	provider  Provider[T]

	providerFrame           stacktrace.Frame
	invokationFrames        map[stacktrace.Frame]struct{} // map garanties uniqueness
	invokationFramesCounter uint32
}

func newServiceLazy[T any](name string, provider Provider[T]) *serviceLazy[T] {
	providerFrame, _ := stacktrace.NewFrameFromPC(reflect.ValueOf(provider).Pointer())

	return &serviceLazy[T]{
		mu:       sync.RWMutex{},
		name:     name,
		typeName: inferServiceName[T](),

		built:     false,
		buildTime: 0,
		provider:  provider,

		providerFrame:           providerFrame,
		invokationFrames:        map[stacktrace.Frame]struct{}{},
		invokationFramesCounter: 0,
	}
}

func (s *serviceLazy[T]) getName() string {
	return s.name
}

func (s *serviceLazy[T]) getTypeName() string {
	return s.typeName
}

func (s *serviceLazy[T]) getServiceType() ServiceType {
	return ServiceTypeLazy
}

func (s *serviceLazy[T]) getReflectType() reflect.Type {
	return reflect.TypeFor[T]() // if T is a pointer or interface, it will return a typed nil
}

func (s *serviceLazy[T]) getInstanceAny(i Injector) (any, error) {
	return s.getInstance(i)
}

func (s *serviceLazy[T]) getInstance(i Injector) (T, error) {
	// Collect up to 100 invokation frames.
	// In the future, we can implement a LFU list, to evict the oldest
	// frames and keep the most recent ones, but it would be much more costly.
	if atomic.AddUint32(&s.invokationFramesCounter, 1) < MaxInvocationFrames {
		frame, ok := stacktrace.NewFrameFromCaller()
		if ok {
			s.mu.Lock()
			s.invokationFrames[frame] = struct{}{}
			s.mu.Unlock()
		}
	}

	instance, built, err := s.instanceOrBuild(i)
	if err != nil {
		return empty[T](), err
	}

	// outside s.mu, so the hook may use this service
	if built {
		onAfterBuild(i, s.name, instance)
	}

	return instance, nil
}

// instanceOrBuild returns the instance, building it first if needed, and
// whether this call built it.
func (s *serviceLazy[T]) instanceOrBuild(i Injector) (instance T, built bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.built {
		if err := s.build(i); err != nil {
			return empty[T](), false, err
		}
		built = true
	}

	return s.instance, built, nil
}

func (s *serviceLazy[T]) build(i Injector) (err error) {
	clock := injectorClock(i)
	start := clock.Now()

	if err := resolveDeclaredDependencies(i, s.opts); err != nil {
		return err
	}

	instance, err := handleProviderPanic(s.provider, i)
	if err != nil {
		return err
	}

	s.instance = instance
	s.built = true
	s.buildTime = clock.Since(start)
	s.published.Store(&instance)

	return nil
}

// onAfterBuild runs the after-build hooks for an instance a wrapper built,
// with the scope behind the injector the wrapper received.
func onAfterBuild(i Injector, name string, instance any) {
	if scope := injectorScope(i); scope != nil {
		scope.RootScope().opts.onAfterBuild(scope, name, instance)
	}
}

// injectorScope returns the *Scope behind an injector a wrapper receives
// (usually a provider's virtual scope), or nil.
func injectorScope(i Injector) *Scope {
	switch injector := i.(type) {
	case *virtualScope:
		return injectorScope(injector.self)
	case *Scope:
		return injector
	case *RootScope:
		return injector.self
	default:
		return nil
	}
}

// injectorClock returns the clock of the injector's root scope, or the real
// clock when there is no injector.
func injectorClock(i Injector) clockwork.Clock {
	if i == nil {
		return clockwork.NewRealClock()
	}

	return i.RootScope().opts.Clock
}

// builtInstance returns the instance and whether it is built, without
// taking s.mu: a service being built counts as not built.
func (s *serviceLazy[T]) builtInstance() (instance T, built bool) {
	published := s.published.Load()
	if published == nil {
		return empty[T](), false
	}

	return *published, true
}

func (s *serviceLazy[T]) isHealthchecker() bool {
	instance, built := s.builtInstance()
	if !built {
		return false
	}

	_, ok1 := any(instance).(HealthcheckerWithContext)
	_, ok2 := any(instance).(Healthchecker)
	return ok1 || ok2
}

// healthcheck runs the check of the built instance without holding s.mu, so
// a check in flight never delays the service's shutdown or invocation.
func (s *serviceLazy[T]) healthcheck(ctx context.Context) error {
	instance, built := s.builtInstance()
	if !built {
		return nil
	}

	switch instance := any(instance).(type) {
	case HealthcheckerWithContext:
		if err := contextDone(ctx); err != nil {
			return err
		}

		return instance.HealthCheck(ctx)
	case Healthchecker:
		if err := contextDone(ctx); err != nil {
			return err
		}

		return instance.HealthCheck()
	}

	return nil
}

func (s *serviceLazy[T]) isShutdowner() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.built {
		return false
	}

	_, ok1 := any(s.instance).(ShutdownerWithContextAndError)
	_, ok2 := any(s.instance).(ShutdownerWithError)
	_, ok3 := any(s.instance).(ShutdownerWithContext)
	_, ok4 := any(s.instance).(Shutdowner)
	return ok1 || ok2 || ok3 || ok4
}

func (s *serviceLazy[T]) shutdown(ctx context.Context) error {
	s.mu.Lock()

	defer func() {
		// whatever the outcome, reset `build` flag and instance
		s.built = false
		s.instance = empty[T]()
		s.published.Store(nil)
		s.mu.Unlock()
	}()

	if !s.built {
		return nil
	}

	switch instance := any(s.instance).(type) {
	case ShutdownerWithContextAndError:
		if ctx.Err() != nil {
			return ctx.Err()
		}

		return instance.Shutdown(ctx)
	case ShutdownerWithError:
		if ctx.Err() != nil {
			return ctx.Err()
		}

		return instance.Shutdown()
	case ShutdownerWithContext:
		if ctx.Err() != nil {
			return ctx.Err()
		}

		instance.Shutdown(ctx)
	case Shutdowner:
		if ctx.Err() != nil {
			return ctx.Err()
		}

		instance.Shutdown()
	}

	return nil
}

func (s *serviceLazy[T]) clone(newScope Injector) any {
	// reset `build` flag and instance
	return &serviceLazy[T]{
		mu:       sync.RWMutex{},
		name:     s.name,
		regIndex: s.regIndex,
		opts:     s.opts,
		typeName: s.typeName,

		built:     false,
		provider:  s.provider,
		buildTime: 0,

		providerFrame:           s.providerFrame,
		invokationFrames:        map[stacktrace.Frame]struct{}{},
		invokationFramesCounter: 0,
	}
}

func (s *serviceLazy[T]) source() (stacktrace.Frame, []stacktrace.Frame) {
	s.mu.RLock()
	invokationFrames := make([]stacktrace.Frame, 0, len(s.invokationFrames))
	for frame := range s.invokationFrames {
		invokationFrames = append(invokationFrames, frame)
	}
	s.mu.RUnlock()

	return s.providerFrame, invokationFrames
}

func (s *serviceLazy[T]) getBuildTime() (time.Duration, bool) {
	return s.buildTime, s.built
}

func (s *serviceLazy[T]) getRegistrationIndex() uint64 {
	return s.regIndex
}

func (s *serviceLazy[T]) setRegistrationIndex(index uint64) {
	s.regIndex = index
}

func (s *serviceLazy[T]) getOptions() *serviceOptions {
	return s.opts
}

func (s *serviceLazy[T]) setOptions(opts *serviceOptions) {
	s.opts = opts
}
