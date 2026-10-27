# Proposal

## Why

A child scope can hide internals from the rest of the container, because lookup only walks up the tree. There is no safe way to publish one of those internals to the parent, though. Doing it by hand with a provider in the parent that invokes from the child breaks the lifecycle. This was shown by throwaway tests during exploration, and each one becomes a RED test in this change:

- **Shutdown order:** a parent service that depends on a child service finds it already closed when the parent service shuts down. `ShutdownWithContext` shuts children down before the scope's own services, and dependents in another scope are not protected by the dependency graph.
- **Duplicate lifecycle:** a forwarding provider is a second service of the same Starter or Shutdowner type. The child instance is then started twice and shut down twice.
- **Wrong scope:** `As` and `AsNamed` can't help. An alias resolves its target from the invoker's scope, which never sees into a child.

`add-extensions` needs this primitive: an extension's default layout is a private scope with selected exports. It is also useful on its own, for any app that encapsulates a subsystem in a child scope.

## What Changes

- Add `Export[T](from *Scope, opts...)` and `ExportNamed[T](from *Scope, name string, opts...)`, returning an error like `As`, plus `MustExport` and `MustExportNamed`, which panic with it. Each registers a service in `from`'s parent that resolves `T` (or `name`) from `from`. The `ExportAs(name)` option renames the published service.
- **An export is an alias bound to a scope.** It is never started, shut down or handled by a kind itself, and the child keeps ownership of the instance. A health check of the export runs the target's health check, except from the source scope or its descendants, where the export is skipped so the target is checked once.
- **Collections and by-type selection see exports in the parent.** Unlike `As`/`Bind` aliases, an export is a collection member in the parent and its other descendants, because the exported service is not visible there otherwise. Read from the exporting scope or its descendants it is not a member, so nothing is counted twice.
- **An export of a default is a default.** When the source was registered with `AsDefault()`, the export yields to and is replaced by explicit services of the published name in the parent, as `add-default-registrations` specifies, so the application's service wins whatever the registration order. The source default never yields to its own export.
- **Shutdown order across an export.** When the scope that holds an export shuts down, its services that depend on the export shut down before the exporting child scope. The export records a dependency edge from the published service to its source, which also orders Start. A cycle that crosses an export fails as `ErrCircularDependency` does elsewhere.
- **Clone:** a cloned export re-binds to the clone's child scope with the same name and never resolves into the original container.
- **Errors:** exporting from a nil scope or a root scope (which has no parent), a source not registered in `from` itself, a type mismatch, an empty name, or a name that already exists in the parent fails at export time. Two exceptions: a name held by an unresolved default in the parent is not taken (the export replaces it), and an export of a default follows the default rules below. A target that is missing at resolution time fails with an error matching `ErrServiceNotFound` that names both scopes.
- **Not BREAKING:** additive. Child-scope shutdown for scopes without exports is unchanged.

## Capabilities

### New Capabilities
- `scope-export`: publishing a child-scope service into the parent. Covers resolution, alias-like lifecycle, shutdown order across the export, health checks, clone and errors.

### Modified Capabilities
- `dependency-declaration`: ADDED "Edges through an export order shutdown across scopes": the parent's dependents of an export shut down before the exporting child scope, including a sibling child that uses them.
- `service-aliases`: ADDED "An export never shuts its target down".
- `collection-invocation`: MODIFIED "Aliases are excluded from collections" to say that exports are not `As`/`Bind` aliases and follow scope-export's membership rule.
- `scope-lifecycle-hooks`: MODIFIED "Scope Closing and Closed hooks bracket a child scope shutdown": a child that exports into its parent closes inside the parent's Closing/Closed pair, after the parent's services that depend on its exports (BEHAVIOR CHANGE, flagged, only for exporting scopes).

`service-aliases`, `collection-invocation` and `scope-lifecycle-hooks` come from `consolidate-pending-lifecycle-work`, which must be archived first.

## Impact

- Code: a new scope-bound alias wrapper (modelled on `service_alias.go`), `scope.go` (shutdown of children and services: the scope's own dependents of exports go first), `dag.go` (the export edge), `CloneWithOpts`/`clone` (re-binding), `invoke.go` (collection membership) and `service_default.go` (export unwrapping). `di_explain.go` needs no change: the export edge shows through the existing DAG.
- Concurrency: the new shutdown ordering runs under the existing scope locks and in parallel batches. A `-race` test with concurrent invokes during shutdown is required.
- Docs: a "Scope export" section on the scopes page.
- Dependencies: requires `add-default-registrations` (exports of defaults, and the shared duplicate-resolution and one-hop invocation helpers). A prerequisite of `add-extensions`.

## Defaults & Control

| Behavior | Zero-config default | Consumer control |
|---|---|---|
| Name of the published service | Same as the child service (inferred type name, or `name`) | `ExportAs(name)` option |
| Lifecycle of an exported instance | Owned by the child: started, health-checked and shut down there, exactly once | Not overridable: one owner per instance is what makes this safe; to make the parent own a service, register it in the parent |
| Shutdown order | Parent dependents of the export shut down before the exporting child | `DeclareDependency` adds more edges; nothing can shut the child down first, because that is the defect this change prevents |
| Scopes without exports | Shutdown exactly as today | – |
| Exporting a default | The export is a default too: an explicit service of the published name wins, before or after the export | Register the explicit service; export a non-default source to publish it as explicit |
| Collections in the parent | The export is a member | Read from the source scope, or invoke by name, to bypass it |
