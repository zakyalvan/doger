package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/kartaladev/doger"
)

func main() {
	if _, err := run(context.Background(), 4); err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
}

// run starts the order service and two billing instances, sharing one
// in-memory event transport as two processes would share a broker, runs a
// journey of orders orders, and returns which billing instance invoiced
// each order.
func run(ctx context.Context, orders int) (map[string][]string, error) {
	fmt.Println("=== Event-Driven Architecture Example ===")

	transport := doger.NewMemoryEventTransport()
	ledger := NewLedger(orders)

	shop, err := newOrderService(transport)
	if err != nil {
		return nil, err
	}
	defer shutdown(shop)
	for _, instance := range []string{"billing-1", "billing-2"} {
		billing, err := newBillingInstance(transport, instance, ledger)
		if err != nil {
			return nil, err
		}
		defer shutdown(billing) // stops its subscription, waiting for handlers in flight
	}

	app := doger.MustInvoke[*Application](shop)
	if err := app.Run(ctx, orders); err != nil {
		return nil, err
	}

	select {
	case <-ledger.Done():
	case <-time.After(5 * time.Second):
		return nil, errors.New("the billing instances did not invoice every order")
	}

	return ledger.Invoices(), nil
}

// shutdown shuts injector down, printing its errors.
func shutdown(injector *doger.RootScope) {
	if report := injector.Shutdown(); !report.Succeed {
		fmt.Println("shutdown:", report.Error())
	}
}

// newOrderService returns the started container of the order service: it
// listens in-process and sends OrderCreatedEvent through transport.
func newOrderService(transport doger.EventTransport) (*doger.RootScope, error) {
	injector := doger.NewWithOpts(&doger.InjectorOpts{EventTransport: transport})
	if err := registerOrderEvents(injector, true); err != nil {
		return nil, err
	}
	provideServices(injector)

	// Listeners: services that opt in at registration. None is built before
	// an event reaches it.
	doger.Provide(injector, func(i doger.Injector) (*UserEventHandler, error) {
		return &UserEventHandler{Logger: doger.MustInvoke[*Logger](i)}, nil
	}, doger.AsListener[UserCreatedEvent]())
	doger.Provide(injector, func(i doger.Injector) (*Notifier, error) {
		return &Notifier{Logger: doger.MustInvoke[*Logger](i)}, nil
	},
		doger.ListenMethod((*Notifier).OnOrderCreated),
		doger.ListenMethod((*Notifier).OnPaymentProcessed),
	)
	doger.Provide(injector, func(i doger.Injector) (*Analytics, error) {
		return &Analytics{Logger: doger.MustInvoke[*Logger](i)}, nil
	}, doger.AsListener[PaymentProcessedEvent](doger.ListenerAsync()))

	// A function listener, for cross-cutting concerns such as auditing.
	doger.Listen(injector, func(_ context.Context, e OrderCreatedEvent) error {
		doger.MustInvoke[*Logger](injector).Log("Audit: order " + e.OrderID)
		return nil
	})

	return injector, start(injector)
}

// newBillingInstance returns the started container of one billing
// instance: its Invoicer is a DeliverOnce remote listener of the group
// "invoice", so the instances share the orders between them.
func newBillingInstance(transport doger.EventTransport, instance string, ledger *Ledger) (*doger.RootScope, error) {
	injector := doger.NewWithOpts(&doger.InjectorOpts{EventTransport: transport, InstanceID: instance})
	if err := registerOrderEvents(injector, false); err != nil {
		return nil, err
	}
	doger.Provide(injector, func(doger.Injector) (*Logger, error) {
		return &Logger{Level: "INFO"}, nil
	})
	doger.Provide(injector, func(i doger.Injector) (*Invoicer, error) {
		return &Invoicer{Instance: instance, Logger: doger.MustInvoke[*Logger](i), Invoiced: ledger}, nil
	}, doger.ListenMethod((*Invoicer).OnOrderCreated, doger.ListenerID("invoice"), doger.ListenerDelivery(doger.DeliverOnce)))

	return injector, start(injector) // subscribes the Invoicer
}

// start starts injector. When Start fails, it shuts injector down, as the
// caller of a failed Start must, and returns the errors of both.
func start(injector *doger.RootScope) error {
	err := injector.Start()
	if err == nil {
		return nil
	}
	if report := injector.Shutdown(); !report.Succeed {
		return errors.Join(err, report)
	}

	return err
}
