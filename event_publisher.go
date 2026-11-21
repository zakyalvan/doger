package doger

import "context"

// Publisher publishes events through one injector. It lets an application
// implement its own publishing port, declared in its application layer as
// an interface with the method Publish(ctx context.Context, events ...any)
// error, without its domain importing doger. It holds only the injector and
// is safe for concurrent use.
type Publisher struct {
	injector Injector
}

// PublisherFor returns the Publisher of i. A nil i makes every Publish fail
// with an error matching [ErrNilInjector].
func PublisherFor(i Injector) *Publisher {
	return &Publisher{injector: i}
}

// Publish publishes events in order, each with [Publish](ctx, i, event),
// and stops at the first error, which it returns: the events of one use
// case usually belong to one transaction, which the caller then rolls back.
// No events returns nil. Bind the transaction into ctx before calling it,
// so that a transactional externalization joins it.
func (p *Publisher) Publish(ctx context.Context, events ...any) error {
	for _, event := range events {
		if err := Publish(ctx, p.injector, event); err != nil {
			return err
		}
	}

	return nil
}
