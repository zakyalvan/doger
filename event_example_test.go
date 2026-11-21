package doger

import (
	"context"
	"errors"
	"fmt"
)

// exampleOrderPlaced is the event of the event examples.
type exampleOrderPlaced struct{ ID string }

// ExamplePublish shows the zero-config defaults: delivery is synchronous,
// listeners run in registration order, and the first failing listener
// stops dispatch, Publish returning a *ListenerError.
func ExamplePublish() {
	injector := New()

	Listen(injector, func(_ context.Context, e exampleOrderPlaced) error {
		fmt.Println("reserve stock for", e.ID)
		return nil
	})
	Listen(injector, func(context.Context, exampleOrderPlaced) error {
		return errors.New("payment declined")
	}, ListenerID("payment"))
	Listen(injector, func(context.Context, exampleOrderPlaced) error {
		fmt.Println("never reached")
		return nil
	})

	err := Publish(context.Background(), injector, exampleOrderPlaced{ID: "42"})

	if listenerErr, ok := errors.AsType[*ListenerError](err); ok {
		fmt.Println("failed listener:", listenerErr.Listener)
	}
	fmt.Println(err)

	// Output:
	// reserve stock for 42
	// failed listener: payment
	// doger: listener `payment` failed on event `doger.exampleOrderPlaced`: payment declined
}

// ExampleListen shows a function listener and its stop function: the first
// call unregisters the listener and reports true, later calls report false.
func ExampleListen() {
	injector := New()

	stop := Listen(injector, func(_ context.Context, e exampleOrderPlaced) error {
		fmt.Println("received", e.ID)
		return nil
	})

	_ = Publish(context.Background(), injector, exampleOrderPlaced{ID: "1"})
	fmt.Println(stop(), stop())
	_ = Publish(context.Background(), injector, exampleOrderPlaced{ID: "2"})

	// Output:
	// received 1
	// true false
}

// exampleMailer sends a mail for each placed order.
type exampleMailer struct{}

func (*exampleMailer) OnEvent(_ context.Context, e exampleOrderPlaced) error {
	fmt.Println("mail for order", e.ID)
	return nil
}

// ExampleAsListener shows a listener service: it is built by the first
// event that reaches it, not at registration.
func ExampleAsListener() {
	injector := New()

	Provide(injector, func(Injector) (*exampleMailer, error) {
		fmt.Println("build mailer")
		return &exampleMailer{}, nil
	}, AsListener[exampleOrderPlaced]())

	fmt.Println("registered")
	_ = Publish(context.Background(), injector, exampleOrderPlaced{ID: "1"})
	_ = Publish(context.Background(), injector, exampleOrderPlaced{ID: "2"})

	// Output:
	// registered
	// build mailer
	// mail for order 1
	// mail for order 2
}

// exampleOrderCancelled is the second event of the ListenMethod example.
type exampleOrderCancelled struct{ ID string }

// exampleBilling listens to two event types with one method each.
type exampleBilling struct{}

func (*exampleBilling) OnPlaced(_ context.Context, e exampleOrderPlaced) error {
	fmt.Println("invoice order", e.ID)
	return nil
}

func (*exampleBilling) OnCancelled(_ context.Context, e exampleOrderCancelled) error {
	fmt.Println("refund order", e.ID)
	return nil
}

// ExampleListenMethod shows one service listening to several event types
// through method expressions; it is built once.
func ExampleListenMethod() {
	injector := New()

	Provide(injector, func(Injector) (*exampleBilling, error) {
		fmt.Println("build billing")
		return &exampleBilling{}, nil
	},
		ListenMethod((*exampleBilling).OnPlaced),
		ListenMethod((*exampleBilling).OnCancelled),
	)

	_ = Publish(context.Background(), injector, exampleOrderPlaced{ID: "1"})
	_ = Publish(context.Background(), injector, exampleOrderCancelled{ID: "1"})

	// Output:
	// build billing
	// invoice order 1
	// refund order 1
}

// ExampleInjectorOpts_eventErrorMode overrides the fail-fast default:
// with EventContinue every listener runs and Publish joins their errors.
func ExampleInjectorOpts_eventErrorMode() {
	injector := NewWithOpts(&InjectorOpts{EventErrorMode: EventContinue})

	Listen(injector, func(context.Context, exampleOrderPlaced) error {
		return errors.New("stock unavailable")
	}, ListenerID("stock"))
	Listen(injector, func(context.Context, exampleOrderPlaced) error {
		fmt.Println("mail still sent")
		return nil
	})
	Listen(injector, func(context.Context, exampleOrderPlaced) error {
		return errors.New("ledger offline")
	}, ListenerID("ledger"))

	err := Publish(context.Background(), injector, exampleOrderPlaced{ID: "7"})
	fmt.Println(err)

	// Output:
	// mail still sent
	// doger: listener `stock` failed on event `doger.exampleOrderPlaced`: stock unavailable
	// doger: listener `ledger` failed on event `doger.exampleOrderPlaced`: ledger offline
}

// ExampleListenerAsync shows an async listener: Publish returns once the
// delivery is submitted, and the listener's error, which has no caller, goes
// to HookEventError. Root shutdown waits for deliveries in flight.
func ExampleListenerAsync() {
	injector := NewWithOpts(&InjectorOpts{
		HookEventError: func(_ context.Context, _ *RootScope, err error) {
			fmt.Println("reported:", err)
		},
	})

	Listen(injector, func(context.Context, exampleOrderPlaced) error {
		return errors.New("smtp unavailable")
	}, ListenerAsync(), ListenerID("mailer"))

	err := Publish(context.Background(), injector, exampleOrderPlaced{ID: "9"})
	fmt.Println("publish:", err)
	injector.Shutdown()

	// Output:
	// publish: <nil>
	// reported: doger: listener `mailer` failed on event `doger.exampleOrderPlaced`: smtp unavailable
}

// ExampleNewBoundedEventExecutor uses the default executor on its own: at
// most n tasks run at once, and Shutdown waits for the running ones.
func ExampleNewBoundedEventExecutor() {
	executor := NewBoundedEventExecutor(1)

	done := make(chan struct{})
	_ = executor.Submit(context.Background(), func(context.Context) {
		fmt.Println("task ran")
		close(done)
	})
	<-done

	fmt.Println("shutdown:", executor.Shutdown(context.Background()))
	err := executor.Submit(context.Background(), func(context.Context) {})
	fmt.Println("after shutdown:", errors.Is(err, ErrPublishAfterShutdown))

	// Output:
	// task ran
	// shutdown: <nil>
	// after shutdown: true
}
