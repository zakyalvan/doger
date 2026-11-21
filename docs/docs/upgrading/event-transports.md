---
title: Event transports release
description: Changes to check when upgrading to the doger release that adds event transports and remote listeners
sidebar_position: 7
---

# Upgrading to the event transports release

This release adds cross-process events: see [Event transports](../service-lifecycle/event-transports.md). Every exported identifier is new (`RegisterEventType` and its options, `Envelope`, `EventTransport`, `TxEventSender`, `NewMemoryEventTransport`, the externalization strategies, `ListenerDelivery`, `ListenerTxPhase`, `ListenerTxFallback`, `Tx`, `TxSource`, `PermanentError`, `PublisherFor`, the package `eventtransporttest`, new sentinels and `InjectorOpts` fields): nothing existing is renamed or removed, so there are **no breaking changes**.

Containers that register no externalized event type and no remote listener behave exactly as before: they resolve no transport and `Publish` does no new work.

If you planned on the superseded durable events proposal, `ListenerDurable()` becomes `RegisterEventType(..., EventExternalized())` on the publishing side, `ListenerDelivery(DeliverOnce)` on the listener, and a crash-safe transport.
