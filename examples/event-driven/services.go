package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/kartaladev/doger"
)

// Logger represents a logging service, safe for concurrent use since async
// listeners log too.
type Logger struct {
	Level string
	mu    sync.Mutex
}

func (l *Logger) Log(message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Printf("[%s] %s\n", l.Level, message)
}

// EventPublisher is the publishing port of the application layer: it names
// no doger type; publisherFor satisfies it with doger.Publish.
type EventPublisher interface {
	Publish(ctx context.Context, events ...any) error
}

// publisherFor returns the EventPublisher publishing through injector.
func publisherFor(injector doger.Injector) EventPublisher {
	return injectorPublisher{injector: injector}
}

// injectorPublisher publishes each event with doger.Publish, in order,
// stopping at the first error.
type injectorPublisher struct {
	injector doger.Injector
}

func (p injectorPublisher) Publish(ctx context.Context, events ...any) error {
	for _, event := range events {
		if err := doger.Publish(ctx, p.injector, event); err != nil {
			return err
		}
	}

	return nil
}

// UserService creates users and publishes UserCreatedEvent.
type UserService struct {
	Events EventPublisher
	Logger *Logger
}

func (u *UserService) CreateUser(ctx context.Context, username, email string) error {
	u.Logger.Log(fmt.Sprintf("Creating user: %s (%s)", username, email))

	return u.Events.Publish(ctx, UserCreatedEvent{UserID: "user-123", Username: username, Email: email})
}

// OrderService creates orders and publishes OrderCreatedEvent.
type OrderService struct {
	Events EventPublisher
	Logger *Logger
}

func (o *OrderService) CreateOrder(ctx context.Context, orderID, userID string, amount float64) error {
	o.Logger.Log(fmt.Sprintf("Creating order %s for user: %s, amount: %.2f", orderID, userID, amount))

	return o.Events.Publish(ctx, OrderCreatedEvent{OrderID: orderID, UserID: userID, Amount: amount})
}

// PaymentService processes payments and publishes PaymentProcessedEvent.
type PaymentService struct {
	Events EventPublisher
	Logger *Logger
}

func (p *PaymentService) ProcessPayment(ctx context.Context, orderID string, amount float64) error {
	p.Logger.Log(fmt.Sprintf("Processing payment for order: %s, amount: %.2f", orderID, amount))

	return p.Events.Publish(ctx, PaymentProcessedEvent{PaymentID: "payment-" + orderID, OrderID: orderID, Amount: amount, Status: "completed"})
}

// provideServices registers the application's services in injector.
func provideServices(injector doger.Injector) {
	doger.Provide(injector, func(doger.Injector) (*Logger, error) {
		return &Logger{Level: "INFO"}, nil
	})
	doger.Provide(injector, func(i doger.Injector) (*UserService, error) {
		return &UserService{Events: publisherFor(i), Logger: doger.MustInvoke[*Logger](i)}, nil
	})
	doger.Provide(injector, func(i doger.Injector) (*OrderService, error) {
		return &OrderService{Events: publisherFor(i), Logger: doger.MustInvoke[*Logger](i)}, nil
	})
	doger.Provide(injector, func(i doger.Injector) (*PaymentService, error) {
		return &PaymentService{Events: publisherFor(i), Logger: doger.MustInvoke[*Logger](i)}, nil
	})
	doger.Provide(injector, func(i doger.Injector) (*Application, error) {
		return &Application{
			UserService:    doger.MustInvoke[*UserService](i),
			OrderService:   doger.MustInvoke[*OrderService](i),
			PaymentService: doger.MustInvoke[*PaymentService](i),
			Logger:         doger.MustInvoke[*Logger](i),
		}, nil
	})
}
