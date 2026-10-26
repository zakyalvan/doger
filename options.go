package doger

import (
	"fmt"
	"reflect"
	"slices"
)

// serviceOptions records the opt-ins attached to one registration.
type serviceOptions struct {
	starter         bool
	starterDeferred bool // registered under an interface type: checked on the instance at Start
	dependsOn       []string
	kinds           []kindBinding
	order           *int // collection order set by WithOrder; nil when unset
}

// ProvideOption attaches an opt-in to a registration. Values come only from
// the constructors of this package, such as [AsStarter] and [DependsOnNamed].
// A nil option is ignored.
type ProvideOption func(*serviceOptions)

// AsStarter opts a registration into being started by [RootScope.Start].
//
// Starters are detected by registered type, so a service registered under an
// interface type that has no Start method needs AsStarter to be started.
// Repeating it is the same as giving it once.
//
// Registration panics, before the service is stored, when AsStarter is
// applied to a transient service ("DI: transient service `name` cannot be
// started") or to a concrete type with no Start method of a Starter variant
// ("DI: service `name` does not implement any Starter interface"). A service
// registered under an interface type is checked on its instance at Start.
func AsStarter() ProvideOption {
	return func(o *serviceOptions) {
		o.starter = true
	}
}

// DependsOn declares that the registration depends on the service of type T.
// It is a shorthand for DependsOnNamed(NameOf[T]()).
func DependsOn[T any]() ProvideOption {
	return DependsOnNamed(NameOf[T]())
}

// DependsOnNamed declares that the registration depends on the named service.
//
// Repeated declarations accumulate in the given order, and duplicates are
// collapsed. DependsOnNamed never panics at registration, transients
// included: the target does not need to exist yet.
func DependsOnNamed(name string) ProvideOption {
	return func(o *serviceOptions) {
		if !slices.Contains(o.dependsOn, name) {
			o.dependsOn = append(o.dependsOn, name)
		}
	}
}

// WithOrder sets the registration's position in the slices returned by
// InvokeAll: lower values come first. It takes precedence over the
// instance's Order() method, which is then not called. Given more than
// once, the last one wins. It affects only collections, never construction,
// start or shutdown order, and has no effect on Invoke.
func WithOrder(n int) ProvideOption {
	return func(o *serviceOptions) {
		o.order = &n
	}
}

// buildServiceOptions applies opts and returns the resulting options, or nil
// when no non-nil option was given, so registrations without options keep
// their current code paths.
func buildServiceOptions(opts []ProvideOption) *serviceOptions {
	var options *serviceOptions
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if options == nil {
			options = &serviceOptions{}
		}
		opt(options)
	}

	return options
}

// starterTypes are the method sets of the four Starter variants.
var starterTypes = []reflect.Type{
	reflect.TypeFor[Starter](),
	reflect.TypeFor[StarterWithError](),
	reflect.TypeFor[StarterWithContext](),
	reflect.TypeFor[StarterWithContextAndError](),
}

// typeIsStarter reports whether t implements one of the Starter variants.
func typeIsStarter(t reflect.Type) bool {
	return slices.ContainsFunc(starterTypes, func(starter reflect.Type) bool {
		return typeCanCastToType(t, starter)
	})
}

// validateServiceOptions panics, before the service is stored, when opts
// cannot apply to the registration. Interface-typed registrations that do not
// satisfy an opt-in are marked deferred and checked on the instance at Start.
func validateServiceOptions(name string, service serviceWrapperAny, opts *serviceOptions) {
	if opts == nil {
		return
	}

	if service.getServiceType() == ServiceTypeTransient {
		validateTransientOpts(name, opts)
		return
	}

	validateStarterOpt(name, service.getReflectType(), opts)
	validateKindOpt(name, service.getReflectType(), opts)
}

// validateTransientOpts rejects opt-ins that need a single instance.
func validateTransientOpts(name string, opts *serviceOptions) {
	if opts.starter {
		panic(fmt.Errorf("DI: transient service `%s` cannot be started", name))
	}
	if len(opts.kinds) > 0 {
		panic(fmt.Errorf("DI: transient service `%s` cannot be handled by kind `%s`", name, opts.kinds[0].id.name))
	}
}

// validateKindOpt checks kind bindings against the registered type.
func validateKindOpt(name string, registered reflect.Type, opts *serviceOptions) {
	seen := map[*kindID]struct{}{}
	for i := range opts.kinds {
		binding := &opts.kinds[i]
		if _, ok := seen[binding.id]; ok {
			panic(fmt.Errorf("DI: kind `%s` applied twice to service `%s`", binding.id.name, name))
		}
		seen[binding.id] = struct{}{}

		switch {
		case typeCanCastToType(registered, binding.target):
		case registered.Kind() == reflect.Interface:
			binding.deferred = true
		default:
			panic(fmt.Errorf("DI: service `%s` is not assignable to `%s` required by kind `%s`", name, binding.target.String(), binding.id.name))
		}
	}
}

// validateStarterOpt checks AsStarter against the registered type.
func validateStarterOpt(name string, registered reflect.Type, opts *serviceOptions) {
	switch {
	case !opts.starter, typeIsStarter(registered):
	case registered.Kind() == reflect.Interface:
		opts.starterDeferred = true
	default:
		panic(fmt.Errorf("DI: service `%s` does not implement any Starter interface", name))
	}
}

// resolveDeclaredDependencies invokes each declared dependency, in declaration
// order, on the virtual scope the wrapper received. This reuses circular
// dependency detection, DAG edge recording and invocation hooks unchanged.
func resolveDeclaredDependencies(i Injector, opts *serviceOptions) error {
	if opts == nil {
		return nil
	}

	for _, dependency := range opts.dependsOn {
		if _, err := invokeAnyByNameAs(i, dependency, DependencySourceDependsOn); err != nil {
			return err
		}
	}

	return nil
}
