---
title: Interface binding
description: Bind and invoke Go interfaces with doger using implicit aliasing (doger.InvokeAs) or explicit binding (doger.As), following "accept interfaces, return structs".
sidebar_position: 2
---

# Interface binding

Interface binding in `doger` lets you register a concrete struct and invoke it through an interface it implements, either implicitly (`doger.InvokeAs`) or explicitly (`doger.As`). You have probably already heard the Go proverb "Accept interfaces, return structs". The basic idea is to let the consumer define the interfaces it uses while the producer returns concrete types.

This library offers a great API for assembling a large number of modules with loose coupling and a clear separation of concerns.

Aliases must be Go interfaces. It can be declared explicitly on injection using `doger.As()`, or invoked implicitly using `doger.InvokeAs()`.

1. **Implicit alias invocation**:
   - provide struct, invoke interface
   - `doger.InvokeAs()`
2. **Explicit alias injection**:
   - provide struct, bind interface, invoke interface
   - `doger.As()`

## Implicit invocation (preferred) {#implicit-invocation-preferred}

2 methods are available for implicit invocation:

- `doger.InvokeAs`
- `doger.MustInvokeAs`

Named invocation is not available for now. Feel free to open an issue to discuss your needs.

Implicit aliasing is recommended for production.

```go
type Metric interface {
    Inc()
}

type RequestPerSecond struct {
    counter int
}

func (r *RequestPerSecond) Inc() {
    r.counter++
}

i := doger.New()

// inject the struct
doger.Provide(i, func(i doger.Injector) (*RequestPerSecond, error) {
    return &RequestPerSecond{}, nil
})

// invoke using the Metric interface
metric := doger.MustInvokeAs[Metric](injector)
metric.Inc()    // <- r.counter will be incremented
```

**Play: https://go.dev/play/p/29gb2TJG4m5**

:::info

The first matching service in the scope tree is returned.

:::

:::warning

Invoking an implicit alias with a very simple interface signature might lead to loading the wrong service.

Eg: `fmt.Stringer` might match dozens of services in a container.

:::

### InvokeStruct and implicit aliasing {#invokestruct-and-implicit-aliasing}

When using `doger.InvokeStruct`, if a field is tagged with an empty tag (eg: `` `do:""` ``) and the inferred name does not match a registered service, the injector will fall back to the first registered service that is assignable to the field type, equivalent to `doger.InvokeAs[T]` resolution. Prefer explicit names when multiple assignable services exist.

## Explicit injection {#explicit-injection}

Explicit injection will be used in rare cases when you need to adapt to legacy code or change the catalog of services at runtime.

If you rely a lot on `doger.Scope`, explicit injection might be used to define custom aliases in the scope tree branches.

4 methods are available for explicit aliasing:

- `doger.As`
- `doger.MustAs`
- `doger.AsNamed`
- `doger.MustAsNamed`

```go
type Metric interface {
    Inc()
}

type RequestPerSecond struct {
    counter int
}

func (r *RequestPerSecond) Inc() {
    r.counter++
}

i := doger.New()

// inject the struct
doger.Provide(i, func(i doger.Injector) (*RequestPerSecond, error) {
    return &RequestPerSecond{}, nil
})

// explicit aliasing
err := doger.As[*RequestPerSecond, Metric](injector)
if err != nil {
    // ...
}

// invoke using the explicit alias
metric := doger.MustInvoke[Metric](injector)
metric.Inc()    // <- r.counter will be incremented
```

**Play: https://go.dev/play/p/_wGjnRJfwV8**

### Aliases and shutdown {#aliases-and-shutdown}

An alias never shuts its target down. Shutting a scope down, or calling `doger.Shutdown`/`doger.ShutdownNamed` on the alias name, removes the alias only. The target is shut down through its own registration, exactly once, even when the alias was invoked or lives in a child scope:

```go
child := injector.Scope("request")
doger.As[*RequestPerSecond, Metric](child)
_ = doger.MustInvoke[Metric](child)

child.Shutdown()    // removes the alias; *RequestPerSecond keeps running
injector.Shutdown() // shuts *RequestPerSecond down, once
```

To stop the target early, shut it down by its own name: `doger.ShutdownNamed(injector, doger.NameOf[*RequestPerSecond]())`.

:::danger

Explicit aliasing can lead to brittle designs and should be used very carefully. You've been warned. 😁

:::
