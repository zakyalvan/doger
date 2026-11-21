package doger

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
)

// EventErrorMode is how a synchronous dispatch handles listener errors; see
// [InjectorOpts.EventErrorMode].
type EventErrorMode int

const (
	// EventFailFast stops dispatch at the first listener error, which
	// Publish returns as a *ListenerError. It is the zero value, the default.
	EventFailFast EventErrorMode = iota
	// EventContinue runs every listener and returns the errors.Join of
	// their *ListenerErrors in listener order, or nil when none failed.
	EventContinue
)

// String returns the name of the mode, or "EventErrorMode(n)" for another
// value.
func (m EventErrorMode) String() string {
	switch m {
	case EventFailFast:
		return "EventFailFast"
	case EventContinue:
		return "EventContinue"
	default:
		return "EventErrorMode(" + strconv.Itoa(int(m)) + ")"
	}
}

// validateEventOptions panics for event options New cannot apply.
func validateEventOptions(opts *InjectorOpts) {
	if opts.EventErrorMode != EventFailFast && opts.EventErrorMode != EventContinue {
		panic(libErrorf("invalid EventErrorMode %d", int(opts.EventErrorMode)))
	}
}

// PublishOption changes how one Publish call delivers its event.
type PublishOption func(*publishOptions)

// publishOptions are the options of one Publish call.
type publishOptions struct {
	local bool // PublishLocal: only the publishing scope is reached
}

// buildPublishOptions applies opts. Without opts it allocates nothing:
// the options escape to the heap only when an option is applied.
func buildPublishOptions(opts []PublishOption) publishOptions {
	if len(opts) == 0 {
		return publishOptions{}
	}
	options := &publishOptions{}
	applyOptions(options, opts)

	return *options
}

// PublishLocal restricts delivery to the listeners of the publishing scope:
// its ancestors and the private scopes of installed extensions are not
// reached. By default an event reaches the publishing scope, its ancestors
// up to the root, then the private scopes of installed extensions; never
// sibling or descendant scopes, nor extensions still installing.
func PublishLocal() PublishOption {
	return func(o *publishOptions) {
		o.local = true
	}
}

// Publish delivers event to every reachable listener declared for a type
// the event's dynamic type is assignable to, and returns nil when none
// matches. Any Go value is an event: a listener of an interface type
// receives every event implementing it, and one of a pointer-receiver
// interface only pointer events.
//
// Defaults: delivery is synchronous, on the caller's goroutine, and the
// first listener error stops dispatch: Publish returns it as a
// [*ListenerError] naming the listener and the event type and unwrapping to
// the cause. A listener panic is recovered into a *ListenerError too. An
// async delivery refused because a shutdown began does not stop dispatch:
// the synchronous listeners still run, and Publish returns the refusals,
// matching
// [ErrPublishAfterShutdown], joined with any other error.
//
// Publish returns an error matching [ErrInvalidEvent] for a nil event, or
// a nil pointer, map, slice, channel or function (also inside an
// interface), and [ErrNilInjector] for a nil injector. A nil ctx reaches
// listeners as context.Background().
func Publish(ctx context.Context, i Injector, event any, opts ...PublishOption) error {
	options := buildPublishOptions(opts)

	ctx, injector, err := normalizePublish(ctx, i, event)
	if err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	scope := injectorScope(injector)
	if scope.closedForEvents() {
		return libErrorMatching(ErrPublishAfterShutdown, "scope `%s` is shut down", scope.name)
	}

	root := scope.rootScope

	return root.dispatch(ctx, scope, event, dispatchConfig{local: options.local, mode: root.opts.EventErrorMode})
}

// dispatchConfig is how one dispatch delivers its event.
type dispatchConfig struct {
	local    bool           // PublishLocal
	mode     EventErrorMode // how listener errors stop the dispatch
	report   bool           // listener errors go to reportEventError, never returned: lifecycle events
	funcOnly bool           // service listeners are not reached
}

// skips reports whether a dispatch with cfg passes listener by: a service
// listener for a function-only event, a service listener root shutdown
// must not build or reach, or a yielded default.
func (cfg dispatchConfig) skips(listener *plannedListener, shuttingDown bool) bool {
	return cfg.funcOnly && listener.fn == nil || shuttingDown && listener.unavailable() || listener.yielded()
}

// async reports whether a dispatch with cfg delivers to listener through
// the executor: a lifecycle event published once the async intake closed
// (ContainerClosed) reaches its async listeners inline instead.
func (cfg dispatchConfig) async(listener *plannedListener, shuttingDown bool) bool {
	return listener.opts.async && (!cfg.report || !shuttingDown)
}

