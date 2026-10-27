package doger

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/kartaladev/doger/stacktrace"
	typetostring "github.com/samber/go-type-to-string"
)

// ExportOption configures an export. Values come only from this package's
// constructors, such as [ExportAs].
type ExportOption func(*exportOptions)

// exportOptions is the configuration of an export.
type exportOptions struct {
	published string // the published name
}

// buildExportOptions returns the configuration of an export of sourceName.
func buildExportOptions(sourceName string, opts []ExportOption) exportOptions {
	options := exportOptions{published: sourceName}
	for _, opt := range opts {
		opt(&options)
	}

	return options
}

// ExportAs publishes the exported service in the parent scope under name,
// instead of the name of its source. An empty name makes the export fail.
func ExportAs(name string) ExportOption {
	return func(options *exportOptions) { options.published = name }
}

// Export publishes the service named after T, registered in from, to the
// parent scope of from, under the same name unless [ExportAs] renames it.
// The parent and its other descendants can then invoke it, while every other
// service of from stays private. To publish further up, export again from
// the parent: an export is a service of the parent, so it can be exported.
//
// An export is an alias bound to from: it always resolves its source from
// from, whatever scope invokes it, and it is listed as [ServiceTypeAlias].
// from keeps ownership of the instance, which it starts, health-checks and
// shuts down exactly once; the export is never started or shut down itself.
//
// Like [As], Export returns an error, and registers nothing, when from is nil
// or has no parent, when the source is not registered in from itself (a
// service visible only through an ancestor of from cannot be exported), when
// its registered type is not assignable to T, or when the published name is
// empty or already registered in the parent. Invoking an export after from
// was shut down fails with an error matching [ErrServiceNotFound].
//
// An export of a default (a source registered with [AsDefault]) is a default
// in the parent: an explicit service of the published name in the parent
// wins, whether it is registered before or after Export, and the private
// default then yields to it too. Export over such an explicit service
// returns nil and leaves it in place. The private default never yields to
// its own export.
//
// Shutdown order: when the parent shuts down, the services of the parent and
// of its exporting descendants (from, and the children exporting into from,
// recursively) shut down in one pass, in dependency order (invoked, DependsOn
// or DeclareDependency edges), whatever scope each belongs to: a service
// shuts down once no service still running in the pass depends on it. The
// parent's services that use the export therefore shut down before the
// exported service, and so do the services of from, or of an exporting
// sibling, that use them. The children that export nothing shut down first,
// as before. from runs its HookScopeClosing hooks just before its first
// service shuts down and its HookScopeClosed hooks once its services and its
// own exporting children are done, inside its parent's pair. A Shutdown of
// from during the pass waits for it. A parent Starter that depends on the
// export starts after the exported Starter.
//
// The source is found by the name of from among the direct children of its
// parent, at every invocation: a clone of the container resolves the cloned
// scope.
func Export[T any](from *Scope, opts ...ExportOption) error {
	return ExportNamed[T](from, NameOf[T](), opts...)
}

// ExportNamed is like [Export] for the service registered as name in from.
// The service is published as name unless [ExportAs] renames it.
func ExportNamed[T any](from *Scope, name string, opts ...ExportOption) error {
	published := buildExportOptions(name, opts).published
	source, err := validateExport(from, name, published, reflect.TypeFor[T]())
	if err != nil {
		return err
	}

	owner := from.parentScope
	export := wrapExportIfDefault(source, newServiceExport[T](published, owner, from.name, name))

	root := owner.rootScope
	root.opts.onBeforeRegistration(owner, published)

	outcome, err := registerLocked(owner, published, export)
	if err != nil {
		return err
	}
	if outcome == registerSkipped {
		return nil
	}

	root.opts.onAfterRegistration(owner, published)
	root.opts.Logf("DI: service %s exported from scope %s", published, from.name)

	return nil
}

// wrapExportIfDefault returns the wrapper to register for export: decorated
// as a default of its owner when source, the exported service, is a default,
// so that an explicit service of the published name wins over it; export
// itself otherwise.
func wrapExportIfDefault[T any](source any, export *serviceExport[T]) serviceWrapper[T] {
	if _, isDefault := source.(serviceWrapperDefault); isDefault {
		return newServiceDefault[T](export, export.owner)
	}

	return export
}

