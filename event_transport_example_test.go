package doger

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// exampleTx is the transaction of the transaction examples: a real one
// comes from an integration module, such as one for database/sql.
type exampleTx struct{ commits, rollbacks []func(context.Context) }

func (tx *exampleTx) OnCommit(fn func(context.Context))   { tx.commits = append(tx.commits, fn) }
func (tx *exampleTx) OnRollback(fn func(context.Context)) { tx.rollbacks = append(tx.rollbacks, fn) }

func (tx *exampleTx) commit() {
	for _, fn := range tx.commits {
		fn(context.Background())
	}
}

// exampleTxKey binds an *exampleTx to a context.
type exampleTxKey struct{}

// exampleTxSource finds the *exampleTx bound to a context.
type exampleTxSource struct{}

func (exampleTxSource) Lookup(ctx context.Context) (Tx, bool) {
	tx, ok := ctx.Value(exampleTxKey{}).(*exampleTx)
	return tx, ok && tx != nil
}

func (exampleTxSource) Detach(ctx context.Context) context.Context {
	return context.WithValue(context.WithoutCancel(ctx), exampleTxKey{}, (*exampleTx)(nil))
}

// ExampleListenerTxPhase shows a listener deferred until the publisher's
// transaction commits, found by InjectorOpts.TxSource. Without a
// transaction, the listener runs during Publish (TxFallbackRun), unless
// ListenerTxFallback(TxFallbackSkip) says otherwise.
func ExampleListenerTxPhase() {
	injector := NewWithOpts(&InjectorOpts{TxSource: exampleTxSource{}})

	Listen(injector, func(_ context.Context, e exampleOrderPlaced) error {
		fmt.Println("send confirmation for", e.ID)
		return nil
	}, ListenerTxPhase(TxPhaseAfterCommit))

	tx := &exampleTx{}
	ctx := context.WithValue(context.Background(), exampleTxKey{}, tx)
	_ = Publish(ctx, injector, exampleOrderPlaced{ID: "42"})
	fmt.Println("published")
	tx.commit()

	_ = Publish(context.Background(), injector, exampleOrderPlaced{ID: "43"})

	// Output:
	// published
	// send confirmation for 42
	// send confirmation for 43
}

// ExampleRegisterEventType gives an event type a stable wire name, here
// with a version and an alias kept from before a rename. A name is bound
// to one type: binding it to another fails with ErrEventTypeConflict.
func ExampleRegisterEventType() {
	injector := New()

	err := RegisterEventType[exampleOrderPlaced](injector, "orders.placed",
		EventVersion(2), EventAlias("order.created"))
	fmt.Println("registered:", err == nil)

	type otherEvent struct{}
	err = RegisterEventType[otherEvent](injector, "orders.placed")
	fmt.Println("conflict:", errors.Is(err, ErrEventTypeConflict))

	// Output:
	// registered: true
	// conflict: true
}

// printingTransport prints what it is asked to send, and delivers nothing.
type printingTransport struct{}

func (printingTransport) Send(_ context.Context, env Envelope) error {
	fmt.Println("send", env.EventType, "to", env.Destination)
	return nil
}

func (printingTransport) Subscribe(context.Context, Subscription, EnvelopeHandler) (func(context.Context) error, error) {
	return func(context.Context) error { return nil }, nil
}

// ExamplePublishExternalize overrides, for one publish, the strategy of the
// type and of the container. Here the type is sent after commit, which
// sends nothing for a rolled-back transaction but loses the event if the
// process crashes between the commit and the send (at most once on a
// crash): a transport with an outbox and ExternalizeOutbox avoid that.
func ExamplePublishExternalize() {
	injector := NewWithOpts(&InjectorOpts{EventTransport: printingTransport{}, TxSource: exampleTxSource{}})
	_ = RegisterEventType[exampleOrderPlaced](injector, "orders.placed", EventExternalize(ExternalizeAfterCommit))

	tx := &exampleTx{}
	ctx := context.WithValue(context.Background(), exampleTxKey{}, tx)
	_ = Publish(ctx, injector, exampleOrderPlaced{ID: "42"})
	fmt.Println("published in the transaction")
	tx.commit()

	_ = Publish(ctx, injector, exampleOrderPlaced{ID: "43"}, PublishExternalize(ExternalizeImmediate))
	fmt.Println("published immediately")

	// Output:
	// published in the transaction
	// send orders.placed to orders.placed
	// send orders.placed to orders.placed
	// published immediately
}

// ExampleInjectorOpts_eventExternalize shows the container default:
// ExternalizeAuto sends during Publish unless a transaction is bound and
// the transport has an outbox (TxEventSender). EventExternalize on a type,
// then PublishExternalize on a publish, override it; an outbox with no
// transaction fails unless EventOutboxFallback allows sending at once.
func ExampleInjectorOpts_eventExternalize() {
	injector := NewWithOpts(&InjectorOpts{
		EventTransport:      printingTransport{},
		EventExternalize:    ExternalizeAuto, // the zero value
		EventOutboxFallback: OutboxFallbackFail,
	})
	_ = RegisterEventType[exampleOrderPlaced](injector, "orders.placed", EventExternalized())

	err := Publish(context.Background(), injector, exampleOrderPlaced{ID: "42"})
	fmt.Println("auto:", err)

	err = Publish(context.Background(), injector, exampleOrderPlaced{ID: "43"}, PublishExternalize(ExternalizeOutbox))
	fmt.Println("outbox without TxEventSender:", errors.Is(err, ErrOutboxNotSupported))

	// Output:
	// send orders.placed to orders.placed
	// auto: <nil>
	// outbox without TxEventSender: true
}