// dispatch delivers event, published through scope, to the listeners it
// reaches, in order, handling their errors as cfg says.
func (s *RootScope) dispatch(ctx context.Context, scope *Scope, event any, cfg dispatchConfig) error {
	eventType := reflect.TypeOf(event)
	events := s.events.Load()
	shuttingDown := events != nil && events.shuttingDown.Load()

	var errs []error
	for _, listener := range matchListeners(scope, eventType, cfg.local) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return errors.Join(append(errs, ctxErr)...)
		}
		if cfg.skips(listener, shuttingDown) {
			continue
		}
		err := s.deliverTo(ctx, listener, event, eventType, cfg.async(listener, shuttingDown), shuttingDown)
		if err == nil {
			continue
		}
		listenerErr := listenerError(listener.id, eventType, err)
		switch {
		case cfg.report:
			s.reportEventError(ctx, listenerErr)
		case cfg.mode == EventFailFast && !isAsyncRefusal(listener, err):
			if len(errs) == 0 {
				return listenerErr
			}
			return errors.Join(append(errs, listenerErr)...)
		default:
			errs = append(errs, listenerErr)
		}
	}
	return errors.Join(errs...)
}

// asyncRefusal is the error of an async delivery refused because a
// shutdown stopped accepting them. It unwraps to an error matching
// ErrPublishAfterShutdown.
type asyncRefusal struct{ err error }

func (e *asyncRefusal) Error() string { return e.err.Error() }
func (e *asyncRefusal) Unwrap() error { return e.err }

// isAsyncRefusal reports whether err is the refusal of an async delivery
// to listener because a shutdown stopped accepting them: it does not stop
// a fail-fast dispatch, so synchronous listeners still run.
func isAsyncRefusal(listener *plannedListener, err error) bool {
	_, refused := err.(*asyncRefusal) //nolint:errorlint // the refusal is returned as is by submitAsync
	return listener.opts.async && refused
}

// ErrPublishAfterShutdown is matched by the error of Publish through a
// scope that was shut down, or a root whose shutdown completed, and by the
// error of an async delivery refused once root shutdown stopped accepting
// them.
var ErrPublishAfterShutdown = errors.New("doger: publish after shutdown")

// ErrInvalidEvent is matched by the error of Publish when the event is nil,
// or a nil pointer, map, slice, channel or function, also held in an
// interface. No listener is called.
var ErrInvalidEvent = errors.New("doger: invalid event")

// isNilEvent reports whether event is nil, or a nil value of a kind that
// can be nil.
func isNilEvent(event any) bool {
	if event == nil {
		return true
	}

	value := reflect.ValueOf(event)
	switch value.Kind() { //nolint:exhaustive // only the nil-able kinds matter
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.Interface, reflect.UnsafePointer:
		return value.IsNil()
	default:
		return false
	}
}

