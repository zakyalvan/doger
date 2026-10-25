---
title: 🚀 Getting started
description: Let's discover doger in less than 5 minutes.
sidebar_position: 1
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# Getting started

Discover **doger in less than 5 minutes**.

## What you'll need {#what-youll-need}

Compatible with [Go](https://go.dev/doc/install/) 1.27 or later.

This library has no dependencies except the Go std lib.

Import package:

```sh
go get -u github.com/kartaladev/doger
```

## Create a DI container {#create-a-di-container}

The simplest way to start is to use the default options:

**Play: https://go.dev/play/p/g549GqBbj-n**

```go
import "github.com/kartaladev/doger"

injector := doger.New()
```

## Service registration and invocation {#service-registration-and-invocation}

Services can be declared as a singleton or a factory. In this example, we create two services, `Car` and `Engine`, with a simple dependency relationship.

```mermaid
---
config:
  theme: 'base'
  themeVariables:
    edgeLabelBackground: 'transparent'
    primaryColor: '#007D9C'
    primaryTextColor: '#fff'
    primaryBorderColor: '#7C0000'
    lineColor: '#F8B229'
---
graph LR
    B[Engine provider] -- Register ---> A{DI Container}
    C[Car provider] -- Register --> A
    A -- Invoke --> D[Engine Instance]
    A -- Invoke --> E[Car Instance]
    D -- Invoke --> E
```

### Declare constructors and invoke singleton {#declare-constructors-and-invoke-singleton}

<Tabs>
  <TabItem value="car" label="car.go" default>
    ```go
    // Provider
    func NewCar(i doger.Injector) (*Car, error) {
        return &Car{
            // import dependency
            Engine: doger.MustInvoke[*Engine](i),
            Wheels: [4]*Wheel{
                doger.MustInvokeNamed[*Wheel](i, "front-left"),
                doger.MustInvokeNamed[*Wheel](i, "front-right"),
                doger.MustInvokeNamed[*Wheel](i, "back-left"),
                doger.MustInvokeNamed[*Wheel](i, "back-right"),
            },
        }, nil
    }

    type Car struct {
        Engine *Engine
        Wheels [4]*Wheel
    }

    func (c *Car) Start() {
        c.Engine.Started = true
        println("vroooom")
    }
    ```

  </TabItem>
  <TabItem value="engine" label="engine.go">
    ```go
    // Provider
    func NewEngine(i doger.Injector) (*Engine, error) {
        return &Engine{
            Started: false,
        }, nil
    }

    type Engine struct {
        Started bool
    }

    func (e *Engine) Shutdown() error {
        // called on injector shutdown
        e.Started = false
        return nil
    }
    ```

  </TabItem>
  <TabItem value="wheel" label="wheel.go" default>
    ```go
    // Provider
    func NewWheel(i doger.Injector) (*Wheel, error) {
        return &Wheel{
            Front: false,
            Left: true,
        }, nil
    }

    type Wheel struct {
        Front bool
        Left  bool
    }

    func (e *Engine) Brake() {
        // stuff
    }
    ```

  </TabItem>
</Tabs>

### Register services using individual declaration {#register-services-using-individual-declaration}

<Tabs>
  <TabItem value="main" label="main.go" default>
    ```go
    func main() {
        // Create DI container and inject services
        injector := doger.New()

        doger.Provide(injector, NewCar)
        doger.Provide(injector, NewEngine)
        doger.ProvideNamed(injector, "front-left", NewWheel)
        doger.ProvideNamed(injector, "front-right", NewWheel)
        doger.ProvideNamed(injector, "back-left", NewWheel)
        doger.ProvideNamed(injector, "back-right", NewWheel)
        doger.ProvideValue(injector, &Config{
            Port: 4242,
        })

        // Invoking Car will instantiate the singleton and its Engine+Wheel dependencies
        car, err := doger.Invoke[*Car](injector)
        if err != nil {
            log.Fatal(err.Error())
        }

        car.Start() // that's all folks 🤗

        // Handle ctrl-c and shutdown services
        injector.ShutdownOnSignals(syscall.SIGTERM, os.Interrupt)
    }
    ```

  </TabItem>
</Tabs>

**Play: https://go.dev/play/p/cp5wNpo-5wn**

### Register services using package declaration {#register-services-using-package-declaration}

The services can be assembled into a package, and then, imported all at once into a new container.

<Tabs>
  <TabItem value="package" label="pkg/car/package.go" default>
    ```go
    package car

    // Export every services of a package and make them available in a single big provider
    var Package = doger.Package(
        doger.Lazy(NewCar),
        doger.Lazy(NewEngine),
        doger.LazyNamed("front-left", NewWheel),
        doger.LazyNamed("front-right", NewWheel),
        doger.LazyNamed("back-left", NewWheel),
        doger.LazyNamed("back-right", NewWheel),
    )
    ```

  </TabItem>
  <TabItem value="main" label="cmd/main.go">
    ```go
    package main

    import "github.com/foo/bar/pkg/car"

    func main() {
        // Create DI container and inject services of the "car" package
        injector := doger.New(car.Package)

        // The traditional DI methods still works
        doger.ProvideValue(injector, &Config{
            Port: 4242,
        })

        // Invoking Car will instantiate the singleton and its Engine+Wheel dependencies
        c, err := doger.Invoke[*car.Car](injector)
        if err != nil {
            log.Fatal(err.Error())
        }

        c.Start()  // that's all folks 🤗

        // Handle ctrl-c and shutdown services
        injector.ShutdownOnSignals(syscall.SIGTERM, os.Interrupt)
    }
    ```

  </TabItem>
</Tabs>

**Play: https://go.dev/play/p/kmf8aOVyj96**
