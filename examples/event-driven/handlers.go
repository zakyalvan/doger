package main

import (
	"context"
	"fmt"
)

// UserEventHandler listens to UserCreatedEvent through its OnEvent method:
// it is registered with doger.AsListener[UserCreatedEvent]().
type UserEventHandler struct {
	Logger *Logger
}

func (h *UserEventHandler) OnEvent(_ context.Context, e UserCreatedEvent) error {
	h.Logger.Log(fmt.Sprintf("User created: ID=%s, Username=%s, Email=%s", e.UserID, e.Username, e.Email))
	return nil
}

// Notifier listens to two event types, one method each: it is registered
// with doger.ListenMethod((*Notifier).OnOrderCreated) and
// doger.ListenMethod((*Notifier).OnPaymentProcessed).
type Notifier struct {
	Logger *Logger
}

func (n *Notifier) OnOrderCreated(_ context.Context, e OrderCreatedEvent) error {
	n.Logger.Log(fmt.Sprintf("Notify: order %s created for user %s, amount %.2f", e.OrderID, e.UserID, e.Amount))
	return nil
}

func (n *Notifier) OnPaymentProcessed(_ context.Context, e PaymentProcessedEvent) error {
	n.Logger.Log(fmt.Sprintf("Notify: payment %s for order %s is %s", e.PaymentID, e.OrderID, e.Status))
	return nil
}

// Analytics records every payment off the publisher's goroutine: it is
// registered with doger.ListenerAsync(), and root shutdown waits for it.
type Analytics struct {
	Logger *Logger
}

func (a *Analytics) OnEvent(_ context.Context, e PaymentProcessedEvent) error {
	a.Logger.Log(fmt.Sprintf("Analytics: recorded payment of %.2f", e.Amount))
	return nil
}
