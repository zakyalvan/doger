package doger

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

var (
	// ErrServiceNotFound is matched by the error of an invocation, health
	// check, ShutdownNamed call or dependency declaration that names a service
	// no visible scope has registered, or one exported from a scope that was
	// shut down. The message names the service and lists the available
	// services sorted by name, at most 10, followed by "and N more" when
	// more exist.
	ErrServiceNotFound = errors.New("doger: could not find service")
	// ErrServiceNotMatch is matched by the error of InvokeAs and MustInvokeAs
	// when no visible service is assignable to the requested type.
	ErrServiceNotMatch = errors.New("doger: could not find service satisfying interface")
	// ErrServiceTypeMismatch is matched by the error of an invocation that
	// finds the requested name but whose registered type is not the
	// requested one. The message names both types.
	ErrServiceTypeMismatch = errors.New("doger: service found, but type mismatch")
	// ErrServiceAlreadyDeclared is matched by the error of a registration
	// whose name the scope already registers: the panic value of the
	// registration functions without an error result, and the error the
	// error-returning ones return. The existing service stays registered.
	ErrServiceAlreadyDeclared = errors.New("doger: service has already been declared")
	// ErrScopeAlreadyDeclared is matched by the panic value of Scope(name)
	// on a scope that already has an attached child named name. The message
	// is "doger: scope `<name>` has already been declared". A child that was
	// shut down is detached, so its name can be declared again; the same
	// name at another depth of the tree is a different scope.
	ErrScopeAlreadyDeclared = errors.New("doger: scope has already been declared")
	// ErrCircularDependency is matched by the error of an invocation or
	// dependency declaration that would close a cycle between services, and
	// by the error of Start when the dependencies it orders form a cycle. The
	// message shows the cycle.
	ErrCircularDependency = errors.New("doger: circular dependency detected")
	// ErrHealthCheckTimeout is matched by the result of a health check whose
	// context ended (its timeout passed or it was cancelled) before the
	// result was observed. The result also matches the context's error.
	ErrHealthCheckTimeout = errors.New("doger: health check timeout")
	// ErrHealthCheckPanic is matched by the result of a health check that
	// panicked. The panic value follows it in the message, and an error value
	// is wrapped, so errors.Is and errors.As also match it.
	ErrHealthCheckPanic = errors.New("doger: health check panic")

	// ErrAlreadyStarted is returned by a Start call after the first one, whether
	// the first one succeeded or failed, as long as root Shutdown has not
	// begun: once it has, a Start returns [ErrShutdown] instead, never
	// ErrAlreadyStarted.
	ErrAlreadyStarted = errors.New("doger: container already started")
	// ErrShutdown is matched once a scope's shutdown has begun: by the error
	// of Invoke*, Alias* and Export* on that scope, by the panic value of
	// Provide*, Override* and Scope() on it, and by the error of an
	// invocation that looked a service up before its shutdown and would
	// have built it again. A container cannot be reused after Shutdown:
	// build a new one.
	//
	// It is also matched by the error of Start, Refresh and Scope.Start once
	// root Shutdown has begun, which takes precedence over
	// [ErrAlreadyStarted]. Shutdown gives it as the cause of the context of a
	// running Start, Refresh or Scope.Start, so the error of a run Shutdown
	// interrupted matches it, and of the context of a health check in flight
	// (see [context.Cause]). It replaces ErrStartAfterShutdown.
	ErrShutdown = errors.New("doger: container is shut down")
	// ErrNotStarted is returned by Refresh and Scope.Start while the
	// container's first Start has not succeeded.
	ErrNotStarted = errors.New("doger: container not started")
	// ErrNilInjector is matched when a nil injector, root or scope is
	// passed, including a typed-nil *RootScope or *Scope: it is the error of
	// the functions with an error result and the panic value of the others.
	// No call falls back to a global container.
	ErrNilInjector = errors.New("doger: nil injector")
	// ErrProviderPanic is matched by the error of an invocation whose
	// provider panicked, or whose dependency's provider panicked. The panic
	// value follows it in the message; an error value is wrapped, so
	// errors.Is and errors.As also match it, including the
	// *runtime.PanicNilError of panic(nil).
	ErrProviderPanic = errors.New("doger: provider panicked")
	// ErrStructInjection is matched by the error of InvokeStruct and the
	// panic value of MustInvokeStruct when the target cannot be injected.
	// These errors match it, each naming the type or field:
	//   - "must be a struct or a pointer to a struct, but got `T`"
	//   - "must be a pointer to a struct"
	//   - "recursive pointer type `T` cannot be injected"
	//   - "field is not addressable `S.F`"
	//   - "`name` is not assignable to field `S.F`", also for a service
	//     whose value is a nil interface of a type not assignable to F
	//   - "tag `*` requires a slice or map[string] field, got `S.F`"
	// A dependency that fails to resolve does not match it.
	ErrStructInjection = errors.New("doger: invalid struct injection target")
	// ErrShutdownPanic is matched by the error a shutdown records for a
	// service whose Shutdown method, or one of its HookBeforeShutdown or
	// HookAfterShutdown hooks, panicked: the panic is recovered, never
	// raised, and the other services and hooks still run. The panic value
	// follows it in the message; an error value is wrapped, so errors.Is and
	// errors.As also match it, including the *runtime.PanicNilError of
	// panic(nil).
	ErrShutdownPanic = errors.New("doger: shutdown panicked")
)

