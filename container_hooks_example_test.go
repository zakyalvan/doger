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
