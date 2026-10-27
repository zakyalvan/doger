---
title: Scope export release
description: Changes to check when upgrading to the doger release that adds scope export
sidebar_position: 3
---

# Upgrading to the scope export release

The scope export release adds `Export`, `ExportNamed`, `MustExport`, `MustExportNamed` and `ExportAs`, which publish a child scope's services to its parent. See [Scope export](../container/scope.md#scope-export). Nothing changes for code that does not export.

## An exporting child closes inside its parent's hooks {#exporting-child-hooks}

**Behavior change, exporting scopes only.** A child scope shuts down before its parent's services, so its `HookScopeClosing` and `HookScopeClosed` hooks run before its parent's. A child that exports services into its parent is the exception: its services shut down in dependency order together with the parent's (see [Shutdown order](../container/scope.md#scope-export-shutdown)), so its hooks run inside its parent's Closing/Closed pair, its `HookScopeClosing` just before its first service shuts down.

Migration: none, unless a scope hook relies on every child closing before its parent; such a hook must accept an exporting child closing after its parent's `HookScopeClosing`.
