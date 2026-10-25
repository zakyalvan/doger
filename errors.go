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

// StartOp names the step of Start that failed for a service.
type StartOp string

const (
	// StartOpBuild is the build of a service (its provider or a declared
	// dependency), or a check of its instance.
	StartOpBuild StartOp = "build"
	// StartOpStart is the Start method of a Starter.
	StartOpStart StartOp = "start"
	// StartOpHandle is a kind handler call for a component.
	StartOpHandle StartOp = "handle"
)

// StartError is the error Start returns when a service or a kind handler
// fails. Errors that are not tied to a service (a guard, a cancelled
// context, a dependency cycle) are not StartError values.
//
// Use errors.As to inspect it; errors.Is reaches Cause through Unwrap.
type StartError struct {
	Scope   *Scope  // scope the service is registered in
	Service string  // name of the service
	Kind    string  // name of the kind whose handler failed; empty unless Op is StartOpHandle
	Op      StartOp // failed step
	Cause   error   // underlying error

	unprefixed bool // Error returns Cause's message as is
}

// newStartError returns the StartError of a service failing at op.
func newStartError(op StartOp, scope *Scope, service, kind string, cause error) *StartError {
	return &StartError{Scope: scope, Service: service, Kind: kind, Op: op, Cause: cause}
}

// Error returns the failure message, naming the service and its scope.
func (e *StartError) Error() string {
	switch {
	case e.unprefixed:
		return e.Cause.Error()
	case e.Op == StartOpHandle:
		return fmt.Sprintf("DI: kind `%s` failed for service `%s` in scope `%s`: %s", e.Kind, e.Service, e.Scope.Name(), e.Cause.Error())
	case e.Op == StartOpStart:
		return fmt.Sprintf("DI: failed to start service `%s` in scope `%s`: %s", e.Service, e.Scope.Name(), e.Cause.Error())
	default:
		return fmt.Sprintf("DI: failed to build service `%s` in scope `%s`: %s", e.Service, e.Scope.Name(), e.Cause.Error())
	}
}

// Unwrap returns Cause.
func (e *StartError) Unwrap() error {
	return e.Cause
}

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

	out.Succeed = len(out.Errors) == 0

	return &out
}
