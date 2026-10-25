---
title: From samber/do
description: Migrate a Go application from samber/do v2 to doger, its fork
sidebar_position: 1
---

# Migrating from samber/do

doger is a fork of [samber/do](https://github.com/samber/do) by Samuel Berthe. Its API matches `samber/do` v2: every exported identifier, signature, option and default is unchanged. What changes is the module path, the package names, the service names the container infers from types declared in the library, and the minimum Go version.

## Module path and package name {#module-path-and-package-name}

The module `github.com/samber/do/v2` (package `do`) becomes `github.com/kartaladev/doger` (package `doger`). The `/v2` suffix is dropped: doger starts at v0.

```bash
go get github.com/kartaladev/doger@latest
```

Replace the import path, then either rename the `do.` qualifier to `doger.`:

```go
import "github.com/kartaladev/doger"

injector := doger.New()
doger.Provide(injector, NewDatabase)
```

or keep your existing `do.` call sites by aliasing the import:

```go
import do "github.com/kartaladev/doger"

injector := do.New()
do.Provide(injector, NewDatabase)
```

## Debug UI adapters {#debug-ui-adapters}

The HTTP adapters move under the root path and get a `doger` prefix, so they no longer clash with the framework they adapt:

| Old import path                      | New import path                          | New package    |
| ------------------------------------ | ---------------------------------------- | -------------- |
| `github.com/samber/do/v2/http`       | `github.com/kartaladev/doger/http`       | `dogerhttp`    |
| `github.com/samber/do/http/chi/v2`   | `github.com/kartaladev/doger/http/chi`   | `dogerchi`     |
| `github.com/samber/do/http/echo/v2`  | `github.com/kartaladev/doger/http/echo`  | `dogerecho`    |
| `github.com/samber/do/http/fiber/v2` | `github.com/kartaladev/doger/http/fiber` | `dogerfiber`   |
| `github.com/samber/do/http/gin/v2`   | `github.com/kartaladev/doger/http/gin`   | `dogergin`     |
| `github.com/samber/do/http/std/v2`   | `github.com/kartaladev/doger/http/std`   | `dogerhttpstd` |

Replace the paths. Existing import aliases keep working. The debug UI routes are unchanged and stay relative to the base path you pass, so a UI mounted at `/debug/do` stays there. Its pages now show `doger` branding.

## Inferred service names of library types {#inferred-service-names}

Service names inferred from types embed the import path. For types declared in the library, that path changes:

| Type            | samber/do v2                            | doger                                       |
| --------------- | --------------------------------------- | ------------------------------------------- |
| `*RootScope`    | `*github.com/samber/do/v2.RootScope`    | `*github.com/kartaladev/doger.RootScope`    |
| `Injector`      | `github.com/samber/do/v2.Injector`      | `github.com/kartaladev/doger.Injector`      |
| `Healthchecker` | `github.com/samber/do/v2.Healthchecker` | `github.com/kartaladev/doger.Healthchecker` |

These names appear in `doger.NameOf`, in service lists, in explain output and in error messages. Never hard-code them: compute them with `doger.NameOf[T]()`, or register such services under explicit names with `ProvideNamed*` and invoke them with `InvokeNamed`.

Names of your own types and explicitly named services do not change.

## Go 1.27 and panic(nil) {#go-1-27-and-panic-nil}

doger requires Go 1.27 or later. With the go command's default `GOTOOLCHAIN=auto`, building a module that requires doger downloads Go 1.27 automatically; with `GOTOOLCHAIN=local`, an older toolchain refuses the build.

Requiring doger raises your module's `go` line to 1.27, and GODEBUG defaults follow that line. If it was below 1.21, this turns on `panicnil=0`, which changes what an invocation returns when a provider panics with `nil`: it used to return the zero value and a `nil` error; it now returns the zero value and an error matching `*runtime.PanicNilError`. If your `go` line was already 1.21 or later, you already get this error. Other panic values are unchanged.

```go
doger.Provide(injector, func(i doger.Injector) (*Database, error) {
	panic(nil)
})

_, err := doger.Invoke[*Database](injector)

var panicNilErr *runtime.PanicNilError
errors.As(err, &panicNilErr) // true
```

To restore the old `(zero, nil)` result, run the program with `GODEBUG=panicnil=1`, or add the directive to your main package:

```go
//go:debug panicnil=1

package main
```

## Health checks on an ended context {#health-checks-on-an-ended-context}

In `samber/do`, a health check whose context had ended returned a random result: sometimes the timeout error, sometimes the check's own result, and sometimes a bare `context.Canceled` or `context.DeadlineExceeded`. doger decides it the same way every time:

- A service with a health check whose context is done when its result is observed returns the timeout error, whatever the check returned. The error matches `ErrHealthCheckTimeout` **and** the context error with `errors.Is`. Its message is unchanged, for example `DI: health check timeout: context canceled`.
- A service without a health check (a value without a `HealthCheck` method, a lazy service not invoked yet, a transient service) always reports `nil`, even on an ended context.

If you compared a health-check error with `==` against `context.Canceled` or `context.DeadlineExceeded`, use `errors.Is` instead:

```go
err := doger.HealthCheckNamedWithContext(ctx, injector, "db")
if errors.Is(err, context.Canceled) {
	// ...
}
```

## Registration options and run-once root shutdown {#registration-options-and-root-shutdown}

doger adds a trailing `...doger.ProvideOption` parameter to the registration functions and makes root `Shutdown()` run once per container. Calls compile unchanged; code that stores a registration function as a value, or relies on a second root `Shutdown()`, needs a small change. See [Upgrading to the Start lifecycle release](../upgrading/start-lifecycle.md).

## What stays the same {#what-stays-the-same}

- The struct injection tag key is still `do` (`do:""`), and you can still override it with `InjectorOpts.StructTagKey`.
- The messages of `ErrServiceNotFound`, `ErrServiceNotMatch`, `ErrCircularDependency` and `ErrHealthCheckTimeout` keep their `DI: ` prefix, and still match with `errors.Is`.
- Every other exported identifier, signature, option and default, apart from the additions above.

doger also adds one option, `InjectorOpts.Clock`. It defaults to the real clock, so you do not need to set it. See [Clock](../container/options.md#clock).
