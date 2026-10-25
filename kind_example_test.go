package doger

import (
	"context"
	"fmt"
	"time"
)

// exampleScheduledJob is the target interface of the scheduled-job kind.
type exampleScheduledJob interface {
	Run(ctx context.Context) error
}

// exampleSchedule is the metadata attached to each scheduled job.
type exampleSchedule struct {
	Every time.Duration
}

type exampleCleanup struct{}

func (*exampleCleanup) Run(context.Context) error { return nil }

func (*exampleCleanup) Shutdown() error {
	fmt.Println("stop cleanup")
	return nil
}

type exampleCron struct {
	jobs []string
}

func (c *exampleCron) Start() error {
	fmt.Println("cron started with", c.jobs)
	return nil
}

func (*exampleCron) Shutdown() error {
	fmt.Println("stop cron")
	return nil
}

func ExampleNewKind() {
	scheduled := NewKind[exampleScheduledJob, exampleSchedule]("scheduled")

	root := New()
	cron := &exampleCron{}
	ProvideNamed(root, "cron", func(i Injector) (*exampleCron, error) { return cron, nil })
	ProvideNamed(root, "cleanup", func(i Injector) (*exampleCleanup, error) {
		return &exampleCleanup{}, nil
	}, scheduled.With(exampleSchedule{Every: time.Hour}))

	// Before Starters start: hand every scheduled job to the cron, and make
	// the cron depend on it, so the cron stops before the jobs it drives.
	scheduled.Handle(root, StageBeforeStart, func(ctx context.Context, c Component[exampleScheduledJob, exampleSchedule]) error {
		cron.jobs = append(cron.jobs, fmt.Sprintf("%s every %s", c.Name, c.Meta.Every))
		return DeclareDependency(c.Scope, "cron", c.Name)
	})

	if err := root.Start(); err != nil {
		fmt.Println(err)
	}
	root.Shutdown()

	// Output:
	// cron started with [cleanup every 1h0m0s]
	// stop cron
	// stop cleanup
}
