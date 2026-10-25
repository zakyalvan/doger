package doger

import "fmt"

type exampleMetrics struct{}

type exampleReporter struct{}

func ExampleDependsOn() {
	injector := New()

	Provide(injector, func(i Injector) (*exampleMetrics, error) {
		fmt.Println("build metrics")
		return &exampleMetrics{}, nil
	})

	// The reporter looks the metrics up later, at runtime, so its provider
	// does not invoke them. DependsOn makes the dependency explicit: the
	// metrics are built first and shut down after the reporter.
	Provide(injector, func(i Injector) (*exampleReporter, error) {
		fmt.Println("build reporter")
		return &exampleReporter{}, nil
	}, DependsOn[*exampleMetrics]())

	_, _ = Invoke[*exampleReporter](injector)

	explained, _ := ExplainService[*exampleReporter](injector)
	fmt.Println("depends on:", explained.Dependencies[0].Service)

	// Output:
	// build metrics
	// build reporter
	// depends on: *github.com/kartaladev/doger.exampleMetrics
}
