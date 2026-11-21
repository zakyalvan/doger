package main

import (
	"context"
	"fmt"
)

// Application runs a user journey that publishes events.
type Application struct {
	UserService    *UserService
	OrderService   *OrderService
	PaymentService *PaymentService
	Logger         *Logger
}

// Run creates a user, then orders orders and pays for each.
func (app *Application) Run(ctx context.Context, orders int) error {
	app.Logger.Log("=== User Journey Simulation ===")

	if err := app.UserService.CreateUser(ctx, "john_doe", "john@example.com"); err != nil {
		return fmt.Errorf("creating user: %w", err)
	}
	for n := range orders {
		orderID := fmt.Sprintf("order-%d", n+1)
		if err := app.OrderService.CreateOrder(ctx, orderID, "user-123", 99.99); err != nil {
			return fmt.Errorf("creating order: %w", err)
		}
		if err := app.PaymentService.ProcessPayment(ctx, orderID, 99.99); err != nil {
			return fmt.Errorf("processing payment: %w", err)
		}
	}

	app.Logger.Log("User journey completed successfully!")

	return nil
}