// libPrefix starts every message the library originates.
const libPrefix = "doger: "

// nilText stands for a nil cause in a message.
const nilText = "<nil>"

// libError marks an error the library made with libErrorf, so a message
// wrapping it can tell the leading libPrefix is the library's own.
type libError struct {
	err error
}

// Error returns the message of the formatted error.
func (e *libError) Error() string {
	return e.err.Error()
}

// Unwrap returns the formatted error, which wraps the %w arguments.
func (e *libError) Unwrap() error {
	return e.err
}

// libSentinels are the exported sentinels, all library errors.
var libSentinels = []error{
	ErrServiceNotFound, ErrServiceNotMatch, ErrServiceTypeMismatch,
	ErrServiceAlreadyDeclared, ErrScopeAlreadyDeclared, ErrCircularDependency,
	ErrHealthCheckTimeout, ErrHealthCheckPanic,
	ErrAlreadyStarted, ErrShutdown, ErrNotStarted, ErrNilInjector,
	ErrStructInjection, ErrProviderPanic, ErrShutdownPanic,
	ErrInvalidExtension, ErrExtensionDisabled, ErrRequiredExtensionDisabled,
	ErrExtensionCycle, ErrExtensionConflict,
	ErrInvalidEvent, ErrInvalidListener, ErrPublishAfterShutdown,
}

// isLibMade reports whether the library made err itself: a sentinel, a
// libErrorf result, or a library error type whose message it formats.
func isLibMade(err error) bool {
	//nolint:errorlint // one layer on purpose: isLibLed walks the chain and asks about each error in it
	switch e := err.(type) {
	case *libError, *ExtensionError, *ServiceError, *ListenerError:
		return true
	case *StartError:
		return !e.unprefixed
	}

	return slices.Contains(libSentinels, err)
}

// isLibLed reports whether the message of err starts with libPrefix written
// by the library: some error in its chain that the library made has a
// message the whole message starts with. A consumer error whose own text
// starts with libPrefix is not library-led.
func isLibLed(err error) bool {
	message := err.Error()
	if !strings.HasPrefix(message, libPrefix) {
		return false
	}

	pending := []error{err}
	for len(pending) > 0 {
		e := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if e == nil {
			continue
		}
		if isLibMade(e) && strings.HasPrefix(message, e.Error()) {
			return true
		}

		//nolint:errorlint // this loop is the chain walk itself: it unwraps one layer at a time, both Unwrap forms
		switch u := e.(type) {
		case interface{ Unwrap() error }:
			pending = append(pending, u.Unwrap())
		case interface{ Unwrap() []error }:
			pending = append(pending, u.Unwrap()...)
		}
	}

	return false
}

