package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/kartaladev/doger"
)

// Logger represents a logging service, safe for concurrent use since async
// and remote listeners log too.
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
// no doger type; doger.PublisherFor satisfies it.
type EventPublisher interface {
	Publish(ctx context.Context, events ...any) error
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

// OrderService creates orders and publishes OrderCreatedEvent, which also
// leaves the process for the billing service.
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

// provideServices registers the order service's services in injector.
func provideServices(injector doger.Injector) {
	doger.Provide(injector, func(doger.Injector) (*Logger, error) {
		return &Logger{Level: "INFO"}, nil
	})
	doger.Provide(injector, func(i doger.Injector) (*UserService, error) {
		return &UserService{Events: doger.PublisherFor(i), Logger: doger.MustInvoke[*Logger](i)}, nil
	})
	doger.Provide(injector, func(i doger.Injector) (*OrderService, error) {
		return &OrderService{Events: doger.PublisherFor(i), Logger: doger.MustInvoke[*Logger](i)}, nil
	})
	doger.Provide(injector, func(i doger.Injector) (*PaymentService, error) {
		return &PaymentService{Events: doger.PublisherFor(i), Logger: doger.MustInvoke[*Logger](i)}, nil
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
