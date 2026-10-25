package doger

import (
	"context"
	"fmt"
	"reflect"
	"slices"
)

// serviceOptions records the opt-ins attached to one registration.
type serviceOptions struct {
	starter         bool
	starterDeferred bool // registered under an interface type: checked on the instance at Start
	dependsOn       []string
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
	reflect.TypeFor[interface{ Start() }](),
	reflect.TypeFor[interface{ Start() error }](),
	reflect.TypeFor[interface{ Start(context.Context) }](),
	reflect.TypeFor[interface{ Start(context.Context) error }](),
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
}

// validateTransientOpts rejects opt-ins that need a single instance.
func validateTransientOpts(name string, opts *serviceOptions) {
	if opts.starter {
		panic(fmt.Errorf("DI: transient service `%s` cannot be started", name))
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
