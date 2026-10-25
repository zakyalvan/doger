package doger

import (
	"context"
	"errors"
	"fmt"
)

type exampleDB struct{}

func (*exampleDB) Start(context.Context) error {
	fmt.Println("db started")
	return nil
}

func (*exampleDB) Shutdown() error {
	fmt.Println("db stopped")
	return nil
}

type exampleHTTPServer struct{}

func (*exampleHTTPServer) Start() error {
	fmt.Println("http started")
	return nil
}

func ExampleRootScope_Start() {
	root := New()

	// Start(ctx) error is detected on the registered type: auto-started.
	Provide(root, func(i Injector) (*exampleDB, error) { return &exampleDB{}, nil })
	// Invokes *exampleDB, so it is started after it.
	Provide(root, func(i Injector) (*exampleHTTPServer, error) {
		_, err := Invoke[*exampleDB](i)
		return &exampleHTTPServer{}, err
	})

	if err := root.StartWithContext(context.Background()); err != nil {
		// Start does not roll back: the caller owns cleanup.
		_ = root.Shutdown()
		fmt.Println(err)
		return
	}

	root.Shutdown()

	// Output:
	// db started
	// http started
	// db stopped
}

// exampleLifecycle has no Start method, so services registered under it are
// not auto-started.
type exampleLifecycle interface {
	Shutdown() error
}

func ExampleAsStarter() {
	root := New()

	// Registered under an interface type without Start: opt in explicitly.
	// The instance is checked for a Start method when Start runs.
	Provide(root, func(i Injector) (exampleLifecycle, error) {
		return &exampleDB{}, nil
	}, AsStarter())

	_ = root.Start()

	// Output:
	// db started
}

type exampleFailingCache struct{}

func (*exampleFailingCache) Start() error { return errors.New("connection refused") }

func ExampleStartError() {
	root := New()
	ProvideNamedValue(root, "cache", &exampleFailingCache{})

	err := root.Start()

	if startErr, ok := errors.AsType[*StartError](err); ok {
		fmt.Println("service:", startErr.Service)
		fmt.Println("scope:", startErr.Scope.Name())
		fmt.Println("op:", startErr.Op)
		fmt.Println("cause:", startErr.Cause)
	}
	// Output:
	// service: cache
	// scope: [root]
	// op: start
	// cause: connection refused
}
