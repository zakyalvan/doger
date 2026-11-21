package doger

import (
	"cmp"
	"context"
	"errors"
	"reflect"
	"strconv"
)

// ExternalizeStrategy is when an externalized event is handed to the
// transport. The strategy of one publish is the first set of
// [PublishExternalize], the type's [EventExternalize] and
// [InjectorOpts.EventExternalize]; otherwise ExternalizeAuto.
type ExternalizeStrategy int

const (
	// ExternalizeAuto is ExternalizeOutbox when the resolved TxSource finds
	// a transaction and the transport implements TxEventSender, and
	// ExternalizeImmediate otherwise. With a transaction but a transport
	// without an outbox, the event is sent during Publish, so it is sent
	// even when that transaction later rolls back: choose
	// ExternalizeAfterCommit or an outbox-capable transport to avoid that.
	// It is the zero value, the default.
	ExternalizeAuto ExternalizeStrategy = iota
	// ExternalizeImmediate sends the event during Publish, on the
	// publisher's goroutine with its context, after the in-process
	// listeners ran; Publish returns the send error as an
	// *ExternalizeError.
	ExternalizeImmediate
	// ExternalizeAfterCommit sends the event once the publish's transaction
	// committed, with the detached publish context, and nothing when it
	// rolls back; without a transaction it is ExternalizeImmediate. The
	// send error goes to HookEventError. Nothing is recorded: a crash
	// between the commit and the send loses the event (at most once on a
	// crash); use ExternalizeOutbox to keep it.
	ExternalizeAfterCommit
	// ExternalizeOutbox writes the event in the publish's transaction with
	// the transport's TxEventSender.SendTx, during Publish, which returns
	// its error; the transport delivers it once the transaction committed.
	// Without a transaction, see InjectorOpts.EventOutboxFallback.
	ExternalizeOutbox
)

// String returns the name of the strategy, or "ExternalizeStrategy(n)" for
// another value.
func (s ExternalizeStrategy) String() string {
	switch s {
	case ExternalizeAuto:
		return "ExternalizeAuto"
	case ExternalizeImmediate:
		return "ExternalizeImmediate"
	case ExternalizeAfterCommit:
		return "ExternalizeAfterCommit"
	case ExternalizeOutbox:
		return "ExternalizeOutbox"
	default:
		return "ExternalizeStrategy(" + strconv.Itoa(int(s)) + ")"
	}
}

// PublishExternalize sets the externalization strategy of one publish,
// over the type's and the container's. It panics with "doger: invalid
// ExternalizeStrategy <n>" for a value that is not one of the constants.
func PublishExternalize(strategy ExternalizeStrategy) PublishOption {
	validateStrategy(strategy)

	return func(o *publishOptions) {
		o.externalize = strategy
		o.externalizeSet = true
	}
}

// validStrategy reports whether strategy is one of the constants.
func validStrategy(strategy ExternalizeStrategy) bool {
	return strategy >= ExternalizeAuto && strategy <= ExternalizeOutbox
}

// validateStrategy panics for a strategy that is not one of the constants.
func validateStrategy(strategy ExternalizeStrategy) {
	if !validStrategy(strategy) {
		panic(libErrorf("invalid ExternalizeStrategy %d", int(strategy)))
	}
}

// ErrOutboxNotSupported is matched by the error of Start when
// InjectorOpts.EventExternalize or a registered type's strategy is
// ExternalizeOutbox and the transport does not implement TxEventSender,
// and by the *ExternalizeError of such a publish.
var ErrOutboxNotSupported = errors.New("doger: outbox not supported")

// OutboxFallback is what an ExternalizeOutbox publish with no transaction
// bound does (see [InjectorOpts.EventOutboxFallback]).
type OutboxFallback int

const (
	// OutboxFallbackFail fails the publish with an *ExternalizeError
	// matching ErrNoTransaction, sending nothing. It is the zero value,
	// the default.
	OutboxFallbackFail OutboxFallback = iota
	// OutboxFallbackImmediate sends the event as ExternalizeImmediate.
	OutboxFallbackImmediate
)

// String returns the name of the fallback, or "OutboxFallback(n)" for
// another value.
func (f OutboxFallback) String() string {
	switch f {
	case OutboxFallbackFail:
		return "OutboxFallbackFail"
	case OutboxFallbackImmediate:
		return "OutboxFallbackImmediate"
	default:
		return "OutboxFallback(" + strconv.Itoa(int(f)) + ")"
	}
}

// ErrNoTransaction is matched by the error of an ExternalizeOutbox publish
// with no transaction bound, unless InjectorOpts.EventOutboxFallback allows
// sending it immediately.
var ErrNoTransaction = errors.New("doger: no transaction")

// EventExternalized makes the events of the type leave the process through
// the transport, with the strategy of the publish or of the container
// (see [PublishExternalize] and [InjectorOpts.EventExternalize]).
func EventExternalized() EventTypeOption {
	return func(o *eventTypeOptions) {
		o.externalized = true
	}
}

// EventExternalize makes the events of the type leave the process through
// the transport with strategy.
func EventExternalize(strategy ExternalizeStrategy) EventTypeOption {
	return func(o *eventTypeOptions) {
		o.externalized = true
		o.strategy = strategy
		o.strategySet = true
	}
}

