---
title: Extensions release
description: Changes to check when upgrading to the doger release that adds extensions
sidebar_position: 5
---

# Upgrading to the extensions release

This release adds the Extension SPI (`doger.Extend`, `doger.Extension`, `ExtensionContext` and property sources): see [Installing extensions](../extensions/installing.md) and [Writing an extension](../extensions/authoring.md). Every exported identifier is new: nothing existing is renamed or removed. One behavior changes, and only for containers that install extensions.

## Root health check covers extension scopes {#root-health-check}

**Behavior change, containers using `doger.Extend` only.** A root `HealthCheck` used to check the root's own services only, so a service exported from a child scope was checked under its published name in the root.

In a container with installed extensions, a root `HealthCheck` now also checks the own services of each extension's private scope, keyed `<scope name>/<service name>` (for example `ext:redis/*redis.Pool`), and no longer checks, in the root, the exports whose source is an extension scope. Each service is checked once. See [Extension scopes](../service-lifecycle/healthchecker.md#extension-scopes).

Ordinary child scopes, and containers without extensions, are checked exactly as before.

Migration: none, unless code reads the root health map of a container with extensions by the published name of an extension's export; read it by the extension-scope key instead.
