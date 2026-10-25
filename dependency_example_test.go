package doger

import (
	"errors"
	"fmt"
)

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

type exampleScheduler struct{}

func (*exampleScheduler) Shutdown() error {
	fmt.Println("stop scheduler")
	return nil
}

type exampleJob struct{}

func (*exampleJob) Shutdown() error {
	fmt.Println("stop job")
	return nil
}

func ExampleDeclareDependency() {
	injector := New()

	ProvideNamed(injector, "scheduler", func(i Injector) (*exampleScheduler, error) { return &exampleScheduler{}, nil })
	ProvideNamed(injector, "job", func(i Injector) (*exampleJob, error) { return &exampleJob{}, nil })
	_, _ = InvokeNamed[*exampleScheduler](injector, "scheduler")
	_, _ = InvokeNamed[*exampleJob](injector, "job")

	// The scheduler drives the job: stop the scheduler before the job.
	if err := DeclareDependency(injector, "scheduler", "job"); err != nil {
		fmt.Println(err)
	}

	// The reverse edge would close a cycle.
	err := DeclareDependency(injector, "job", "scheduler")
	fmt.Println(errors.Is(err, ErrCircularDependency))

	injector.Shutdown()

	// Output:
	// true
	// stop scheduler
	// stop job
}
