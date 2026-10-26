package doger

import (
	"context"
	"fmt"
	"time"

	"github.com/jonboulle/clockwork"
)

// clockExampleDatabase is a health check that hangs until released.
type clockExampleDatabase struct{ release chan struct{} }

func (db *clockExampleDatabase) HealthCheck() error {
	<-db.release
	return nil
}

func ExampleInjectorOpts_clock() {
	// By default (Clock: nil) the container measures time on the real clock.
	// A fake clock lets a test reach a timeout without waiting for it.
	clock := clockwork.NewFakeClock()
	injector := NewWithOpts(&InjectorOpts{
		Clock:              clock,
		HealthCheckTimeout: 5 * time.Second,
	})

	db := &clockExampleDatabase{release: make(chan struct{})}
	defer close(db.release)
	ProvideNamedValue(injector, "db", db)

	result := make(chan map[string]error, 1)
	go func() { result <- injector.HealthCheck() }()

	_ = clock.BlockUntilContext(context.Background(), 1) // the 5s timeout is armed
	clock.Advance(5 * time.Second)

	fmt.Println((<-result)["db"])
	// Output:
	// DI: health check timeout: context deadline exceeded
}

func ExampleInjectorOpts_healthCheckSingleTimeout() {
	// HealthCheckTimeout bounds each check of HealthCheck(); single-service
	// checks such as HealthCheckNamed get their own, longer bound here.
	clock := clockwork.NewFakeClock()
	injector := NewWithOpts(&InjectorOpts{
		Clock:                    clock,
		HealthCheckTimeout:       time.Second,
		HealthCheckSingleTimeout: 10 * time.Second,
	})

	db := &clockExampleDatabase{release: make(chan struct{})}
	defer close(db.release)
	ProvideNamedValue(injector, "db", db)

	result := make(chan error, 1)
	go func() { result <- HealthCheckNamed(injector, "db") }()

	_ = clock.BlockUntilContext(context.Background(), 1) // the 10s bound is armed
	clock.Advance(time.Second)                           // HealthCheckTimeout does not apply
	select {
	case err := <-result:
		fmt.Println("returned early:", err)
	case <-time.After(10 * time.Millisecond):
		fmt.Println("still running after 1s")
	}

	clock.Advance(9 * time.Second)
	fmt.Println(<-result)
	// Output:
	// still running after 1s
	// DI: health check timeout: context deadline exceeded
}
