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