// validateExport returns the source of an export of sourceName from from,
// published as publishedName and typed target, or the error of the export
// when it is invalid.
func validateExport(from *Scope, sourceName, publishedName string, target reflect.Type) (any, error) {
	if from == nil {
		return nil, errors.New("DI: cannot export from a nil scope")
	}
	if from.parentScope == nil {
		return nil, fmt.Errorf("DI: scope `%s` has no parent scope to export to", from.name)
	}
	if publishedName == "" {
		return nil, errors.New("DI: export name cannot be empty")
	}
	source, ok := from.serviceGet(sourceName)
	if !ok {
		return nil, fmt.Errorf("DI: service `%s` has not been declared", sourceName)
	}
	if !serviceCanCastToType(source, target) {
		return nil, fmt.Errorf("DI: service `%s` is not assignable to `%s`", sourceName, typetostring.GetReflectType(target))
	}

	return source, nil
}

// MustExport is like [Export] but panics with the error Export returns.
func MustExport[T any](from *Scope, opts ...ExportOption) {
	must0(Export[T](from, opts...))
}

// MustExportNamed is like [ExportNamed] but panics with the error
// ExportNamed returns.
func MustExportNamed[T any](from *Scope, name string, opts ...ExportOption) {
	must0(ExportNamed[T](from, name, opts...))
}

var (
	_ serviceWrapper[int]       = (*serviceExport[int])(nil)
	_ serviceWrapperHealthcheck = (*serviceExport[int])(nil)
	_ serviceWrapperShutdown    = (*serviceExport[int])(nil)
	_ serviceWrapperClone       = (*serviceExport[int])(nil)
	_ serviceWrapperExport      = (*serviceExport[int])(nil)
)

// serviceWrapperExport is implemented by exports: aliases bound to a child
// scope of the scope that holds them.
type serviceWrapperExport interface {
	// exportSource returns the scope the export resolves from and the name of
	// its source there; ok is false once that scope is gone.
	exportSource() (scope *Scope, sourceName string, ok bool)
	// withinSource reports whether scope is the scope the export resolves
	// from, or one of its descendants. It takes no lock.
	withinSource(scope *Scope) bool
}

// serviceWrapperUnwrapExport is implemented by the wrappers that decorate
// another one, such as defaults, which may decorate an export.
type serviceWrapperUnwrapExport interface {
	// unwrapExport returns the export the wrapper decorates (nil when it
	// decorates none), and whether the wrapper yielded to another service.
	unwrapExport() (export serviceWrapperExport, yielded bool)
}

// wrapsExport reports whether service is an export, or a default decorating
// one, whatever the default decided: what a scope counts as an export.
func wrapsExport(service any) bool {
	switch wrapper := service.(type) {
	case serviceWrapperExport:
		return true
	case serviceWrapperUnwrapExport:
		export, _ := wrapper.unwrapExport()
		return export != nil
	default:
		return false
	}
}

// chainHasExports reports whether scope or one of its ancestors holds an
// export, so that containers without exports skip their rules.
func chainHasExports(scope *Scope) bool {
	for ; scope != nil; scope = scope.parentScope {
		if scope.exportCount() > 0 {
			return true
		}
	}

	return false
}

// asExport returns service as an export: itself, or the export a default
// decorates, unless that default yielded (it then resolves to another
// service, it is no longer an export).
func asExport(service any) (serviceWrapperExport, bool) {
	switch wrapper := service.(type) {
	case serviceWrapperExport:
		return wrapper, true
	case serviceWrapperUnwrapExport:
		export, yielded := wrapper.unwrapExport()
		return export, export != nil && !yielded
	default:
		return nil, false
	}
}

// readerSeesSource reports whether export is an export and reader is inside
// its source scope (that scope or a descendant), where the exported service
// is visible under its own name.
func readerSeesSource(reader *Scope, export any) bool {
	wrapper, ok := asExport(export)
	return ok && wrapper.withinSource(reader)
}

// serviceExport is the wrapper an export registers in the parent of the
// scope it exports from. It resolves its source by the name of that direct
// child scope at every invocation, so a clone resolves the cloned child and a
// shut down child fails to resolve.
type serviceExport[T any] struct {
	regIndex   uint64 // registration order, unique per root container
	mu         sync.RWMutex
	name       string // the published name
	owner      *Scope // the scope holding the export
	childName  string // the direct child of owner the export resolves from
	sourceName string // the name of the source in that child

	providerFrame           stacktrace.Frame
	invokationFrames        map[stacktrace.Frame]struct{}
	invokationFramesCounter uint32
}

func newServiceExport[T any](name string, owner *Scope, childName, sourceName string) *serviceExport[T] {
	providerFrame, _ := stacktrace.NewFrameFromCaller()

	return &serviceExport[T]{
		name:       name,
		owner:      owner,
		childName:  childName,
		sourceName: sourceName,

		providerFrame:    providerFrame,
		invokationFrames: map[stacktrace.Frame]struct{}{},
	}
}

func (s *serviceExport[T]) exportSource() (*Scope, string, bool) {
	s.owner.mu.RLock()
	child, ok := s.owner.childScopes[s.childName]
	s.owner.mu.RUnlock()

	return child, s.sourceName, ok
}