// examplePublisherPort is the publishing port of an application layer: it
// names no doger type.
type examplePublisherPort interface {
	Publish(ctx context.Context, events ...any) error
}

// examplePlaceOrder is a use case: it publishes its events through its own
// port, inside the unit of work that bound the transaction into ctx.
type examplePlaceOrder struct{ events examplePublisherPort }

func (uc examplePlaceOrder) run(ctx context.Context, id string) error {
	// ... change state in the transaction of ctx ...
	return uc.events.Publish(ctx, exampleOrderPlaced{ID: id})
}

// ExamplePublisherFor wires a use case's publishing port to the container:
// PublisherFor returns a concrete handle satisfying the port. The events
// are published inside the unit of work, so a transactional strategy joins
// its transaction; publishing after the unit of work returned would not.
func ExamplePublisherFor() {
	injector := NewWithOpts(&InjectorOpts{TxSource: exampleTxSource{}})
	Listen(injector, func(_ context.Context, e exampleOrderPlaced) error {
		fmt.Println("email for", e.ID)
		return nil
	}, ListenerTxPhase(TxPhaseAfterCommit))

	useCase := examplePlaceOrder{events: PublisherFor(injector)}

	// the unit of work: begin, run the use case, commit
	tx := &exampleTx{}
	ctx := context.WithValue(context.Background(), exampleTxKey{}, tx)
	if err := useCase.run(ctx, "42"); err != nil {
		fmt.Println("rollback:", err)
		return
	}
	fmt.Println("commit")
	tx.commit()

	// Output:
	// commit
	// email for 42
}

// ExampleNewMemoryEventTransport connects two containers in one process:
// "orders" externalizes OrderPlaced, "billing" handles it with a remote
// listener. With several billing instances, DeliverOnce would reach one of
// them per event; DeliverAll would reach each one.
func ExampleNewMemoryEventTransport() {
	transport := NewMemoryEventTransport()

	orders := NewWithOpts(&InjectorOpts{EventTransport: transport})
	_ = RegisterEventType[exampleOrderPlaced](orders, "orders.placed", EventExternalized())

	billing := NewWithOpts(&InjectorOpts{EventTransport: transport})
	_ = RegisterEventType[exampleOrderPlaced](billing, "orders.placed")
	handled := make(chan string, 1)
	Listen(billing, func(_ context.Context, e exampleOrderPlaced) error {
		handled <- e.ID
		return nil
	}, ListenerID("invoice"), ListenerDelivery(DeliverOnce))
	_ = billing.Start() // subscribes the remote listener

	_ = Publish(context.Background(), orders, exampleOrderPlaced{ID: "42"})
	fmt.Println("invoice for", <-handled)

	billing.Shutdown() // stops the subscription, waiting for its handlers
	orders.Shutdown()

	// Output:
	// invoice for 42
}

// ExampleListenerDelivery declares remote listeners on a service and a
// function: Start subscribes them through the transport, in-process
// Publish never reaches them, and each subscribes as group
// EventGroupPrefix + listener ID (with "/" + InstanceID for DeliverAll).
func ExampleListenerDelivery() {
	transport := NewMemoryEventTransport()
	injector := NewWithOpts(&InjectorOpts{EventTransport: transport, InstanceID: "pod-0"})
	_ = RegisterEventType[exampleOrderPlaced](injector, "orders.placed", EventExternalized())

	handled := make(chan string, 2)
	Listen(injector, func(_ context.Context, e exampleOrderPlaced) error {
		handled <- "every instance: " + e.ID
		return nil
	}, ListenerID("cache"), ListenerDelivery(DeliverAll))
	Listen(injector, func(_ context.Context, e exampleOrderPlaced) error {
		handled <- "one instance: " + e.ID
		return nil
	}, ListenerID("invoice"), ListenerDelivery(DeliverOnce))
	_ = injector.Start()

	_ = Publish(context.Background(), injector, exampleOrderPlaced{ID: "42"})
	received := []string{<-handled, <-handled}
	injector.Shutdown()
	slices.Sort(received) // the two subscriptions handle concurrently
	fmt.Println(received[0])
	fmt.Println(received[1])

	// Output:
	// every instance: 42
	// one instance: 42
}

// ExamplePermanentError marks a failure no redelivery can fix: the
// transport stops redelivering it and dead-letters or drops it, here
// logged by the in-memory transport after one attempt.
func ExamplePermanentError() {
	dropped := make(chan error, 1)
	transport := NewMemoryEventTransport(MemoryLogger(func(_ string, args ...any) {
		err, _ := args[len(args)-1].(error)
		dropped <- err
	}))
	injector := NewWithOpts(&InjectorOpts{EventTransport: transport})
	_ = RegisterEventType[exampleOrderPlaced](injector, "orders.placed", EventExternalized())
	Listen(injector, func(_ context.Context, e exampleOrderPlaced) error {
		fmt.Println("handling", e.ID)
		return PermanentError(errExampleInvalidOrder)
	}, ListenerID("invoice"), ListenerDelivery(DeliverOnce))
	_ = injector.Start()

	_ = Publish(context.Background(), injector, exampleOrderPlaced{ID: "42"})
	fmt.Println("dropped:", errors.Is(<-dropped, errExampleInvalidOrder))
	injector.Shutdown()

	// Output:
	// handling 42
	// dropped: true
}

// errExampleInvalidOrder is the permanent failure of ExamplePermanentError.
var errExampleInvalidOrder = errors.New("invalid order")
