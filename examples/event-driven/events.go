package main

// The events this example publishes. Any Go value is an event: listeners
// are matched by type, so there is no string topic and no `any` payload.

// UserCreatedEvent is published when a user is created.
type UserCreatedEvent struct {
	UserID   string
	Username string
	Email    string
}

// OrderCreatedEvent is published when an order is created.
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
