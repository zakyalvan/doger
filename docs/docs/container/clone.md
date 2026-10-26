---
title: Clone
description: Clone a doger root scope to reset invoked service state between tests while keeping the same service registrations.
sidebar_position: 3
---

# Clone

`Clone()` copies a root scope's service **registrations** into a new root scope, without carrying over any already-**invoked** singleton state. It only works on `*doger.RootScope`; regular and virtual scopes cannot be cloned directly.

This is the recommended way to isolate tests: register your services (and mocks) once, then clone the injector for each test case instead of rebuilding it from scratch.

```go
injector := doger.New()

Provide[*Car](i, NewCar)
Provide[*Engine](i, NewEngine)

// reset scope
injector = injector.Clone()
```

Because the clone starts with no invoked services, the next call to `doger.Invoke` re-runs the provider functions and builds fresh instances — nothing is shared with the original injector.

## Clone with options {#clone-with-options}

`CloneWithOpts` clones a root scope while also overriding its [container options](./options.md), such as the struct tag key used for `doger.InvokeStruct`. Combined with `doger.Override`, it's the standard pattern for swapping a real dependency with a mock in a single test:

```go
injector := doger.New()

Provide[*Car](i, NewCar)
Provide[Engine](i, NewEngine)

// clone
injector = injector.CloneWithOpts(&doger.InjectorOpts{
    StructTagKey: "di",
})

// replace Engine by *MockEngine
doger.Override[Engine](injector, func (i doger.Injector) (Engine, error) {
    return &MockEngine{}, nil
})
```

## What a clone keeps {#what-a-clone-keeps}

A clone keeps the service registrations and the [component kind](../service-lifecycle/component-kinds.md) handlers, including a replaced `Runnable` handler, with their stages and order. Handling a kind on the clone replaces its handler on the clone only. `Clone()` also keeps the container options and hooks; `CloneWithOpts(opts)` uses only `opts`.

A clone does not keep invoked instances or the lifecycle state: it starts and shuts down on its own.

## When to use Clone {#when-to-use-clone}

- **Table-driven tests**: clone once per test case to avoid state leaking between them.
- **Parallel tests**: each goroutine gets its own instances from a shared registration set.
- **Feature flags / A-B setups**: clone a base injector and override a handful of services per variant.

## Common mistake: cloning the wrong scope {#common-mistake-cloning-the-wrong-scope}

`Clone()` and `CloneWithOpts()` are only available on `*doger.RootScope`. If you're holding a child scope (from `injector.Scope("name")`) and try to reset it between tests, clone the root scope instead and re-derive the child scope from the clone — cloning a child scope directly isn't supported, since a scope's identity is tied to its position in the parent's tree.

For mocking whole packages instead of individual services, see [package loading](../service-registration/package-loading.md#testing-and-mocking). For a broader picture of how scopes relate to each other, see [Scopes](./scope.md).