// unprefixedText returns the message of err, without its leading libPrefix
// when that prefix is the library's (see isLibLed).
func unprefixedText(err error) string {
	if isLibLed(err) {
		return strings.TrimPrefix(err.Error(), libPrefix)
	}

	return err.Error()
}

// unprefixedError is a library-led error whose message is shown without its
// leading libPrefix, so a message wrapping it carries the prefix once.
// errors.Is and errors.As reach the original through Unwrap.
type unprefixedError struct {
	err error
}

// Error returns the message of the wrapped error without its leading
// libPrefix.
func (e unprefixedError) Error() string {
	return strings.TrimPrefix(e.err.Error(), libPrefix)
}

// Unwrap returns the wrapped error.
func (e unprefixedError) Unwrap() error {
	return e.err
}

// libErrorf formats a library error like fmt.Errorf, with libPrefix added
// once in front of format. A library-led error argument (see isLibLed) is
// shown without its prefix, and still matches with errors.Is and errors.As
// when wrapped with %w; any other argument, including a consumer error
// whose text starts with libPrefix, is formatted as is.
func libErrorf(format string, args ...any) error {
	return &libError{fmt.Errorf(libPrefix+format, unprefixedArgs(args)...)}
}

// ledErrorf formats a message that starts with lead, the first argument of
// format. When lead is library-led (see isLibLed), it is libErrorf;
// otherwise the message is led by the consumer's own error and carries no
// library prefix. Library-led arguments are shown without their prefix
// either way, so the message carries libPrefix at most once.
func ledErrorf(lead error, format string, args ...any) error {
	args = append([]any{lead}, args...)
	if isLibLed(lead) {
		return libErrorf(format, args...)
	}

	return fmt.Errorf(format, unprefixedArgs(args)...)
}

// unprefixedArgs returns args with each library-led error shown without its
// prefix; errors.Is and errors.As still reach it through %w.
func unprefixedArgs(args []any) []any {
	unprefixed := make([]any, len(args))
	for i, arg := range args {
		if err, ok := arg.(error); ok && isLibLed(err) {
			arg = unprefixedError{err}
		}
		unprefixed[i] = arg
	}

	return unprefixed
}

// injectionTargetError is an invalid struct injection target: its message
// is the one of err, and it matches both err and ErrStructInjection.
type injectionTargetError struct {
	err error
}

// Error returns the message of the wrapped error.
func (e *injectionTargetError) Error() string {
	return e.err.Error()
}

// Unwrap returns the wrapped error and ErrStructInjection.
func (e *injectionTargetError) Unwrap() []error {
	return []error{e.err, ErrStructInjection}
}

// structInjectionError formats an invalid struct injection target like
// libErrorf: the error matches ErrStructInjection, and its message is the
// formatted one.
func structInjectionError(format string, args ...any) error {
	return &injectionTargetError{libErrorf(format, args...)}
}

// ServiceError is the error of an invocation whose service build failed:
// its provider returned an error or panicked, or a dependency it resolves
// failed. Each service whose build failed adds one layer, so a missing
// dependency "b" of "a" is a ServiceError for "a" wrapping a ServiceError
// for "b" wrapping the not-found error. A requested name that is not
// registered at all is a plain not-found error, not a ServiceError.
//
// Use errors.As to find the outermost failing service; errors.Is reaches
// the cause through Unwrap.
type ServiceError struct {
	Scope   string // name of the scope the service is registered in
	Service string // name of the service
	Err     error  // cause of the failure
}

// Error returns `doger: service "<Service>" in scope "<Scope>": <cause>`,
// with the library prefix once. A nil Err reads as <nil>.
func (e *ServiceError) Error() string {
	cause := nilText
	if e.Err != nil {
		cause = unprefixedText(e.Err)
	}

	return fmt.Sprintf(libPrefix+"service %q in scope %q: %s", e.Service, e.Scope, cause)
}

// Unwrap returns Err.
func (e *ServiceError) Unwrap() error {
	return e.Err
}

// sentinelMatch is a library message that matches sentinel with errors.Is
// and errors.As without showing the sentinel's own text.
type sentinelMatch struct {
	message  string
	sentinel error
}