// withinSource compares names and parents, which never change, so it needs
// no lock and also holds for a source scope that was shut down.
func (s *serviceExport[T]) withinSource(scope *Scope) bool {
	for ; scope != nil; scope = scope.parentScope {
		if scope.parentScope == s.owner && scope.name == s.childName {
			return true
		}
	}

	return false
}

// lookupSource returns the scope the export resolves from and its source
// there, if both still exist.
func (s *serviceExport[T]) lookupSource() (*Scope, any, bool) {
	child, sourceName, ok := s.exportSource()
	if !ok {
		return nil, nil, false
	}
	service, ok := child.serviceGet(sourceName)

	return child, service, ok
}

// sourceNotFound is the error of an export whose source scope or source is
// gone; it matches ErrServiceNotFound and names both scopes.
func (s *serviceExport[T]) sourceNotFound() error {
	return fmt.Errorf("%w `%s` in scope `%s`, exported into scope `%s`", ErrServiceNotFound, s.sourceName, s.childName, s.owner.name)
}

func (s *serviceExport[T]) getName() string              { return s.name }
func (s *serviceExport[T]) getTypeName() string          { return inferServiceName[T]() }
func (s *serviceExport[T]) getServiceType() ServiceType  { return ServiceTypeAlias }
func (s *serviceExport[T]) getReflectType() reflect.Type { return reflect.TypeFor[T]() }

func (s *serviceExport[T]) getInstanceAny(i Injector) (any, error) {
	return s.getInstance(i)
}

func (s *serviceExport[T]) getInstance(i Injector) (T, error) {
	if atomic.AddUint32(&s.invokationFramesCounter, 1) < MaxInvocationFrames {
		frame, ok := stacktrace.NewFrameFromCaller()
		if ok {
			s.mu.Lock()
			s.invokationFrames[frame] = struct{}{}
			s.mu.Unlock()
		}
	}

	child, source, ok := s.lookupSource()
	if !ok {
		return empty[T](), s.sourceNotFound()
	}

	instance, err := invokeWrapperInScope(i, child, s.sourceName, source)
	if err != nil {
		return empty[T](), err
	}

	typed, ok := instance.(T)
	if !ok && instance != nil {
		return empty[T](), serviceTypeMismatch(inferServiceName[T](), source.(serviceWrapperAny).getTypeName()) //nolint:errcheck,forcetypeassert
	}

	return typed, nil
}

// isHealthchecker reports whether the source is a health checker; false
// once the source is gone.
func (s *serviceExport[T]) isHealthchecker() bool {
	_, source, ok := s.lookupSource()
	if !ok {
		return false
	}

	return source.(serviceWrapperIsHealthchecker).isHealthchecker() //nolint:errcheck,forcetypeassert
}

// healthcheck runs the health check of the source; nil once it is gone.
func (s *serviceExport[T]) healthcheck(ctx context.Context) error {
	_, source, ok := s.lookupSource()
	if !ok {
		return nil
	}

	return source.(serviceWrapperHealthcheck).healthcheck(ctx) //nolint:errcheck,forcetypeassert
}

func (s *serviceExport[T]) isShutdowner() bool { return false }

// shutdown never shuts the source down: the child scope owns it.
func (s *serviceExport[T]) shutdown(context.Context) error { return nil }

// clone returns the export bound to newOwner: it resolves the child scope of
// the same name there, never the original's.
func (s *serviceExport[T]) clone(newOwner Injector) any {
	return &serviceExport[T]{
		regIndex:   s.regIndex,
		name:       s.name,
		owner:      injectorScope(newOwner),
		childName:  s.childName,
		sourceName: s.sourceName,

		providerFrame:    s.providerFrame,
		invokationFrames: map[stacktrace.Frame]struct{}{},
	}
}

func (s *serviceExport[T]) source() (stacktrace.Frame, []stacktrace.Frame) {
	s.mu.RLock()
	invokationFrames := make([]stacktrace.Frame, 0, len(s.invokationFrames))
	for frame := range s.invokationFrames {
		invokationFrames = append(invokationFrames, frame)
	}
	s.mu.RUnlock()

	return s.providerFrame, invokationFrames
}

func (s *serviceExport[T]) getRegistrationIndex() uint64 { return s.regIndex }

func (s *serviceExport[T]) setRegistrationIndex(index uint64) { s.regIndex = index }

// getOptions returns nil: an export carries no opt-ins.
func (s *serviceExport[T]) getOptions() *serviceOptions { return nil }

// setOptions ignores opts: an export carries no opt-ins.
func (s *serviceExport[T]) setOptions(*serviceOptions) {}
