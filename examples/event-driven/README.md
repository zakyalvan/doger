# Event-Driven Architecture Example

Publishes typed events and reacts to them in-process and across processes.

In the order service:

- a listener service registered with `doger.AsListener[UserCreatedEvent]()`;
- one service listening to two event types with `doger.ListenMethod`;
- an async listener (`doger.ListenerAsync()`), drained by root shutdown;
- a function listener registered with `doger.Listen`;
- services publishing through their own `EventPublisher` port, satisfied by
  `doger.PublisherFor`.

Across processes: `OrderCreatedEvent` is registered as `"orders.created"`
and externalized with `doger.EventExternalized()`. Two billing instances,
separate containers sharing one `doger.NewMemoryEventTransport()` as two
processes would share a broker, declare their `Invoicer` with
`doger.ListenerDelivery(doger.DeliverOnce)` and `doger.ListenerID("invoice")`:
both join the group "invoice", so each order is invoiced by exactly one of
them. Replace the in-memory transport with a broker or SQL transport to run
them as real processes.

```sh
go run .
go test ./...
```