// Error returns the message.
func (e sentinelMatch) Error() string {
	return e.message
}

// Unwrap returns the sentinel.
func (e sentinelMatch) Unwrap() error {
	return e.sentinel
}

// libErrorMatching formats a library error like libErrorf, matching
// sentinel without showing its text.
func libErrorMatching(sentinel error, format string, args ...any) error {
	return &libError{sentinelMatch{message: libErrorf(format, args...).Error(), sentinel: sentinel}}
}

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

// StartError is the error Start, Refresh and Scope.Start return when a
// service or a kind handler fails. Errors that are not tied to a service (a
// guard, a cancelled context, a cycle among the dependency edges checked
// before the build) are not StartError values; a cycle met while building a
// service, for example through [DependsOnNamed], is that service's build
// failure.
//
// When a service opted in with [AsStarter] implements no Starter interface,
// Op is StartOpStart and Error returns Cause's message without the "failed
// to start service" prefix, because that message names the service already.
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

// Error returns the failure message, naming the service and its scope. A
// nil Scope reads as an empty scope name and a nil Cause as <nil>, so a
// value built by hand never panics.
func (e *StartError) Error() string {
	switch {
	case e.unprefixed:
		if e.Cause == nil {
			return nilText
		}
		return e.Cause.Error()
	case e.Op == StartOpHandle:
		return fmt.Sprintf("doger: kind `%s` failed for service `%s` in scope `%s`: %s", e.Kind, e.Service, e.scopeName(), e.causeText())
	case e.Op == StartOpStart:
		return fmt.Sprintf("doger: failed to start service `%s` in scope `%s`: %s", e.Service, e.scopeName(), e.causeText())
	default:
		return fmt.Sprintf("doger: failed to build service `%s` in scope `%s`: %s", e.Service, e.scopeName(), e.causeText())
	}
}

// scopeName returns the name of Scope, or "" when it is nil.
func (e *StartError) scopeName() string {
	if e.Scope == nil {
		return ""
	}

	return e.Scope.Name()
}

// causeText returns the message of Cause without its library prefix, or
// "<nil>" when it is nil. A Cause that is the ServiceError of the same
// service and scope is shown by its own cause, so the message names the
// service once.
func (e *StartError) causeText() string {
	cause := e.Cause
	//nolint:errorlint // only a direct ServiceError of this very service is shown by its cause; a wrapped one names another layer
	if serviceErr, ok := cause.(*ServiceError); ok && serviceErr.Service == e.Service && serviceErr.Scope == e.scopeName() {
		cause = serviceErr.Err
	}
	if cause == nil {
		return nilText
	}

	return unprefixedText(cause)
}

// Unwrap returns Cause.
func (e *StartError) Unwrap() error {
	return e.Cause
}

// ShutdownReport represents the result of a shutdown operation.
// It includes overall success, the list of services that were shut down,
// any errors encountered, total shutdown time, and per-service shutdown durations.
//
// To tell whether the shutdown failed, check report.Err() != nil (or
// Succeed): Err returns nil on success and an error matching every service
// error otherwise, and Unwrap lists them as *ServiceError values. The report
// also implements the error interface: Error returns "" on success and a
// "doger: shutdown errors:" description otherwise, ordered by scope name,
// then service name. A report is never nil, so do not compare it with nil.
// It marshals to JSON (see MarshalJSON).
type ShutdownReport struct {
	Succeed             bool
	Services            []ServiceDescription
	Errors              map[ServiceDescription]error
	ShutdownTime        time.Duration
	ServiceShutdownTime map[ServiceDescription]time.Duration
}

// Error implements the error interface for ShutdownReport. It returns "" when
// the report holds no error, and a multiline "doger: shutdown errors:"
// description otherwise. Check Err to tell success from failure.
func (r ShutdownReport) Error() string {
	if len(r.Errors) == 0 {
		return ""
	}

	lines := []string{}
	for _, e := range r.sortedServiceErrors() {
		lines = append(lines, fmt.Sprintf("  - %s > %s: %s", e.Scope, e.Service, e.Err.Error()))
	}

	if len(lines) == 0 {
		return "doger: no shutdown errors"
	}

	return "doger: shutdown errors:\n" + strings.Join(lines, "\n")
}

