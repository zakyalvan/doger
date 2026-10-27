---
title: Shutdown context release
description: Changes to check when upgrading to the doger release that fixes the signal helper's shutdown context
sidebar_position: 4
---

# Upgrading to the shutdown context release

This release documents how a shutdown honors its context (see [Shutdown with a deadline](../service-lifecycle/shutdowner.md#shutdown-with-a-deadline)) and fixes `ShutdownOnSignalsWithContext`. No exported identifier changes. Services reached by `ShutdownWithContext` after its context ended are still skipped, as before; only the signal helper behaves differently.

## The signal helper's context only ends the wait for a signal {#signal-helper-context}

**Behavior change, `ShutdownOnSignalsWithContext` only.** The helper used to shut down with the context it was given:

- when that context ended before a signal arrived, it shut no built service down, returned a report whose errors were the context error of each service, and gave the `HookContainerClosing` and `HookContainerClosed` hooks the ended context;
- after a signal, the shutdown was bounded and cancelled by that context, so a context ending right after the signal (a `signal.NotifyContext` context for the same signals, or a deadline expiring at that moment) skipped the remaining services.

It now shuts down, in both cases, with a context that keeps the values of its context but has no cancellation or deadline, as `ShutdownOnSignals` does:

- every built service's `Shutdown` is called, and the report holds only the errors those calls return;
- the Closing and Closed hooks receive that context: its `Err()` is `nil` and it has no deadline;
- the shutdown is unbounded: a `Shutdown` that blocks now blocks the helper, where it used to return once the context ended (having skipped the remaining services).

The helper still returns the signal, or `nil` when the context ended first.

Migration: none, unless code relied on the helper shutting nothing down when its context ended, on the context's deadline bounding the shutdown after a signal, or on a Closing or Closed hook seeing that context end. To bound the shutdown, wait for the signal yourself and call `ShutdownWithContext` with your own deadline, as shown in [Shutdown with a deadline](../service-lifecycle/shutdowner.md#shutdown-with-a-deadline).
