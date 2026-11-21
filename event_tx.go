package doger

import (
	"context"
	"errors"
	"reflect"
	"strconv"
)

// Tx is a transaction a listener can be bound to (see [ListenerTxPhase]):
// the callbacks it registers run once the transaction committed or rolled
// back, on the goroutine that ended it, in registration order. Core passes
// no transaction type through: an integration module (for example one for
// database/sql) implements Tx and the [TxSource] that finds it.
type Tx interface {
	// OnCommit registers fn to run once the transaction committed.
	OnCommit(fn func(ctx context.Context))
	// OnRollback registers fn to run once the transaction rolled back.
	OnRollback(fn func(ctx context.Context))
}

// TxSource finds the transaction a publish runs in. It is resolved once per
// root: [InjectorOpts.TxSource], else a root service named
// NameOf[TxSource](), else the TxSource of an [EventTransport] implementing
// [TxEventSender], else none.
type TxSource interface {
	// Lookup returns the transaction bound to ctx, and false when none is,
	// or when it already ended.
	Lookup(ctx context.Context) (Tx, bool)
	// Detach returns a context keeping the values of ctx, not cancelled
	// with it, in which Lookup finds no transaction. Deferred listeners and
	// after-commit sends run with it.
	Detach(ctx context.Context) context.Context
}

// TxPhase is when a listener runs relative to the transaction of the
// publish (see [ListenerTxPhase]).
type TxPhase int

const (
	// TxPhaseImmediate runs the listener during Publish. It is the zero
	// value, the default.
	TxPhaseImmediate TxPhase = iota
	// TxPhaseAfterCommit runs the listener once the transaction committed.
	TxPhaseAfterCommit
	// TxPhaseAfterRollback runs the listener once the transaction rolled
	// back.
	TxPhaseAfterRollback
	// TxPhaseAfterCompletion runs the listener once the transaction ended,
	// committed or rolled back.
	TxPhaseAfterCompletion
)

// String returns the name of the phase, or "TxPhase(n)" for another value.
func (p TxPhase) String() string {
	switch p {
	case TxPhaseImmediate:
		return "TxPhaseImmediate"
	case TxPhaseAfterCommit:
		return "TxPhaseAfterCommit"
	case TxPhaseAfterRollback:
		return "TxPhaseAfterRollback"
	case TxPhaseAfterCompletion:
		return "TxPhaseAfterCompletion"
	default:
		return "TxPhase(" + strconv.Itoa(int(p)) + ")"
	}
}

// ListenerTxPhase sets when the listener runs relative to the transaction
// the resolved [TxSource] finds in the publish context. A deferred phase
// (any but TxPhaseImmediate) registers the listener with the transaction's
// OnCommit or OnRollback callbacks, or both for TxPhaseAfterCompletion: it
// then runs with TxSource.Detach of the publish context, on the goroutine
// ending the transaction, or on the executor when it is also async, and
// its errors and panics go to [InjectorOpts.HookEventError]; Publish
// returns nil for it. Deferred listeners of one publish run in listener
// order. Without a transaction, see [ListenerTxFallback].
//
// Registration panics with "doger: invalid TxPhase <n>" for a value that
// is not one of the TxPhase constants. Given more than once, the last one
// wins.
func ListenerTxPhase(phase TxPhase) ListenerOption {
	return func(o *listenerOptions) {
		o.txPhase = phase
	}
}

// TxFallbackMode is what a listener with a deferred [TxPhase] does when
// the publish has no transaction (see [ListenerTxFallback]).
type TxFallbackMode int

const (
	// TxFallbackRun runs the listener during Publish, as an immediate one.
	// It is the zero value, the default.
	TxFallbackRun TxFallbackMode = iota
	// TxFallbackSkip skips the listener for that publish.
	TxFallbackSkip
)

// String returns the name of the mode, or "TxFallbackMode(n)" for another
// value.
func (m TxFallbackMode) String() string {
	switch m {
	case TxFallbackRun:
		return "TxFallbackRun"
	case TxFallbackSkip:
		return "TxFallbackSkip"
	default:
		return "TxFallbackMode(" + strconv.Itoa(int(m)) + ")"
	}
}

// ListenerTxFallback sets what a listener with a deferred [ListenerTxPhase]
// does when the resolved TxSource finds no transaction in the publish
// context (none is resolved, none is bound, or the bound one already
// ended): TxFallbackRun (the default) runs it during Publish, and
// TxFallbackSkip skips it. Registration panics with "doger: invalid
// TxFallbackMode <n>" for another value. Given more than once, the last one
// wins.
func ListenerTxFallback(mode TxFallbackMode) ListenerOption {
	return func(o *listenerOptions) {
		o.txFallback = mode
	}
}

// validateTxOptions panics for transaction options a listener cannot have.
func validateTxOptions(o *listenerOptions) {
	if o.txPhase < TxPhaseImmediate || o.txPhase > TxPhaseAfterCompletion {
		panic(libErrorf("invalid TxPhase %d", int(o.txPhase)))
	}
	if o.txFallback != TxFallbackRun && o.txFallback != TxFallbackSkip {
		panic(libErrorf("invalid TxFallbackMode %d", int(o.txFallback)))
	}
}

// deferred reports whether the listener runs once the transaction ended.
func (o *listenerOptions) deferred() bool {
	return o.txPhase != TxPhaseImmediate
}

// runsOnCommit reports whether a deferred listener runs on commit.
func (o *listenerOptions) runsOnCommit() bool {
	return o.txPhase == TxPhaseAfterCommit || o.txPhase == TxPhaseAfterCompletion
}

// runsOnRollback reports whether a deferred listener runs on rollback.
func (o *listenerOptions) runsOnRollback() bool {
	return o.txPhase == TxPhaseAfterRollback || o.txPhase == TxPhaseAfterCompletion
}