// normalizePublish checks the arguments of Publish: a nil context becomes
// context.Background(), and a nil injector or event is an error.
func normalizePublish(ctx context.Context, i Injector, event any) (context.Context, Injector, error) {
	injector, err := getInjector(i)
	if err != nil {
		return nil, nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if isNilEvent(event) {
		return nil, nil, libErrorMatching(ErrInvalidEvent, "cannot publish a nil event")
	}

	return ctx, injector, nil
}

// listenerError returns the *ListenerError of the listener id failing with
// err on an event of eventType.
func listenerError(id string, eventType reflect.Type, err error) *ListenerError {
	return &ListenerError{Listener: id, EventType: eventType, Err: err}
}

// deliverTo delivers event to listener when its filter accepts it: through
// the executor when async, on this goroutine otherwise. During
// root service shutdown, a service listener that cannot be resolved because
// it was shut down meanwhile is skipped, as if it was removed before; an
// error its OnEvent returns is never skipped.
func (s *RootScope) deliverTo(ctx context.Context, listener *plannedListener, event any, eventType reflect.Type, async, shuttingDown bool) error {
	accepted, err := listener.accepts(event)
	if err != nil || !accepted {
		return err
	}

	return s.deliverAccepted(ctx, listener, event, eventType, async, shuttingDown)
}

// deliverAccepted is deliverTo for an event the listener's filter already
// accepted.
func (s *RootScope) deliverAccepted(ctx context.Context, listener *plannedListener, event any, eventType reflect.Type, async, shuttingDown bool) error {
	if async {
		return s.submitAsync(ctx, listener, event, eventType)
	}

	deliver, err := recoverListenerErrorValue(listener.resolved)
	if err != nil {
		if shuttingDown && listener.fn == nil && errors.Is(err, ErrShutdown) {
			return nil
		}
		return err
	}

	return recoverListenerError(func() error { return deliver(ctx, event) })
}

// submitAsync submits the delivery of event to listener to the container's
// executor. It returns the error of the submission; the delivery's own
// error goes to reportEventError.
//
// The delivery is counted in flight in the listener's scope and its
// ancestors from its admission, under the intake lock and the scopes'
// locks, until it ends, so that drains wait for it; a delivery to a
// listener whose scope, or an ancestor scope, began shutting down is
// refused with an error matching ErrPublishAfterShutdown. Its context
// follows the root's abort and marks it, so that a shutdown called with it
// does not wait for it.
func (s *RootScope) submitAsync(ctx context.Context, listener *plannedListener, event any, eventType reflect.Type) error {
	deliver, err := recoverListenerErrorValue(listener.resolved)
	if err != nil {
		return err
	}

	events := s.eventState()
	events.intakeMu.RLock()
	if events.shuttingDown.Load() {
		events.intakeMu.RUnlock()
		return &asyncRefusal{err: libErrorMatching(ErrPublishAfterShutdown, "container is shutting down: async delivery refused")}
	}
	admitted := listener.scope.admitInflight()
	events.intakeMu.RUnlock()
	if !admitted {
		return &asyncRefusal{err: libErrorMatching(ErrPublishAfterShutdown, "scope `%s` is shut down: async delivery refused", listener.scope.name)}
	}

	var once sync.Once
	done := func() { once.Do(listener.scope.releaseInflight) }
	inSubmit := &atomic.Bool{}
	inSubmit.Store(true)
	defer inSubmit.Store(false)
	task := func(executorCtx context.Context) {
		defer done()
		abortable, release := followAbort(executorCtx, events.abortCtx)
		defer release()
		taskCtx, end := withDelivery(abortable, listener.scope, inSubmit)
		defer end()
		if err := recoverListenerError(func() error { return deliver(taskCtx, event) }); err != nil {
			s.reportEventError(taskCtx, listenerError(listener.id, eventType, err))
		}
	}

	err = recoverListenerError(func() error { return events.asyncExecutor().Submit(ctx, task) })
	if err != nil {
		done()
		if errors.Is(err, ErrPublishAfterShutdown) { // an executor shut down
			return &asyncRefusal{err: err}
		}
	}

	return err
}

// closedForEvents reports whether a Publish through s must fail: s is a
// child scope whose shutdown began, or the root's own scope once root
// shutdown completed its services.
func (s *Scope) closedForEvents() bool {
	if s.parentScope != nil {
		return s.closed.Load()
	}
	events := s.rootScope.events.Load()

	return events != nil && events.closed.Load()
}

// drainEvents stops accepting async deliveries, shuts the executor down,
// which wakes the publishers waiting for a slot, and waits for the
// deliveries in flight, bounded by ctx. When ctx ends first, it cancels
// their contexts with cause ErrPublishAfterShutdown and returns an error
// wrapping ctx.Err() at once: the cancelled deliveries end on their own,
// after Shutdown returned. A delivery whose context is ctx, an async
// listener shutting its own container down, is not waited for.
func (s *RootScope) drainEvents(ctx context.Context) error {
	events := s.eventState()
	events.closeIntake()

	executorErr := events.shutdownExecutor(ctx)
	if s.self.awaitInflight(ctx) && executorErr == nil {
		return nil
	}

	events.abort(ErrPublishAfterShutdown)
	if executorErr != nil && ctx.Err() == nil {
		return libErrorf("event executor shutdown: %w", executorErr)
	}

	return drainTimeoutError(ctx)
}

// drainTimeoutError is the error of a drain of async deliveries that ctx
// ended first.
func drainTimeoutError(ctx context.Context) error {
	return libErrorf("draining async event deliveries: %w", ctx.Err())
}

// reportEventError passes err, the failure of a listener with no caller, to
// HookEventError; without it, err is logged with Logger, or with the
// standard log package when Logger is unset. A hook panic is recovered and
// logged the same way.
func (s *RootScope) reportEventError(ctx context.Context, err error) {
	hook := s.opts.HookEventError
	if hook == nil {
		s.logEventError(err.Error())
		return
	}

	if recovered, panicked := callHookSafely(func() { hook(ctx, s, err) }); panicked {
		s.logEventError(libErrorf("HookEventError panicked: %v", recovered).Error())
	}
}

// logEventError logs message, about an event failure, with Logger, or with
// the standard log package when Logger is unset.
func (s *RootScope) logEventError(message string) {
	if s.opts.Logger == discardLogger {
		// The documented last-resort sink for an error no caller receives,
		// used only when neither Logger nor HookEventError is set (either
		// replaces it): message is the library's own error text, written
		// to the application's own log output.
		// bearer:disable go_lang_log_output_neutralization,go_lang_logger_leak
		log.Print(message)
		return
	}

	s.self.log(slog.LevelError, "event listener failed", slog.String("error", message))
}
