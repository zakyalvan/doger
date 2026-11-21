package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/kartaladev/doger"
)

func main() {
	if err := run(context.Background(), 2); err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
}

// run starts the container, runs a journey of orders orders, and shuts the
// container down, which waits for the async listeners.
func run(ctx context.Context, orders int) error {
	fmt.Println("=== Event-Driven Architecture Example ===")

	injector := newContainer()
	if err := start(injector); err != nil {
		return err
	}
	defer shutdown(injector)

	app := doger.MustInvoke[*Application](injector)

	return app.Run(ctx, orders)
}

// shutdown shuts injector down, printing its errors.
func shutdown(injector *doger.RootScope) {
	if report := injector.Shutdown(); !report.Succeed {
		fmt.Println("shutdown:", report.Error())
	}
}

// newContainer returns the container of the application, with its
// services and listeners registered.
func newContainer() *doger.RootScope {
	injector := doger.New()
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

	return injector
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
