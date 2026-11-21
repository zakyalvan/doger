package main

import (
	"context"
	"fmt"
	"sync"
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

// Invoicer runs in the billing service. It is a remote listener of
// OrderCreatedEvent with doger.ListenerDelivery(doger.DeliverOnce): every
// billing instance subscribes to the group "invoice", and each order is
// invoiced by one of them only.
type Invoicer struct {
	Instance string
	Logger   *Logger
	Invoiced *Ledger
}

func (i *Invoicer) OnOrderCreated(_ context.Context, e OrderCreatedEvent) error {
	i.Logger.Log(fmt.Sprintf("Billing %s: invoice for order %s", i.Instance, e.OrderID))
	i.Invoiced.Record(e.OrderID, i.Instance)
	return nil
}

// Ledger records which instance invoiced each order.
type Ledger struct {
	mu       sync.Mutex
	invoices map[string][]string // order ID -> instances
	done     chan struct{}
	doneOnce sync.Once
	want     int
}

// NewLedger returns a ledger whose Done channel is closed once want orders
// were invoiced.
func NewLedger(want int) *Ledger {
	return &Ledger{invoices: map[string][]string{}, done: make(chan struct{}), want: want}
}

// Record records that instance invoiced order.
func (l *Ledger) Record(order, instance string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.invoices[order] = append(l.invoices[order], instance)
	if len(l.invoices) >= l.want { // a redelivered order records again
		l.doneOnce.Do(func() { close(l.done) })
	}
}

// Done is closed once every expected order was invoiced.
func (l *Ledger) Done() <-chan struct{} { return l.done }

// Invoices returns the instances that invoiced each order.
func (l *Ledger) Invoices() map[string][]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	invoices := make(map[string][]string, len(l.invoices))
	for order, instances := range l.invoices {
		invoices[order] = append([]string{}, instances...)
	}
	return invoices
}
