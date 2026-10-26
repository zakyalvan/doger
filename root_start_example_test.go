package doger

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
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

type exampleWorker struct{ name string }

func (w *exampleWorker) Start() error {
	fmt.Println(w.name, "started")
	return nil
}

func ExampleRootScope_Refresh() {
	root := New()
	ProvideNamedValue(root, "api", &exampleWorker{name: "api"})
	_ = root.Start()

	// Registered after Start: not started until Refresh.
	ProvideNamedValue(root, "reports", &exampleWorker{name: "reports"})
	tenant := root.Scope("tenant")
	ProvideNamedValue(tenant, "tenant-worker", &exampleWorker{name: "tenant-worker"})

	_ = root.Refresh() // starts reports and tenant-worker, not api again
	_ = root.Refresh() // nothing new
	// Output:
	// api started
	// reports started
	// tenant-worker started
}

func ExampleRootScope_Refresh_notStarted() {
	root := New()

	// Refresh needs a container whose first Start succeeded.
	err := root.Refresh()
	fmt.Println(errors.Is(err, ErrNotStarted))

	_ = root.Start()
	fmt.Println(root.Refresh())
	// Output:
	// true
	// <nil>
}

func ExampleRootScope_RefreshWithContext() {
	root := New()
	_ = root.Start()
	ProvideNamedValue(root, "reports", &exampleWorker{name: "reports"})

	// The run's context derives from ctx: its deadline and values reach
	// context-aware Starters, kind handlers and hooks.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fmt.Println(root.RefreshWithContext(ctx))

	// A cancelled context starts nothing.
	ProvideNamedValue(root, "late", &exampleWorker{name: "late"})
	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	fmt.Println(errors.Is(root.RefreshWithContext(cancelled), context.Canceled))
	// Output:
	// reports started
	// <nil>
	// true
}

func ExampleScope_StartWithContext() {
	root := New()
	_ = root.Start()

	type tenantKey struct{}
	root.AddScopeStartingHook(func(ctx context.Context, scope *Scope) error {
		fmt.Println("starting", scope.Name(), "for", ctx.Value(tenantKey{}))
		return nil
	})
	tenant := root.Scope("tenant-a")
	ProvideNamedValue(tenant, "sync", &exampleWorker{name: "tenant-a sync"})

	// Like Scope.Start, with a context for the run.
	ctx := context.WithValue(context.Background(), tenantKey{}, "acme")
	fmt.Println(tenant.StartWithContext(ctx))
	// Output:
	// starting tenant-a for acme
	// tenant-a sync started
	// <nil>
}

func ExampleScope_Start() {
	root := New()
	_ = root.Start()

	// A tenant scope created after Start, with its own workers.
	tenant := root.Scope("tenant-a")
	ProvideNamedValue(tenant, "sync", &exampleWorker{name: "tenant-a sync"})
	other := root.Scope("tenant-b")
	ProvideNamedValue(other, "sync", &exampleWorker{name: "tenant-b sync"})

	// Starts tenant-a and its descendants only.
	if err := tenant.Start(); err != nil {
		fmt.Println(err)
	}
	// Output:
	// tenant-a sync started
}

// exampleBarrierStarter waits, in Start, until every Starter of its group
// has entered Start: it returns only if they start concurrently.
type exampleBarrierStarter struct {
	entered *sync.WaitGroup
}

func (s *exampleBarrierStarter) Start() error {
	s.entered.Done()

	all := make(chan struct{})
	go func() {
		s.entered.Wait()
		close(all)
	}()

	select {
	case <-all:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("not started concurrently")
	}
}

func ExampleInjectorOpts_startParallelism() {
	// Up to 4 independent Starters start at the same time; a Starter still
	// waits for the Starters it depends on.
	root := NewWithOpts(&InjectorOpts{StartParallelism: 4})

	var entered sync.WaitGroup
	entered.Add(4)
	for _, name := range []string{"cache-a", "cache-b", "cache-c", "cache-d"} {
		ProvideNamedValue(root, name, &exampleBarrierStarter{entered: &entered})
	}

	fmt.Println(root.Start())
	// Output:
	// <nil>
}
