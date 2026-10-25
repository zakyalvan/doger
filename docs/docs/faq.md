---
id: faq
title: ❓ FAQ
description: Frequently asked questions about doger — reflection, uber/fx comparison, generics, mocking, circular dependencies, and production readiness.
sidebar_position: 5
---

import Head from '@docusaurus/Head';

<Head>
  <script type="application/ld+json">
    {JSON.stringify({
      '@context': 'https://schema.org',
      '@type': 'FAQPage',
      mainEntity: [
        {
          '@type': 'Question',
          name: 'Does doger use reflection?',
          acceptedAnswer: {
            '@type': 'Answer',
            text: 'No. doger resolves and injects services using Go generics (Go 1.27+), so type checks happen at compile time. The only exception is doger.InvokeStruct, an opt-in helper that uses reflection to populate struct fields tagged with `do:""`.',
          },
        },
        {
          '@type': 'Question',
          name: 'How is doger different from uber/fx?',
          acceptedAnswer: {
            '@type': 'Answer',
            text: 'uber/fx resolves dependencies at runtime via reflection (through uber/dig) and adds an application lifecycle on top. doger resolves services through generics instead of reflection, and ships its own lifecycle primitives (health checks, graceful shutdown, hooks) without pulling in dig or fx as dependencies.',
          },
        },
        {
          '@type': 'Question',
          name: 'Can I use doger without generics?',
          acceptedAnswer: {
            '@type': 'Answer',
            text: 'No, doger requires Go 1.27 or later because its entire API — Provide, Invoke, Override, As — is built on generic type parameters. There is no reflection-based fallback for older Go versions.',
          },
        },
        {
          '@type': 'Question',
          name: 'How do I mock a service in tests?',
          acceptedAnswer: {
            '@type': 'Answer',
            text: 'Clone the injector with Clone() or CloneWithOpts(), then call doger.Override (or doger.OverrideNamed / doger.OverrideValue) to replace a service with a test double before invoking it. For whole packages, ship a second Package variant exposing mocks behind the same interfaces.',
          },
        },
        {
          '@type': 'Question',
          name: 'Does doger detect circular dependencies?',
          acceptedAnswer: {
            '@type': 'Answer',
            text: 'Yes. Services must be invoked in a Directed Acyclic Graph (DAG); doger tracks the invocation chain through internal virtual scopes and returns an error as soon as a cycle is detected, instead of deadlocking or overflowing the stack.',
          },
        },
        {
          '@type': 'Question',
          name: 'Is doger production-ready?',
          acceptedAnswer: {
            '@type': 'Answer',
            text: 'doger ships built-in health checks, dependency-aware parallel shutdown, lifecycle hooks, and a debug Web UI, inherited from samber/do v2. doger itself is pre-1.0 (v0): until v1.0.0, a minor release may contain breaking changes.',
          },
        },
        {
          '@type': 'Question',
          name: 'How do I gracefully shut down services?',
          acceptedAnswer: {
            '@type': 'Answer',
            text: 'Call injector.Shutdown() or injector.ShutdownOnSignals(syscall.SIGTERM, os.Interrupt) on the root scope. Every service implementing the Shutdowner interface is stopped in reverse invocation order, and child scopes shut down recursively.',
          },
        },
        {
          '@type': 'Question',
          name: 'What is a scope in doger?',
          acceptedAnswer: {
            '@type': 'Answer',
            text: 'A scope is a module with restricted visibility: it can see services from its ancestor scopes but not from its siblings. Applications typically keep a root scope for shared infrastructure and dedicated child scopes for business logic.',
          },
        },
      ],
    })}
  </script>
</Head>

# FAQ

Answers to common questions about `doger`. For terminology, see the [glossary](./glossary.md); for a broader introduction, see [Dependency Injection in Go](./about.md).

## Does doger use reflection? {#does-samberdo-use-reflection}

No. `doger` resolves and injects services using Go generics (Go 1.27+), so type checks happen at compile time. The only exception is [`doger.InvokeStruct`](./service-invocation/service-invocation.md#auto-magically-load-a-service), an opt-in helper that uses reflection to populate struct fields tagged with `` `do:""` ``.

## How is doger different from uber/fx? {#how-is-samberdo-different-from-uberfx}

`uber/fx` resolves dependencies at runtime via reflection (through `uber/dig`) and adds an application lifecycle on top. `doger` resolves services through generics instead of reflection, and ships its own lifecycle primitives ([health checks](./service-lifecycle/healthchecker.md), [graceful shutdown](./service-lifecycle/shutdowner.md), hooks) without pulling in `dig` or `fx` as dependencies. See the full [do vs uber/fx comparison](./comparison/samber-do-vs-uber-fx.md).

## Can I use doger without generics? {#can-i-use-samberdo-without-generics}

No, `doger` requires Go 1.27 or later because its entire API — `Provide`, `Invoke`, `Override`, `As` — is built on generic type parameters. There is no reflection-based fallback for older Go versions.

## How do I mock a service in tests? {#how-do-i-mock-a-service-in-tests}

[Clone the injector](./container/clone.md) with `Clone()` or `CloneWithOpts()`, then call `doger.Override` (or `doger.OverrideNamed` / `doger.OverrideValue`) to replace a service with a test double before invoking it. For whole packages, ship a second `Package` variant exposing mocks behind the same interfaces — see [package loading](./service-registration/package-loading.md#testing-and-mocking).

## Does doger detect circular dependencies? {#does-samberdo-detect-circular-dependencies}

Yes. Services must be invoked in a Directed Acyclic Graph (DAG); `doger` tracks the invocation chain through internal virtual scopes and returns an error as soon as a cycle is detected, instead of deadlocking or overflowing the stack. Use [service dependencies](./troubleshooting/service-dependencies.md) to inspect a suspicious chain.

## Is doger production-ready? {#is-samberdo-production-ready}

`doger` ships built-in health checks, dependency-aware parallel shutdown, lifecycle hooks, and a debug Web UI, inherited from `samber/do` v2. `doger` itself is pre-1.0 (v0): until v1.0.0, a minor release may contain breaking changes. Coming from `samber/do`? See [Migrating from samber/do](./migrating/migrating-from-samber-do.md); for upstream's own v1 to v2 upgrade, see the [v1 to v2 upgrade guide](./upgrading/from-v1-x-to-v2.md).

## How do I gracefully shut down services? {#how-do-i-gracefully-shut-down-services}

Call `injector.Shutdown()` or `injector.ShutdownOnSignals(syscall.SIGTERM, os.Interrupt)` on the root scope. Every service implementing the [`Shutdowner` interface](./service-lifecycle/shutdowner.md) is stopped in reverse invocation order, and child scopes shut down recursively.

## What is a scope in doger? {#what-is-a-scope-in-samberdo}

A [scope](./container/scope.md) is a module with restricted visibility: it can see services from its ancestor scopes but not from its siblings. Applications typically keep a root scope for shared infrastructure and dedicated child scopes for business logic.
