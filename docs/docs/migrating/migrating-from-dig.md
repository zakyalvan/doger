---
title: From Uber Dig
description: Migrate your dependency injection from Uber Dig to doger
sidebar_position: 3
---

# Migrating from Uber Dig

This guide will help you migrate your dependency injection setup from Uber Dig to `doger`.

## Overview {#overview}

Uber Dig and `doger` are both runtime dependency injection libraries for Go, but they have different APIs and features:

- **Uber Dig**: Uses a builder pattern with `dig.New()` and `Provide`/`Invoke` methods
- **doger**: Uses a fluent API with `doger.New()` and similar `Provide`/`Invoke` methods

## Key Differences {#key-differences}

| Feature               | Uber Dig                      | doger                          |
| --------------------- | ----------------------------- | ------------------------------ |
| **API Style**         | Builder pattern               | Fluent API                     |
| **Error Handling**    | Returns errors from `Build()` | Returns errors from `Invoke()` |
| **Service Lifecycle** | Basic                         | Full lifecycle management      |
| **Scoped Services**   | Limited                       | Full scoping support           |
| **Health Checks**     | Not built-in                  | Built-in support               |
| **Graceful Shutdown** | Manual                        | Built-in support               |

## Migration Steps {#migration-steps}

### 1. Remove Dig Dependencies {#1-remove-dig-dependencies}

Remove Dig from your `go.mod`:

```bash
go mod edit -droprequire go.uber.org/dig
```

Remove Dig imports from your code.

### 2. Replace Container Creation {#2-replace-container-creation}

**Before (Dig):**

```go
import "go.uber.org/dig"

func main() {
    container := dig.New()
    // ... setup container
}
```

**After (doger):**

```go
import "github.com/kartaladev/doger"

func main() {
    injector := doger.New()
    // ... setup injector
}
```

### 3. Update Service Registration {#3-update-service-registration}

**Before (Dig):**

```go
err := container.Provide(NewDatabase)
if err != nil {
    log.Fatal(err)
}

err = container.Provide(NewUserService)
if err != nil {
    log.Fatal(err)
}
```

**After (doger):**

```go
doger.Provide(injector, NewDatabase)
doger.Provide(injector, NewUserService)
```

### 4. Update Service Invocation {#4-update-service-invocation}

**Before (Dig):**

```go
err := container.Invoke(func(app *App) {
    app.Run()
})
if err != nil {
    log.Fatal(err)
}
```

**After (doger):**

```go
app, err := doger.Invoke[*App](injector)
if err != nil {
    log.Fatal(err)
}
// or
app := doger.MustInvoke[*App](injector)

app.Run()
```

### 5. Handle Constructor Functions {#5-handle-constructor-functions}

Dig and `doger` use different constructor function signatures. `doger` constructors receive `doger.Injector` as the first parameter and return an additional error:

**Before (Dig):**

```go
func NewUserService(db *Database) *UserService {
    return &UserService{db: db}
}

func NewDatabase(config *Config) *Database {
    return &Database{config: config}
}
```

**After (doger):**

```go
func NewUserService(i doger.Injector) (*UserService, error) {
    // if service is not found, `doger` will catch the panic and return an `error`
    db := doger.MustInvoke[*Database](i)
    return &UserService{db: db}, nil
}

func NewDatabase(i doger.Injector) (*Database, error) {
    config := doger.MustInvoke[*Config](i)
    return &Database{config: config}, nil
}
```

### 6. Update Interface Bindings {#6-update-interface-bindings}

**Before (Dig):**

```go
err := container.Provide(func() Repository {
    return &UserRepository{}
})
```

**After (doger):**

```go
doger.Provide(injector, func(i doger.Injector) (Repository, error) {
    return &UserRepository{}, nil
})
```

Or declare an explicit binding:

```go
// Register the concrete implementation
doger.Provide(injector, func(i doger.Injector) (*UserRepository, error) {
    repo := doger.MustInvoke[*UserRepository](i)
    return repo, nil
})

// Register the interface binding
doger.As[*UserRepository, Repository](i)
```

Or bind on service loading:

```go
// Register the concrete implementation
doger.Provide(injector, func(i doger.Injector) (*UserRepository, error) {
    repo := doger.MustInvoke[*UserRepository](i)
    return repo, nil
})

// Find the matching concrete type during invocation
userRepository, err := doger.InvokeAs[Repository](i)
```

## Advanced Migration Patterns {#advanced-migration-patterns}

### Provider Groups {#provider-groups}

**Before (Dig):**

```go
type Result struct {
    dig.Out
    Service1 *Service1 `name:"service1"`
    Service2 *Service2 `name:"service2"`
}

func ProvideServices() Result {
    return Result{
        Service1: NewService1(),
        Service2: NewService2(),
    }
}

err := container.Provide(ProvideServices)
```

**After (doger):**

