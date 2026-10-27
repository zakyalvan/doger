# Proposal

## Why

A reusable package (and, later, an extension) cannot ship a service that the application may replace. `provide` panics when a name is already declared in the same scope (`di.go`, "has already been declared"), so whichever side registers second fails. In a child scope the opposite happens: the package's own registration shadows the application's service in an ancestor, so the override is silently ignored. Both outcomes depend on registration order.

"Ship a sensible default that the consumer can replace" is the library principle of this project, and it is also the prerequisite for `add-extensions`. This change makes it a first-class registration mode.

## What Changes

- Add the registration option `AsDefault()`. A default is a fallback: it is used only when no explicit (non-default) service replaces it.
- **The decision happens at first resolution, not at registration.** When a default is first resolved, it checks whether a non-default service with the same name is registered in an ancestor of the default's scope. If one is, the default yields to it. Otherwise the default uses its own provider or value ("chosen"). Registration order no longer matters, and the rule works with or without `Start`.
- **Same scope, explicit after default:** an explicit `Provide*` of the same name replaces a default that has not been resolved yet, instead of panicking. The replace decision and a concurrent first resolution are serialized, so exactly one of them wins.
- **Same scope, explicit after a resolved default:** this fails as a duplicate registration does today. An instance the default built is never silently orphaned.
- **Same scope, default after explicit:** the default registration is skipped silently.
- **Two defaults with the same name in one scope:** this panics as a duplicate, as today.
- **A default that yields resolves as an alias of the service it yields to.** It is never started, shut down, health-checked or handled by a kind on its own, so a service is never started or shut down twice.
- **By-type resolution and collections honor defaults, following each default's decision.** `InvokeAs` and implicit-aliasing struct injection consider an unresolved default only when no other candidate matches. `InvokeAll`, `InvokeAllMap` and live collections exclude an unresolved default when a non-default member is assignable to its registered type. A yielded default is never a candidate, a default that chose its own provider stays one, and a default never hides a same-name non-default in an ancestor.
- **Every registration form accepts `AsDefault()`:** lazy, eager (`ProvideValue`), transient, and the `Package` helpers. Its meaning for an eager value, which already exists at registration, is "replaceable until first resolved". The container shuts an eager default value down only when the default uses it; a superseded or yielded value is owned by the code that created it. `Override*` ignores the option: an override is explicit by definition.
- **Health check:** a health check from a yielded default's scope reports the yielded-to service's result, calling it once.
- **Explainability:** `ExplainService` and `ExplainNamedService` report the default state (`not-default`, `unresolved`, `chosen`, `yielded`, serialized by name) and the scope and service it yielded to.
- **Not BREAKING:** the option is additive. Without `AsDefault()`, every registration behaves exactly as before, including the duplicate-name panic.

## Capabilities

### New Capabilities
- `default-registrations`: registering replaceable fallback services. Covers the resolve-time decision, same-scope replacement, yielding to ancestors, the lifecycle of a default that yields, concurrency and explain output.

### Modified Capabilities
- `registration-options`: adds `AsDefault()` to the library-constructed options, and specifies how it combines with the other options and with `Override*`.
- `type-resolution`: MODIFIED "InvokeAs picks the first match deterministically" (defaults rank after non-defaults) and ADDED the default ranking rules.
- `collection-invocation`: MODIFIED "Collect every visible service assignable to a type" and "Visibility follows scope ancestry with nearest-name shadowing" (the default exceptions) and ADDED the default membership rules. Live collections follow them through their "membership rules of `InvokeAll`".
- `container-start`: a default that yields is not a start candidate, and is not handled by a kind.

`type-resolution`, `collection-invocation` and the current `container-start` and `registration-options` requirements come from `consolidate-pending-lifecycle-work`. That change must be archived first, so these deltas apply to its requirements.

## Impact

- Code: `options.go` (option), `di.go` (`provide`: the duplicate path becomes default-aware), a default-aware wrapper or state on the lazy, eager and transient services, `invoke.go` (`selectByType` and collection candidates), `root_start.go` (candidate discovery), `di_explain.go`.
- Concurrency: the default's state (unresolved / chosen / yielded / superseded) is read and written under its build lock. A `-race` scenario covers "explicit replaces the default" racing "first resolution".
- Docs: a new "Default registrations" page, plus updates to the options, `InvokeAs` and collection pages.
- Dependencies: none. This is a prerequisite of `add-extensions`.

## Defaults & Control

| Behavior | Zero-config default | Consumer control |
|---|---|---|
| Registration without `AsDefault()` | Unchanged: explicit, and a duplicate name panics | – |
| A default meets an explicit service of the same name | The explicit service wins, whichever was registered first | Don't mark the service as a default, or use `Override*` to force a replacement |
| A default and an explicit service of a compatible type with different names (by-type resolution and collections) | The explicit service wins; the default is skipped | Invoke the default by name, or register the explicit service under the default's name |
| Explicit registration after the default was resolved (by any read, Start or shutdown) | Fails like a duplicate | Register before resolving, or use `Override*` (documented caveat: a built instance is not shut down, as for any override today) |
| A default that yields to another service | Behaves like an alias: no start, shutdown, health check or kind handling of its own | Not overridable: lifecycle effects belong to the service that actually provides the instance |