// Err returns nil when the report holds no error, and otherwise an error
// matching every service error with errors.Is and errors.As:
//
//	if err := injector.Shutdown().Err(); err != nil {
//	    log.Printf("shutdown: %v", err)
//	}
//
// Check Err, not the report itself, against nil: a *ShutdownReport is never
// nil.
func (r ShutdownReport) Err() error {
	errs := r.Unwrap()
	if len(errs) == 0 {
		return nil
	}

	return errors.Join(errs...)
}

// Unwrap returns one *ServiceError per failed service, ordered by scope
// name, then service name; nil when the report holds no error.
func (r ShutdownReport) Unwrap() []error {
	serviceErrs := r.sortedServiceErrors()
	if len(serviceErrs) == 0 {
		return nil
	}

	errs := make([]error, 0, len(serviceErrs))
	for _, e := range serviceErrs {
		errs = append(errs, e)
	}

	return errs
}

// sortedServiceErrors returns one *ServiceError per error of the report
// that is not nil, ordered by scope name, then service name, then scope ID.
func (r ShutdownReport) sortedServiceErrors() []*ServiceError {
	keys := make([]ServiceDescription, 0, len(r.Errors))
	for k, v := range r.Errors {
		if v != nil {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, compareServiceDescriptions)

	errs := make([]*ServiceError, 0, len(keys))
	for _, k := range keys {
		errs = append(errs, &ServiceError{Scope: k.ScopeName, Service: k.Service, Err: r.Errors[k]})
	}

	return errs
}

// MarshalJSON encodes the report as an object with "succeed",
// "shutdownTime" (a Go duration string), "services" ([{"scope", "service"}])
// and "errors" ([{"scope", "service", "error"}]); both arrays are ordered by
// scope name, then service name, and are [] when empty.
func (r ShutdownReport) MarshalJSON() ([]byte, error) {
	type service struct {
		Scope   string `json:"scope"`
		Service string `json:"service"`
	}
	type serviceError struct {
		Scope   string `json:"scope"`
		Service string `json:"service"`
		Error   string `json:"error"`
	}

	described := slices.Clone(r.Services)
	slices.SortFunc(described, compareServiceDescriptions)
	services := make([]service, 0, len(described))
	for _, d := range described {
		services = append(services, service{Scope: d.ScopeName, Service: d.Service})
	}

	serviceErrs := r.sortedServiceErrors()
	errs := make([]serviceError, 0, len(serviceErrs)) // [] when empty, never null
	for _, e := range serviceErrs {
		errs = append(errs, serviceError{Scope: e.Scope, Service: e.Service, Error: e.Err.Error()})
	}

	return json.Marshal(struct {
		Succeed      bool           `json:"succeed"`
		ShutdownTime string         `json:"shutdownTime"`
		Services     []service      `json:"services"`
		Errors       []serviceError `json:"errors"`
	}{r.Succeed, r.ShutdownTime.String(), services, errs})
}

// compareServiceDescriptions orders by scope name, then service name, then
// scope ID.
func compareServiceDescriptions(a, b ServiceDescription) int {
	return cmp.Or(
		strings.Compare(a.ScopeName, b.ScopeName),
		strings.Compare(a.Service, b.Service),
		strings.Compare(a.ScopeID, b.ScopeID),
	)
}

// appendShutdownReport adds r to dst in place, as mergeShutdownReports(dst,
// r) would, without copying what dst already holds.
func appendShutdownReport(dst, r *ShutdownReport) {
	dst.Services = append(dst.Services, r.Services...)
	for k, v := range r.Errors {
		if v != nil {
			dst.Errors[k] = v
		}
	}
	maps.Copy(dst.ServiceShutdownTime, r.ServiceShutdownTime)
	dst.ShutdownTime += r.ShutdownTime
	dst.Succeed = len(dst.Errors) == 0
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