```go
type Result struct {
    Service1 *Service1 `do:""`
    Service2 *Service2 `do:""`
}

doger.Provide(injector, NewService1)
doger.Provide(injector, NewService2)
doger.Provide(injector, doger.InvokeStruct[*Result])
```

### Parameter Structs {#parameter-structs}

**Before (Dig):**

```go
type Params struct {
    dig.In
    DB      *Database
    Logger  *Logger
}

func NewUserService(params Params) *UserService {
    return &UserService{
        db:     params.DB,
        logger: params.Logger,
    }
}
```

**After (doger):**

```go
func NewUserService(i doger.Injector) (*UserService, error) {
    db := doger.MustInvoke[*Database](i)
    logger := doger.MustInvoke[*Logger](i)

    return &UserService{
        db:     db,
        logger: logger,
    }, nil
}
```

### Optional Dependencies {#optional-dependencies}

**Before (Dig):**

```go
type Params struct {
    dig.In
    DB      *Database
    Cache   *Cache `optional:"true"`
}

func NewUserService(params Params) *UserService {
    return &UserService{
        db:    params.DB,
        cache: params.Cache, // nil if not provided
    }
}
```

**After (doger):**

```go
func NewUserService(i doger.Injector) (*UserService, error) {
    db := doger.MustInvoke[*Database](i)
    cache, _ := doger.Invoke[*Cache](i) // error is ignored
    return &UserService{
        db: db,
        cache: cache,
    }, nil
}

// Or use conditional registration
func RegisterServices(injector doger.Injector, useCache bool) {
    doger.Provide(injector, NewDatabase)
    if useCache {
        doger.Provide(injector, NewCache)
    }
    doger.Provide(injector, NewUserService)
}
```

### Value Groups {#value-groups}

**Before (Dig):**

```go
type Result struct {
    dig.Out
    Handler Handler `group:"handlers"`
}

func ProvideHandler1() Result {
    return Result{Handler: NewHandler1()}
}

func ProvideHandler2() Result {
    return Result{Handler: NewHandler2()}
}

type Params struct {
    dig.In
    Handlers []Handler `group:"handlers"`
}
```

**After (doger):**
Should be available starting do v2.1

## Complete Example {#complete-example}

**Before (Dig):**

```go
package main

import (
    "log"
    "go.uber.org/dig"
)

func main() {
    container := dig.New()

    err := container.Provide(NewDatabase)
    if err != nil {
        log.Fatal(err)
    }

    err = container.Provide(NewUserService)
    if err != nil {
        log.Fatal(err)
    }

    err = container.Provide(NewApp)
    if err != nil {
        log.Fatal(err)
    }

    err = container.Invoke(func(app *App) {
        app.Run()
    })
    if err != nil {
        log.Fatal(err)
    }
}
```

**After (doger):**

```go
package main

import (
    "log"
    "github.com/kartaladev/doger"
)

func main() {
    injector := doger.New()

    // Register services
    doger.Provide(injector, NewDatabase)
    doger.Provide(injector, NewUserService)
    doger.Provide(injector, NewApp)

    // Get the app and run it
    app, err := doger.Invoke[*App](injector)
    if err != nil {
        log.Fatal(err)
    }

    // Optional: Graceful shutdown
    defer injector.ShutdownOnSignals(syscall.SIGTERM, os.Interrupt)

    app.Run()
}
```

## Benefits of Migration {#benefits-of-migration}

1. **Simpler API** - More intuitive fluent API
2. **Better error messages** - More detailed error information
3. **Service lifecycle management** - Built-in shutdown and health checks
4. **Scoped services** - Full support for request-scoped services
5. **Minimal dependencies** - Pure Go, with two small libraries (`go-type-to-string`, `clockwork`)
6. **Better testing support** - Easier to mock and test

## Common Pitfalls {#common-pitfalls}

1. **Error handling** - `doger` returns errors from `Invoke()`, not `Provide()`
2. **Circular dependencies** - `doger` will detect and report these
3. **Interface bindings** - Use `doger.As` for interface implementations
4. **Panics vs errors** - Prefer `doger.MustInvoke` over `doger.Invoke` when a missing service should stop the program immediately instead of being handled as a recoverable error

## Testing {#testing}

Your existing tests should work with minimal changes:

```go
func TestUserService(t *testing.T) {
    injector := doger.New()

    // Register test dependencies
    doger.Provide(injector, NewMockDatabase)
    doger.Provide(injector, NewUserService)

    service := doger.MustInvoke[*UserService](injector)

    // Your test logic here
}
```

## Next Steps {#next-steps}

After migration, consider exploring these `doger` features:

- [Interface binding](/docs/service-invocation/accept-interfaces-return-structs.md)
- [Scoped Services](/docs/container/scope.md)
- [Health Checks](/docs/service-lifecycle/healthchecker.md)
- [Graceful Shutdown](/docs/service-lifecycle/shutdowner.md)
- [Dependency resolution troubleshooting](/docs/troubleshooting/scope-tree.md)
