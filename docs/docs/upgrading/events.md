---
title: Events release
description: Changes to check when upgrading to the doger release that adds in-process events
sidebar_position: 6
---

# Upgrading to the events release

This release adds in-process events: see [Events](../service-lifecycle/events.md). Every exported identifier is new (`Publish`, `Listen`, `AsListener`, `ListenMethod`, the listener options, `ListenerError`, `EventExecutor`, the lifecycle event types and the `InjectorOpts` fields `EventErrorMode`, `EventAsyncParallelism`, `EventExecutor` and `HookEventError`): nothing existing is renamed or removed, so there are **no breaking changes**.

Containers that register no listener behave as before: lifecycle events are published after the existing container hooks, and publishing one nobody listens to costs one check. If you built your own event bus, replace it with `Publish` and listeners as shown in `examples/event-driven`.