// ErrTxSourceConflict is matched by the error of Start, and of a
// transactional externalization, when the transport implements
// TxEventSender and another TxSource is configured: the transport writes in
// the transactions its own TxSource finds. Two TxSource values of one
// non-comparable type cannot be told apart, so they do not conflict.
var ErrTxSourceConflict = errors.New("doger: TxSource conflict")

// configuredTxSource returns InjectorOpts.TxSource, else the root service
// named NameOf[TxSource](), built once, else nil.
func (s *RootScope) configuredTxSource() (TxSource, error) {
	if source := s.opts.TxSource; source != nil {
		return source, nil
	}
	state := s.transportState()

	return resolveRootService(s, &state.mu, &state.txSource)
}

// txSource returns the TxSource of the container: the configured one,
// else the TxSource of a transport implementing TxEventSender, else nil.
func (s *RootScope) txSource() (TxSource, error) {
	source, err := s.configuredTxSource()
	if source != nil || err != nil {
		return source, err
	}
	transport, err := s.resolveTransport()
	if sender, ok := transport.(TxEventSender); ok {
		return sender.TxSource(), err
	}

	return nil, err
}

// resolveTxSource returns the TxSource of a transactional
// externalization through transport: as txSource, failing with
// ErrTxSourceConflict when transport is a TxEventSender and another
// TxSource is configured.
func (s *RootScope) resolveTxSource(transport EventTransport) (TxSource, error) {
	source, err := s.configuredTxSource()
	if err != nil {
		return nil, err
	}
	sender, ok := transport.(TxEventSender)
	switch {
	case !ok:
		return source, nil
	case source == nil:
		return sender.TxSource(), nil
	case !sameTxSource(source, sender.TxSource()):
		return nil, libErrorMatching(ErrTxSourceConflict, "the TxSource of the transport is not the configured one")
	default:
		return source, nil
	}
}

// sameTxSource reports whether a and b can be the same TxSource: the same
// comparable value, or two values of one non-comparable type, which cannot
// be told apart and are trusted to be.
func sameTxSource(a, b TxSource) bool {
	typ := reflect.TypeOf(a)
	if typ != nil && typ == reflect.TypeOf(b) && !typ.Comparable() {
		return true
	}

	return sameInstance(a, b)
}

// publishTx is the transaction of one publish, looked up on the first
// deferred listener.
type publishTx struct {
	looked bool
	tx     Tx
	found  bool
	source TxSource
	err    error // resolving the TxSource failed
}

// lookup reports whether ctx holds a transaction, looking it up once.
func (p *publishTx) lookup(ctx context.Context, root *RootScope) (bool, error) {
	if !p.looked {
		p.looked = true
		p.source, p.err = root.txSource()
		if p.source != nil {
			p.tx, p.found = p.source.Lookup(ctx)
		}
	}

	return p.found, p.err
}

// txRoute is what a publish does with a listener with a deferred phase.
type txRoute int

const (
	routeRun   txRoute = iota // no transaction: run it now (TxFallbackRun)
	routeDefer                // defer it to the transaction's callbacks
	routeSkip                 // no transaction: skip it (TxFallbackSkip)
)

// routeFor returns what a dispatch with cfg and ctx does with listener:
// run it now, unless it has a deferred phase and is not reached by a
// lifecycle event. Resolving the TxSource may fail.
func (p *publishTx) routeFor(ctx context.Context, root *RootScope, listener *plannedListener, cfg dispatchConfig) (txRoute, error) {
	if !listener.opts.deferred() || cfg.report {
		return routeRun, nil
	}
	found, err := p.lookup(ctx, root)
	switch {
	case err != nil:
		return routeRun, err
	case found:
		return routeDefer, nil
	case listener.opts.txFallback == TxFallbackSkip:
		return routeSkip, nil
	default:
		return routeRun, nil
	}
}

// deferListeners registers the deferred listeners with the commit and
// rollback callbacks of tx, running them with the detached publish context.
func (s *RootScope) deferListeners(ctx context.Context, source TxSource, tx Tx, listeners []*plannedListener, event any) {
	eventType := reflect.TypeOf(event)
	onCommit, onRollback := splitByCallback(listeners)
	if len(onCommit) > 0 {
		tx.OnCommit(func(context.Context) { s.runDeferred(source.Detach(ctx), onCommit, event, eventType) })
	}
	if len(onRollback) > 0 {
		tx.OnRollback(func(context.Context) { s.runDeferred(source.Detach(ctx), onRollback, event, eventType) })
	}
}

// runDeferred delivers event to listeners, whose filters accepted it at
// publish, in order, with the detached context ctx; each failure goes to
// HookEventError and does not stop the next listener.
func (s *RootScope) runDeferred(ctx context.Context, listeners []*plannedListener, event any, eventType reflect.Type) {
	for _, listener := range listeners {
		events := s.events.Load()
		shuttingDown := events != nil && events.shuttingDown.Load()
		if err := s.deliverAccepted(ctx, listener, event, eventType, listener.opts.async && !shuttingDown, shuttingDown); err != nil {
			s.reportEventError(ctx, listenerError(listener.id, eventType, err))
		}
	}
}

// splitByCallback returns, in order, the deferred listeners that run on
// commit and those that run on rollback; an after-completion listener is
// in both.
func splitByCallback(listeners []*plannedListener) (onCommit, onRollback []*plannedListener) {
	for _, listener := range listeners {
		if listener.opts.runsOnCommit() {
			onCommit = append(onCommit, listener)
		}
		if listener.opts.runsOnRollback() {
			onRollback = append(onRollback, listener)
		}
	}

	return onCommit, onRollback
}
