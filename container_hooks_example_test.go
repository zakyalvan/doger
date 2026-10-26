package doger

import (
	"context"
	"errors"
	"fmt"
)

func ExampleRootScope_AddContainerStartingHook() {
	root := New()
	ProvideValue(root, &exampleDB{})

	config := map[string]string{} // DB_URL is missing

	// Fail fast: veto Start before anything is built.
	root.AddContainerStartingHook(func(context.Context, *RootScope) error {
		if config["DB_URL"] == "" {
			return errors.New("missing DB_URL")
		}
		return nil
	})

	err := root.Start()
	fmt.Println(err)
	// Output:
	// DI: container start aborted: missing DB_URL
}

type exampleMigration struct{}

func (*exampleMigration) Run(context.Context) error {
	fmt.Println("migration ran")
	return nil
}

func ExampleRootScope_AddContainerReadyHook() {
	root := New()
	ProvideValue(root, &exampleDB{})
	ProvideValue(root, &exampleMigration{}, AsRunnable())

	// Ready fires after every Starter, every runner and every Started hook.
	root.AddContainerReadyHook(func(context.Context, *RootScope) {
		fmt.Println("ready: report healthy to the orchestrator")
	})

	if err := root.Start(); err != nil {
		fmt.Println(err)
	}
	// Output:
	// db started
	// migration ran
	// ready: report healthy to the orchestrator
}

type exampleBrokenServer struct{}

func (*exampleBrokenServer) Start() error { return errors.New("port in use") }

func ExampleRootScope_AddContainerFailedHook() {
	root := New()
	ProvideValue(root, &exampleDB{})
	ProvideNamedValue(root, "http", &exampleBrokenServer{})

	// Start rolls nothing back: clean up what did start. Failed hooks run
	// after Start released, so they may call Shutdown.
	root.AddContainerFailedHook(func(_ context.Context, root *RootScope, err error) {
		fmt.Println("start failed:", err)
		root.Shutdown()
	})

	_ = root.Start()
	// Output:
	// db started
	// start failed: DI: failed to start service `http` in scope `[root]`: port in use
	// db stopped
}

func ExampleInjectorOpts_containerHooks() {
	root := NewWithOpts(&InjectorOpts{
		HookContainerStarting: []func(context.Context, *RootScope) error{
			func(context.Context, *RootScope) error { fmt.Println("starting"); return nil },
		},
		HookContainerStarted: []func(context.Context, *RootScope){
			func(context.Context, *RootScope) { fmt.Println("started") },
		},
		HookContainerReady: []func(context.Context, *RootScope){
			func(context.Context, *RootScope) { fmt.Println("ready") },
		},
		HookContainerFailed: []func(context.Context, *RootScope, error){
			func(_ context.Context, _ *RootScope, err error) { fmt.Println("failed:", err) },
		},
		HookContainerClosing: []func(context.Context, *RootScope){
			func(context.Context, *RootScope) { fmt.Println("closing") },
		},
		HookContainerClosed: []func(context.Context, *RootScope, *ShutdownReport){
			func(_ context.Context, _ *RootScope, report *ShutdownReport) {
				fmt.Println("closed, succeed:", report.Succeed)
			},
		},
	})
	ProvideValue(root, &exampleDB{})

	_ = root.Start()
	root.Shutdown()
	// Output:
	// starting
	// db started
	// started
	// ready
	// closing
	// db stopped
	// closed, succeed: true
}

func ExampleInjectorOpts_hookContainerPanic() {
	root := NewWithOpts(&InjectorOpts{
		// Log observer hook panics instead of re-panicking.
		HookContainerPanic: func(_ *RootScope, hook string, recovered any) {
			fmt.Printf("%s hook panicked: %v\n", hook, recovered)
		},
	})
	root.AddContainerReadyHook(func(context.Context, *RootScope) {
		panic("readiness endpoint unavailable")
	})

	err := root.Start()
	fmt.Println("start error:", err)
	// Output:
	// ready hook panicked: readiness endpoint unavailable
	// start error: <nil>
}

func ExampleRootScope_AddScopeClosingHook() {
	root := New()
	_ = root.Start()

	// Per-tenant teardown, without a per-service hook.
	root.AddScopeClosingHook(func(_ context.Context, scope *Scope) {
		fmt.Println("closing", scope.Name())
	})
	root.AddScopeClosedHook(func(_ context.Context, scope *Scope) {
		fmt.Println("closed", scope.Name())
	})

	tenant := root.Scope("tenant-a")
	tenant.Shutdown()
	// Output:
	// closing tenant-a
	// closed tenant-a
}
