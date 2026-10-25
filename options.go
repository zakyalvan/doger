package doger

// serviceOptions records the opt-ins attached to one registration.
type serviceOptions struct {
	starter   bool
	dependsOn []string
}

// ProvideOption attaches an opt-in to a registration. Values come only from
// the constructors of this package, such as [AsStarter] and [DependsOnNamed].
// A nil option is ignored.
type ProvideOption func(*serviceOptions)

// AsStarter opts a registration into being started by [RootScope.Start].
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
func DependsOnNamed(name string) ProvideOption {
	return func(o *serviceOptions) {
		o.dependsOn = append(o.dependsOn, name)
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
