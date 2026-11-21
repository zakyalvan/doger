# Event-Driven Architecture Example

Publishes typed events and reacts to them in-process:

- a listener service registered with `doger.AsListener[UserCreatedEvent]()`;
- one service listening to two event types with `doger.ListenMethod`;
- an async listener (`doger.ListenerAsync()`), drained by root shutdown;
- a function listener registered with `doger.Listen`;
- services publishing through their own `EventPublisher` port, backed by
  `doger.Publish`.

```sh
go run .
```
