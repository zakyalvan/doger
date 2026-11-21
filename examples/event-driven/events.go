package main

import "github.com/kartaladev/doger"

// The events this example publishes. Any Go value is an event: listeners
// are matched by type, so there is no string topic and no `any` payload.

// UserCreatedEvent is published when a user is created. It stays
// in-process: it has no wire name.
type UserCreatedEvent struct {
	UserID   string
	Username string
	Email    string
}

// OrderCreatedEvent is published when an order is created. It is an
// integration event: it leaves the process as "orders.created".
type OrderCreatedEvent struct {
	OrderID string
	UserID  string
	Amount  float64
}

// PaymentProcessedEvent is published when a payment is processed.
type PaymentProcessedEvent struct {
	PaymentID string
	OrderID   string
	Amount    float64
	Status    string
}

// registerOrderEvents gives OrderCreatedEvent its wire name in root;
// externalized says whether root sends it to other processes.
func registerOrderEvents(root *doger.RootScope, externalized bool) error {
	var opts []doger.EventTypeOption
	if externalized {
		opts = append(opts, doger.EventExternalized())
	}

	return doger.RegisterEventType[OrderCreatedEvent](root, "orders.created", opts...)
}
