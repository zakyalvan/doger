
# doger - Dependency Injection

[![tag](https://img.shields.io/github/tag/kartaladev/doger.svg)](https://github.com/kartaladev/doger/releases)
![Go Version](https://img.shields.io/badge/Go-%3E%3D%201.27-%23007d9c)
[![GoDoc](https://godoc.org/github.com/kartaladev/doger?status.svg)](https://pkg.go.dev/github.com/kartaladev/doger)
![Build Status](https://github.com/kartaladev/doger/actions/workflows/test.yml/badge.svg)
[![Go report](https://goreportcard.com/badge/github.com/kartaladev/doger)](https://goreportcard.com/report/github.com/kartaladev/doger)
[![Coverage](https://img.shields.io/codecov/c/github/kartaladev/doger)](https://codecov.io/gh/kartaladev/doger)
[![License](https://img.shields.io/github/license/kartaladev/doger)](./LICENSE)

**⚙️ A dependency injection toolkit based on Go generics (Go 1.27+).**

> doger is a fork of [samber/do](https://github.com/samber/do) by Samuel Berthe. Coming from `samber/do`? See [Migrating from samber/do](https://kartaladev.github.io/doger/docs/migrating/migrating-from-samber-do).

This library implements the Dependency Injection design pattern. It may replace the fantastic `uber/dig` package. `doger` uses Go generics (Go 1.27+) and therefore offers a type‑safe API.

![image](https://github.com/user-attachments/assets/81b91fa7-cdb4-4094-94ba-a0179abc6bf7)

**See also:**

- [samber/ro](https://github.com/samber/ro): Reactive Programming for Go: declarative and composable API for event-driven applications
- [samber/lo](https://github.com/samber/lo): A Lodash-style Go library based on Go 1.18+ Generics
- [samber/mo](https://github.com/samber/mo): Monads based on Go 1.18+ Generics (Option, Result, Either...)
- [samber/cc-skills-golang](https://github.com/samber/cc-skills-golang): AI Agent Skills for Golang

----

**Why this name?**

Upstream's short name `do` is the sum of `DI` and `Go`. `doger` keeps it as its prefix.

## 💡 Features

- **📒 Service registration**
  - Register by type
  - Register by name
  - Register multiple services from a package at once
- **🪃 Service invocation**
  - Eager loading
  - Lazy loading
  - Transient loading
  - Tag-based invocation
  - Circular dependency detection
- **🧙‍♂️ Service aliasing**
  - Implicit (provide struct, invoke interface)
  - Explicit (provide struct, bind interface, invoke interface)
- **🔁 Service lifecycle**
  - Health check
  - Graceful unload (shutdown)
  - Dependency-aware parallel shutdown
  - Lifecycle hooks
- **📦 Scope (a.k.a module) tree**
  - Visibility control
  - Dependency grouping
- **📤 Container**
  - Dependency graph resolution and visualization
  - Default container
  - Container cloning
  - Service override
- **🧪 Debugging & introspection**
  - Explain APIs: scope tree and service dependencies
  - Web UI & HTTP middleware (std, Gin, Fiber, Echo, Chi)
- **🌈 Lightweight, no dependencies**
- **🔅 No code generation**
- **😷 Type‑safe API**

## 🚀 Install

```sh
go get github.com/kartaladev/doger@latest
```

This library is v0 and follows SemVer: until v1.0.0, a minor release may contain breaking changes.

This library has no dependencies except the Go std lib.

### 🔥 Migration from samber/do

[Documentation here](https://kartaladev.github.io/doger/docs/migrating/migrating-from-samber-do). For upstream's own v1 to v2 upgrade, see [this page](https://kartaladev.github.io/doger/docs/upgrading/from-v1-x-to-v2).

## 🤠 Documentation

- [GoDoc: https://pkg.go.dev/github.com/kartaladev/doger](https://pkg.go.dev/github.com/kartaladev/doger)
- [Documentation](https://kartaladev.github.io/doger/docs/getting-started)
- [Examples](https://github.com/kartaladev/doger/tree/main/examples)
- [Project templates](https://kartaladev.github.io/doger/examples)

## 🎬 Project boilerplate

Upstream `samber/do` templates:

- [do-template-api](https://github.com/samber/do-template-api)
- [do-template-worker](https://github.com/samber/do-template-worker)
- [do-template-cli](https://github.com/samber/do-template-cli)

## 🤝 Contributing

- Fork the [project](https://github.com/kartaladev/doger)
- Fix [open issues](https://github.com/kartaladev/doger/issues) or request new features

Don't hesitate ;)

```bash
# Install some dev dependencies
make tools

# Run tests
make test
# or
make watch-test
```

## 👤 Contributors

![Contributors](https://contrib.rocks/image?repo=kartaladev/doger)

## 💫 Show your support

Give a ⭐️ if this project helped you!

## 📝 License

Copyright © 2022 [Samuel Berthe](https://github.com/samber) (upstream [samber/do](https://github.com/samber/do)).

Copyright © 2026 kartaladev.

This project is [MIT](./LICENSE) licensed.
