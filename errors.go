package doger

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"
)

var (
	//nolint:revive
	ErrServiceNotFound    = errors.New("DI: could not find service")
	ErrServiceNotMatch    = errors.New("DI: could not find service satisfying interface")
	ErrCircularDependency = errors.New("DI: circular dependency detected")
	ErrHealthCheckTimeout = errors.New("DI: health check timeout")

	// ErrAlreadyStarted is returned by a Start call after the first one, whether
	// the first one succeeded or failed.
	ErrAlreadyStarted = errors.New("DI: container already started")
	// ErrStartAfterShutdown is returned by Start once root Shutdown has begun.
	// It is also the cause Shutdown gives the context of a running Start.
	ErrStartAfterShutdown = errors.New("DI: container is shut down")
)

// ShutdownReport represents the result of a shutdown operation.
// It includes overall success, the list of services that were shut down,
// any errors encountered, total shutdown time, and per-service shutdown durations.
//
// It implements the error interface, returning a formatted description of errors
// when any occurred, or a "no shutdown errors" message otherwise.
type ShutdownReport struct {
	Succeed             bool
	Services            []ServiceDescription
	Errors              map[ServiceDescription]error
	ShutdownTime        time.Duration
	ServiceShutdownTime map[ServiceDescription]time.Duration
}

// Error implements the error interface for ShutdownReport.
// If there are errors, it returns a multiline description. Otherwise a friendly message.
func (r ShutdownReport) Error() string {
	if len(r.Errors) == 0 {
		return ""
	}

	lines := []string{}
	for k, v := range r.Errors {
		if v != nil {
			lines = append(lines, fmt.Sprintf("  - %s > %s: %s", k.ScopeName, k.Service, v.Error()))
		}
	}

	if len(lines) == 0 {
		return "DI: no shutdown errors"
	}

	return "DI: shutdown errors:\n" + strings.Join(lines, "\n")
}

// mergeShutdownReports merges multiple ShutdownReport values into a single report.
// Services and ServiceShutdownTime are merged, and Errors are combined.
// Succeed is true only if all reports succeeded and no errors are present.
// ShutdownTime is the sum of individual report times.
func mergeShutdownReports(reports ...*ShutdownReport) *ShutdownReport {
	out := ShutdownReport{
		Succeed:             true,
		Services:            []ServiceDescription{},
		Errors:              map[ServiceDescription]error{},
		ShutdownTime:        0,
		ServiceShutdownTime: map[ServiceDescription]time.Duration{},
	}

	for _, r := range reports {
		// Merge services
		out.Services = append(out.Services, r.Services...)

		// Merge errors
		for k, v := range r.Errors {
			if v != nil {
				out.Errors[k] = v
			}
		}

		// Merge per-service times
		maps.Copy(out.ServiceShutdownTime, r.ServiceShutdownTime)

		out.ShutdownTime += r.ShutdownTime
	}

	out.Succeed = len(out.Errors) > 0

	return &out
}