// ExternalizeError is the error of an event that could not be handed to
// the transport. It unwraps to the cause.
type ExternalizeError struct {
	EventType   string              // the registered name of the event type
	Destination string              // where it was sent
	Strategy    ExternalizeStrategy // the strategy used: ExternalizeImmediate when Auto, an Outbox fallback or an AfterCommit without transaction sent at once
	Err         error               // the cause
}

// Error returns "doger: externalizing event `<type>` to `<destination>`
// (<strategy>): <cause>".
func (e *ExternalizeError) Error() string {
	return libErrorf("externalizing event `%s` to `%s` (%s): %w", e.EventType, e.Destination, e.Strategy, e.Err).Error()
}

// Unwrap returns the cause.
func (e *ExternalizeError) Unwrap() error { return e.Err }

// externalizeEvent externalizes event after its in-process dispatch ended
// with dispatchErr, when its type is externalized, and returns the outcome
// of both. A type that is not externalized costs one registry read.
func (s *RootScope) externalizeEvent(ctx context.Context, event any, options publishOptions, dispatchErr error) error {
	if options.local || dispatchErr != nil && s.opts.EventErrorMode == EventFailFast && !onlyAsyncRefusals(dispatchErr) {
		return dispatchErr
	}
	info := s.eventTypeOf(reflect.TypeOf(event))
	if info == nil || !info.externalized {
		return dispatchErr
	}

	return joinErrors([]error{dispatchErr, s.externalize(ctx, info, event, options)})
}

// chooseStrategy returns the strategy of one publish: its
// PublishExternalize, else the type's EventExternalize, else global, the
// container's InjectorOpts.EventExternalize.
func chooseStrategy(options publishOptions, info *eventTypeInfo, global ExternalizeStrategy) ExternalizeStrategy {
	switch {
	case options.externalizeSet:
		return options.externalize
	case info.strategySet:
		return info.strategy
	default:
		return global
	}
}

// externalize hands event, of the externalized type info, to the transport
// with the strategy of the publish.
func (s *RootScope) externalize(ctx context.Context, info *eventTypeInfo, event any, options publishOptions) error {
	x := &externalization{root: s, ctx: ctx, info: info, strategy: chooseStrategy(options, info, s.opts.EventExternalize)}

	transport, err := s.resolveTransport()
	if err != nil {
		return x.fail(err)
	}
	if transport == nil {
		s.transportState().noTransport.Do(func() {
			s.logWarning("no EventTransport: externalized events stay in-process")
		})
		return nil
	}
	x.transport = transport

	x.env, err = s.newEnvelope(ctx, info, event)
	if err != nil {
		return x.fail(err)
	}

	return x.run()
}

// externalization is one event being handed to the transport.
type externalization struct {
	root      *RootScope
	ctx       context.Context //nolint:containedctx // the publish context, for the one call of the strategy
	info      *eventTypeInfo
	strategy  ExternalizeStrategy
	transport EventTransport
	env       Envelope
}

// fail returns err as the *ExternalizeError of x.
func (x *externalization) fail(err error) error {
	destination := cmp.Or(x.env.Destination, x.info.destination)

	return &ExternalizeError{EventType: x.info.name, Destination: destination, Strategy: x.strategy, Err: err}
}

// run sends the envelope as its strategy says, with the transaction the
// TxSource finds in the publish context.
func (x *externalization) run() error {
	if x.strategy == ExternalizeOutbox {
		if err := checkOutbox(x.transport); err != nil {
			return x.fail(err)
		}
	}
	txSender, outbox := x.transport.(TxEventSender)
	var source TxSource
	var tx Tx
	var found bool
	if x.strategy != ExternalizeImmediate {
		var err error
		if source, err = x.root.resolveTxSource(x.transport); err != nil {
			return x.fail(err)
		}
		if source != nil {
			tx, found = source.Lookup(x.ctx)
		}
	}

	switch x.strategy {
	case ExternalizeAuto:
		if outbox && found {
			x.strategy = ExternalizeOutbox
			return x.sendTx(txSender)
		}
	case ExternalizeOutbox:
		if found {
			return x.sendTx(txSender)
		}
		if x.root.opts.EventOutboxFallback != OutboxFallbackImmediate {
			return x.fail(libErrorMatching(ErrNoTransaction, "no transaction bound for the outbox"))
		}
	case ExternalizeAfterCommit:
		if found {
			x.sendAfterCommit(source, tx)
			return nil
		}
	case ExternalizeImmediate:
	}

	x.strategy = ExternalizeImmediate // a fallback reports the strategy it used

	return x.send()
}

// send sends the envelope now, with the publish context.
func (x *externalization) send() error {
	return x.call(x.transport.Send)
}

// sendTx writes the envelope in the transaction of the publish context.
func (x *externalization) sendTx(sender TxEventSender) error {
	return x.call(sender.SendTx)
}

// call hands the envelope to fn with the publish context, returning its
// error or panic as the *ExternalizeError of x.
func (x *externalization) call(fn func(context.Context, Envelope) error) error {
	if err := recoverListenerError(func() error { return fn(x.ctx, x.env) }); err != nil {
		return x.fail(err)
	}

	return nil
}

// sendAfterCommit sends the envelope once tx committed, with the detached
// publish context; its error goes to HookEventError.
func (x *externalization) sendAfterCommit(source TxSource, tx Tx) {
	tx.OnCommit(func(context.Context) {
		detached := source.Detach(x.ctx)
		if err := recoverListenerError(func() error { return x.transport.Send(detached, x.env) }); err != nil {
			x.root.reportEventError(detached, x.fail(err))
		}
	})
}
