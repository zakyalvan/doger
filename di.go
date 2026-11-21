package doger

import (
	"log/slog"
	"reflect"
)

// DefaultStructTagKey is the default tag key used for struct field injection.
// When using struct injection, fields can be tagged with `do:""` or `do:"service-name"`
// to specify which service should be injected.
const DefaultStructTagKey = "do"

// Provider is a function type that creates and returns a service instance of type T.
// This is the core abstraction for service creation in the dependency injection container.
//
// The provider function receives an Injector instance that can be used to resolve
// dependencies for the service being created.
//
// Example:
//
//	func NewMyService(i doger.Injector) (*MyService, error) {
//	    db := doger.MustInvoke[*Database](i)
//	    config := doger.MustInvoke[*Config](i)
//	    return &MyService{DB: db, Config: config}, nil
//	}
//
//	// Register the provider
//	doger.Provide(injector, NewMyService)
type Provider[T any] func(Injector) (T, error)

// NameOf returns the name of the service in the DI container.
// This is highly discouraged to use this function, as your code
// should not declare any dependency explicitly.
//
// The function uses type inference to determine the service name
// based on the generic type parameter T.
//
// Example:
//
//	serviceName := doger.NameOf[*Database]()
func NameOf[T any]() string {
	return inferServiceName[T]()
}

// Provide registers a service in the DI container, using type inference.
// The service will be lazily instantiated when first requested.
//
// Once the shutdown of the scope has begun, Provide and the other Provide*
// and Override* functions panic with an error matching [ErrShutdown]: a
// container cannot be reused after Shutdown.
//
// The provider runs on the goroutine that first invokes the service, once;
// see [Invoke] for concurrent invocations and the cycle errors a provider
// may get from them.
//
// Example:
//
//	doger.Provide(injector, func(i doger.Injector) (*MyService, error) {
//	    return &MyService{...}, nil
//	})
//
// Options: opts attach opt-ins to the registration, such as [AsStarter] or
// [DependsOnNamed]. A nil option is ignored, and a call without options
// behaves exactly as before options existed.
func Provide[T any](i Injector, provider Provider[T], opts ...ProvideOption) {
	name := inferServiceName[T]()
	ProvideNamed(i, name, provider, opts...)
}

// ProvideNamed registers a named service in the DI container.
// This allows you to register multiple services of the same type
// with different names for disambiguation.
//
// The service will be lazily instantiated when first requested.
//
// Example:
//
//	doger.ProvideNamed(injector, "main-db", func(i doger.Injector) (*Database, error) {
//	    return &Database{URL: "postgres://main.acme.dev:5432/db"}, nil
//	})
//	doger.ProvideNamed(injector, "backup-db", func(i doger.Injector) (*Database, error) {
//	    return &Database{URL: "postgres://backup.acme.dev:5432/db"}, nil
//	})
//
// Options: opts attach opt-ins to the registration, such as [AsStarter] or
// [DependsOnNamed]. A nil option is ignored, and a call without options
// behaves exactly as before options existed.
func ProvideNamed[T any](i Injector, name string, provider Provider[T], opts ...ProvideOption) {
	provide(i, name, provider, func(s string, a Provider[T]) serviceWrapper[T] {
		return newServiceLazy(s, a)
	}, opts)
}

// ProvideValue registers a value in the DI container, using type inference to determine the service name.
// The value is immediately available and will not be recreated on each request.
//
// Example:
//
//	ProvideValue(injector, &MyService{})
//
// Options: opts attach opt-ins to the registration, such as [AsStarter] or
// [DependsOnNamed]. A nil option is ignored, and a call without options
// behaves exactly as before options existed.
func ProvideValue[T any](i Injector, value T, opts ...ProvideOption) {
	name := inferServiceName[T]()
	ProvideNamedValue(i, name, value, opts...)
}

// ProvideNamedValue registers a named value in the DI container.
// This allows you to register multiple values of the same type
// with different names for disambiguation.
//
// The value is immediately available and will not be recreated on each request.
//
// Example:
//
//	doger.ProvideNamedValue(injector, "app-config", &Config{Port: 8080})
//	doger.ProvideNamedValue(injector, "db-config", &Config{Port: 5432})
//
// Options: opts attach opt-ins to the registration, such as [AsStarter] or
// [DependsOnNamed]. A nil option is ignored, and a call without options
// behaves exactly as before options existed.
func ProvideNamedValue[T any](i Injector, name string, value T, opts ...ProvideOption) {
	provide(i, name, value, func(s string, a T) serviceWrapper[T] {
		return newServiceEager(s, a)
	}, opts)
}

// ProvideTransient registers a factory in the DI container, using type inference to determine the service name.
// The service will be recreated each time it is requested, providing a fresh instance.
//
// Example:
//
//	// Each invocation creates a new instance
//	doger.ProvideTransient(injector, func(i doger.Injector) (string, error) {
//	    return uuid.New().String(), nil
//	})
//
//	// First invocation
//	id1, _ := doger.Invoke[string](injector)
//	// Second invocation - different instance
//	id2, _ := doger.Invoke[string](injector)
//
//	fmt.Println(id1 != id2) // Output: true
//
// Options: opts attach opt-ins to the registration, such as [AsStarter] or
// [DependsOnNamed]. A nil option is ignored, and a call without options
// behaves exactly as before options existed.
func ProvideTransient[T any](i Injector, provider Provider[T], opts ...ProvideOption) {
	name := inferServiceName[T]()
	ProvideNamedTransient(i, name, provider, opts...)
}

// ProvideNamedTransient registers a named factory in the DI container.
// This allows you to register multiple transient services of the same type
// with different names for disambiguation.
//
// The service will be recreated each time it is requested, providing a fresh instance.
//
// Example:
//
//	// Each invocation creates a new instance
//	doger.ProvideNamedTransient(injector, "request-id", func(i doger.Injector) (string, error) {
//	    return uuid.New().String(), nil
//	})
//
//	// First invocation
//	id1, _ := doger.InvokeNamed[string](injector, "request-id")
//	// Second invocation - different instance
//	id2, _ := doger.InvokeNamed[string](injector, "request-id")
//
//	fmt.Println(id1 != id2) // Output: true
//
// Options: opts attach opt-ins to the registration, such as [AsStarter] or
// [DependsOnNamed]. A nil option is ignored, and a call without options
// behaves exactly as before options existed.
func ProvideNamedTransient[T any](i Injector, name string, provider Provider[T], opts ...ProvideOption) {
	provide(i, name, provider, func(s string, a Provider[T]) serviceWrapper[T] {
		return newServiceTransient(s, a)
	}, opts)
}

// provide is an internal helper function that handles the common logic
// for registering services in the DI container. It ensures that:
// - The injector is properly initialized
// - No duplicate service names are registered
// - The service is properly created and stored
// - Logging is performed for successful registration.
func provide[T any, A any](i Injector, name string, valueOrProvider A, serviceCtor func(string, A) serviceWrapper[T], opts []ProvideOption) {
	if err := tryProvide(i, name, valueOrProvider, serviceCtor, opts); err != nil {
		panic(err)
	}
}

// tryProvide is provide returning the duplicate-registration error instead
// of panicking with it.
func tryProvide[T any, A any](i Injector, name string, valueOrProvider A, serviceCtor func(string, A) serviceWrapper[T], opts []ProvideOption) error {
	_i := mustInjector(i)
	scope := injectorScope(_i)
	if err := scope.checkOpen(); err != nil {
		return err
	}
	options := buildServiceOptions(opts)

	// without a default involved, a duplicate fails first, as it always did
	if (options == nil || !options.isDefault) && nonDefaultExists(scope, name) {
		return duplicateServiceError(name)
	}

	service := serviceCtor(name, valueOrProvider)
	validateServiceOptions(name, service, options)
	scope.rootScope.noteListeners(scope, name, options)
	service.setOptions(options)
	wrapped := wrapIfDefault(scope, service, options)

	root := _i.RootScope()
	root.hooks.onBeforeRegistration(scope, name)

	outcome, err := registerLocked(scope, name, wrapped)
	if err != nil {
		return err
	}
	if outcome == registerSkipped {
		return nil
	}

	root.hooks.onAfterRegistration(scope, name)
	scope.log(slog.LevelDebug, "service registered", slog.String("service", name))

	return nil
}

// registerLocked registers incoming as name in scope under the scope's write
// lock, so deciding between storing, replacing a default and skipping is
// atomic with the write. It runs no hook. A taken name returns the duplicate
// error and leaves the scope unchanged.
func registerLocked(scope *Scope, name string, incoming serviceWrapperAny) (registerOutcome, error) {
	root := scope.rootScope

	scope.mu.Lock()
	if err := scope.checkOpen(); err != nil {
		scope.mu.Unlock()
		return registerDuplicate, err
	}
	replaced, outcome := scope.services[name], registerStored
	if replaced != nil {
		outcome = sameScopeOutcome(replaced, incoming)
	}
	switch outcome {
	case registerStored:
		incoming.setRegistrationIndex(root.nextRegIndex.Add(1) - 1)
		scope.storeLocked(name, replaced, incoming)
	case registerReplaced:
		incoming.setRegistrationIndex(replaced.(serviceWrapperAny).getRegistrationIndex()) //nolint:errcheck,forcetypeassert // every value a scope stores is a service wrapper, which implements serviceWrapperAny
		store := func() { scope.storeLocked(name, replaced, incoming) }
		if !replaced.(serviceWrapperSupersede).supersede(store) { //nolint:errcheck,forcetypeassert // registerReplaced is decided only when replaced is a default, and every serviceWrapperDefault is a *serviceDefault, which implements serviceWrapperSupersede
			outcome = registerDuplicate
		}
	case registerSkipped, registerDuplicate:
	}
	scope.mu.Unlock()

	if outcome == registerDuplicate {
		return outcome, duplicateServiceError(name)
	}
	if outcome == registerReplaced {
		root.forget(replaced)
	}

	return outcome, nil
}

// registerOutcome is what registering a service does to the scope's entry
// of its name.
type registerOutcome int

const (
	registerStored    registerOutcome = iota // the name was free
	registerReplaced                         // an explicit registration replaced an unresolved default
	registerSkipped                          // a default met an explicit service
	registerDuplicate                        // the name is taken
)

// sameScopeOutcome returns what registering incoming does to existing, a
// registration of the same name in the same scope.
func sameScopeOutcome(existing, incoming any) registerOutcome {
	_, existingIsDefault := existing.(serviceWrapperDefault)
	_, incomingIsDefault := incoming.(serviceWrapperDefault)

	switch {
	case existingIsDefault && !incomingIsDefault:
		return registerReplaced
	case !existingIsDefault && incomingIsDefault:
		return registerSkipped
	default:
		return registerDuplicate
	}
}

// nonDefaultExists reports whether scope itself registers name, other than
// as a default.
func nonDefaultExists(scope *Scope, name string) bool {
	service, ok := scope.serviceGet(name)
	if !ok {
		return false
	}
	_, isDefault := service.(serviceWrapperDefault)

	return !isDefault
}

// duplicateServiceError is the error of a registration whose name is taken.
func duplicateServiceError(name string) error {
	return libErrorMatching(ErrServiceAlreadyDeclared, "service `%s` has already been declared", name)
}

// duplicateScopeError is the panic value of a child scope declaration
// whose name its parent already has.
func duplicateScopeError(name string) error {
	return libErrorMatching(ErrScopeAlreadyDeclared, "scope `%s` has already been declared", name)
}

// wrapIfDefault returns the wrapper to store for service in scope: decorated
// as a default when options carry AsDefault, service itself otherwise.
func wrapIfDefault[T any](scope *Scope, service serviceWrapper[T], options *serviceOptions) serviceWrapper[T] {
	if options != nil && options.isDefault {
		return newServiceDefault(service, scope)
	}

	return service
}

// assignRegistrationIndex returns the registration index for name in scope.
// A name already registered in scope itself keeps its index, so an override
// keeps the position of the first registration. Otherwise the next index of
// the root container is taken.
func assignRegistrationIndex(scope Injector, name string) uint64 {
	if previous, ok := scope.serviceGet(name); ok {
		if indexed, ok := previous.(interface{ getRegistrationIndex() uint64 }); ok {
			return indexed.getRegistrationIndex()
		}
	}

	return scope.RootScope().nextRegIndex.Add(1) - 1
}

// Override replaces the service in the DI container, using type inference to determine the service name.
// Warning: this will not unload/shutdown the previously invoked service.
//
// This function is useful for testing or when you need to replace a service
// that has already been registered. However, be cautious as it may lead to
// resource leaks if the original service was already instantiated.
//
// Options: opts attach opt-ins to the registration, such as [AsStarter] or
// [DependsOnNamed]. A nil option is ignored, and a call without options
// behaves exactly as before options existed.
func Override[T any](i Injector, provider Provider[T], opts ...ProvideOption) {
	name := inferServiceName[T]()
	OverrideNamed(i, name, provider, opts...)
}

// OverrideNamed replaces the named service in the DI container.
// Warning: this will not unload/shutdown the previously invoked service.
//
// This function allows you to replace a specific named service that has
// already been registered. Use with caution to avoid resource leaks.
//
// Options: opts attach opt-ins to the registration, such as [AsStarter] or
// [DependsOnNamed]. A nil option is ignored, and a call without options
// behaves exactly as before options existed.
func OverrideNamed[T any](i Injector, name string, provider Provider[T], opts ...ProvideOption) {
	override(i, name, provider, func(s string, a Provider[T]) serviceWrapper[T] {
		return newServiceLazy(s, a)
	}, opts)
}

// OverrideValue replaces the value in the DI container, using type inference to determine the service name.
// Warning: this will not unload/shutdown the previously invoked service.
//
// This function replaces an existing value service with a new one.
// The old value will not be properly cleaned up if it was already instantiated.
//
// Options: opts attach opt-ins to the registration, such as [AsStarter] or
// [DependsOnNamed]. A nil option is ignored, and a call without options
// behaves exactly as before options existed.
func OverrideValue[T any](i Injector, value T, opts ...ProvideOption) {
	name := inferServiceName[T]()
	OverrideNamedValue(i, name, value, opts...)
}

// OverrideNamedValue replaces the named value in the DI container.
// Warning: this will not unload/shutdown the previously invoked service.
//
// This function allows you to replace a specific named value service.
// Use with caution to avoid resource leaks.
//
// Options: opts attach opt-ins to the registration, such as [AsStarter] or
// [DependsOnNamed]. A nil option is ignored, and a call without options
// behaves exactly as before options existed.
func OverrideNamedValue[T any](i Injector, name string, value T, opts ...ProvideOption) {
	override(i, name, value, func(s string, a T) serviceWrapper[T] {
		return newServiceEager(s, a)
	}, opts)
}

// OverrideTransient replaces the factory in the DI container, using type inference to determine the service name.
// Warning: this will not unload/shutdown the previously invoked service.
//
// This function replaces an existing transient service factory with a new one.
// Since transient services are recreated on each request, this is generally safer
// than overriding lazy or eager services.
//
// Options: opts attach opt-ins to the registration, such as [AsStarter] or
// [DependsOnNamed]. A nil option is ignored, and a call without options
// behaves exactly as before options existed.
func OverrideTransient[T any](i Injector, provider Provider[T], opts ...ProvideOption) {
	name := inferServiceName[T]()
	OverrideNamedTransient(i, name, provider, opts...)
}

// OverrideNamedTransient replaces the named factory in the DI container.
// Warning: this will not unload/shutdown the previously invoked service.
//
// This function allows you to replace a specific named transient service factory.
// Since transient services are recreated on each request, this is generally safer
// than overriding lazy or eager services.
//
// Options: opts attach opt-ins to the registration, such as [AsStarter] or
// [DependsOnNamed]. A nil option is ignored, and a call without options
// behaves exactly as before options existed.
func OverrideNamedTransient[T any](i Injector, name string, provider Provider[T], opts ...ProvideOption) {
	override(i, name, provider, func(s string, a Provider[T]) serviceWrapper[T] {
		return newServiceTransient(s, a)
	}, opts)
}

// override is an internal helper function that handles the common logic
// for overriding services in the DI container. Unlike provide, it allows
// replacing existing services without throwing an error.
func override[T any, A any](i Injector, name string, valueOrProvider A, serviceCtor func(string, A) serviceWrapper[T], opts []ProvideOption) {
	_i := mustInjector(i)

	// Note: We don't check if the service exists here, allowing override
	service := serviceCtor(name, valueOrProvider)
	options := buildServiceOptions(opts)
	validateServiceOptions(name, service, options)
	_i.RootScope().noteListeners(injectorScope(_i), name, options)
	if options != nil {
		options.isDefault = false // an override is explicit
	}
	service.setRegistrationIndex(assignRegistrationIndex(_i, name))
	service.setOptions(options)
	_i.serviceSet(name, service) // @TODO: should we unload/shutdown the previous service ?

	injectorScope(_i).log(slog.LevelDebug, "service overridden", slog.String("service", name))
}

// Invoke retrieves and instantiates a service from the DI container using type inference.
// The service will be created if it hasn't been instantiated yet (for lazy services).
//
// Once the shutdown of the invoking scope has begun, Invoke and the other
// Invoke* functions return an error matching [ErrShutdown]. A service is
// never built again after its shutdown: an invocation that looked it up just
// before gets ErrShutdown too.
//
// Concurrency: a lazy service is built once, however many goroutines invoke
// it; the others wait for that build and share its outcome, its error
// included. Resolution never deadlocks inside the container: a cycle met by
// concurrent first invocations, or a provider resolving its own service
// again through a captured injector, returns an error matching
// [ErrCircularDependency] instead of blocking, and a later invocation builds
// again. Two cases are not detected: a transient provider re-entering itself
// through a captured injector recurses until Go reports a stack overflow,
// and a provider that waits for a goroutine it spawned, which resolves the
// provider's own service, blocks.
//
// Example:
//
//	service, err := doger.Invoke[*MyService](injector)
func Invoke[T any](i Injector) (T, error) {
	name := inferServiceName[T]()
	return invokeByName[T](i, name)
}

// InvokeNamed retrieves and instantiates a named service from the DI container.
// This allows you to retrieve specific named services when multiple services
// of the same type are registered.
//
// Example:
//
//	// Register multiple databases
//	doger.ProvideNamed(injector, "main-db", func(i doger.Injector) (*Database, error) {
//	    return &Database{URL: "postgres://main.acme.dev:5432/db"}, nil
//	})
//	doger.ProvideNamed(injector, "backup-db", func(i doger.Injector) (*Database, error) {
//	    return &Database{URL: "postgres://backup.acme.dev:5432/db"}, nil
//	})
//
//	// Retrieve specific database
//	mainDB, err := doger.InvokeNamed[*Database](injector, "main-db")
//	backupDB, err := doger.InvokeNamed[*Database](injector, "backup-db")
func InvokeNamed[T any](i Injector, name string) (T, error) {
	if typeIsAssignable[T, any]() {
		v, err := invokeAnyByName(i, name)
		t, _ := v.(T) // just skip if v == nil
		return t, err
	}

	return invokeByName[T](i, name)
}

// MustInvoke retrieves and instantiates a service from the DI container using type inference.
// If the service cannot be retrieved or instantiated, it panics.
//
// This function is useful when you're certain the service exists and want
// to avoid error handling in your code.
//
// Example:
//
//	service := doger.MustInvoke[*MyService](injector)
func MustInvoke[T any](i Injector) T {
	return must1(Invoke[T](i))
}

// MustInvokeNamed retrieves and instantiates a named service from the DI container.
// If the service cannot be retrieved or instantiated, it panics.
//
// This function is useful when you're certain the named service exists and want
// to avoid error handling in your code.
//
// Example:
//
//	service := doger.MustInvokeNamed[*MyService](injector, "my-service")
func MustInvokeNamed[T any](i Injector, name string) T {
	return must1(InvokeNamed[T](i, name))
}

// InvokeStruct invokes services located in struct properties.
// The struct fields must be tagged with `do:""` or `do:"name"`, where `name` is the service name in the DI container.
// If the service is not found in the DI container, an error is returned.
// If the service is found but not assignable to the struct field, an error is returned.
//
// An empty tag (`do:""`) names the service after the field type. When no
// visible service has that name, or, called from a provider, only the
// service that provider builds has it, InvokeStruct falls back to resolution
// by type, selecting among assignable services with the rule of [InvokeAs].
// The fallback depends on the name only: a service with that name that fails
// to build returns its error, even one matching [ErrServiceNotFound].
//
// The tag value `*` injects a collection: a []E field receives
// [InvokeAll][E] and a map[string]E field [InvokeAllMap][E]; any other field
// kind is an error. A service named `*` cannot be injected by tag.
//
// A dependency whose value is a nil interface sets its field to the zero
// value. An invalid target (not a struct or a pointer chain to one, a
// recursive pointer type, a service not assignable to its field, a `*` tag
// on another field kind) returns an error matching [ErrStructInjection]; a
// dependency that fails to resolve returns its own error instead.
//
// Example:
//
//	type App struct {
//	    Database *Database `do:""`
//	    Logger   *Logger   `do:"app-logger"`
//	    Config   *Config   `do:""`
//	}
//
//	// Register services
//	doger.Provide(injector, func(i doger.Injector) (*Database, error) {
//	    return &Database{}, nil
//	})
//	doger.ProvideNamed(injector, "app-logger", func(i doger.Injector) (*Logger, error) {
//	    return &Logger{}, nil
//	})
//	doger.Provide(injector, func(i doger.Injector) (*Config, error) {
//	    return &Config{}, nil
//	})
//
//	// Invoke struct with injected services
//	app, err := doger.InvokeStruct[App](injector)
func InvokeStruct[T any](i Injector) (T, error) {
	injector, err := getInjector(i)
	if err != nil {
		return empty[T](), err
	}
	if err := injectorScope(injector).checkOpen(); err != nil {
		return empty[T](), err
	}

	structName := inferServiceName[T]()
	output, err := deepEmpty[T]() // if the struct is hidden behind a pointer, we need to init the struct value deep enough
	if err != nil {
		return empty[T](), err
	}
	value := reflect.ValueOf(&output)

	for value.Elem().Kind() == reflect.Pointer {
		value = value.Elem()
	}

	// Check if the empty value is a struct (before passing a pointer to reflect.ValueOf).
	// It will be checked in invokeByTags, but the error message is different.
	if value.Kind() != reflect.Pointer || value.Elem().Kind() != reflect.Struct {
		return empty[T](), structInjectionError("must be a struct or a pointer to a struct, but got `%s`", structName)
	}

	err = invokeByTags(i, structName, value, true)
	if err != nil {
		return empty[T](), err
	}

	return output, nil
}

// MustInvokeStruct invokes services located in struct properties and panics on error.
// The struct fields must be tagged with `do:""` or `do:"name"`, where `name` is the service name in the DI container.
// If the service is not found in the DI container, it panics.
// If the service is found but not assignable to the struct field, it panics.
//
// Example:
//
//	type App struct {
//	    Database *Database `do:""`
//	    Logger   *Logger   `do:"app-logger"`
//	    Config   *Config   `do:""`
//	}
//
//	// Register services
//	doger.Provide(injector, func(i doger.Injector) (*Database, error) {
//	    return &Database{}, nil
//	})
//	doger.ProvideNamed(injector, "app-logger", func(i doger.Injector) (*Logger, error) {
//	    return &Logger{}, nil
//	})
//	doger.Provide(injector, func(i doger.Injector) (*Config, error) {
//	    return &Config{}, nil
//	})
//
//	// Invoke struct with injected services (panics on error)
//	app := doger.MustInvokeStruct[App](injector)
func MustInvokeStruct[T any](i Injector) T {
	return must1(InvokeStruct[T](i))
}

/////////////////////////////////////////////////////////////////////////////
// 							Explicit aliases
/////////////////////////////////////////////////////////////////////////////

// Alias registers, in the scope of i, an alias named after Target that
// resolves the service named after Initial, so the service can be invoked
// as Target. It replaces the As function of samber/do; [Bind] is its
// package-loading form.
//
// Alias returns an error and registers nothing when Initial does not
// implement Target (the message names both types), when no visible scope
// registers Initial (matching [ErrServiceNotFound]), when the scope of i
// already registers the alias name (matching [ErrServiceAlreadyDeclared]),
// or when i is nil (matching [ErrNilInjector]).
//
// Shutting the alias down (with its scope, or with Shutdown/ShutdownNamed on
// the alias name) removes the alias but never shuts its target down: the
// target is shut down only through its own registration, exactly once.
//
// Example:
//
//	// Register a concrete service
//	doger.Provide(injector, func(i doger.Injector) (*PostgresqlDatabase, error) {
//	    return &PostgresqlDatabase{}, nil
//	})
//
//	// Create an alias so it can be retrieved as an interface
//	doger.Alias[*PostgresqlDatabase, Database](injector)
//
//	// Now both work:
//	db := doger.MustInvoke[*PostgresqlDatabase](injector)
//	db := doger.MustInvoke[Database](injector)
func Alias[Initial any, Target any](i Injector) error {
	initialName := NameOf[Initial]()
	aliasName := NameOf[Target]()

	return AliasNamed[Initial, Target](i, initialName, aliasName)
}

// MustAlias is like [Alias] but panics with the error Alias would return.
//
// Example:
//
//	doger.MustAlias[*PostgresqlDatabase, Database](injector)
func MustAlias[Initial any, Target any](i Injector) {
	must0(Alias[Initial, Target](i))
}

// AliasNamed registers, in the scope of i, an alias named alias that
// resolves the service named initial, typed Initial, as Target. It is
// [Alias] with explicit names, and returns the same errors; [BindNamed] is
// its package-loading form.
//
// Shutting the alias down (with its scope, or with Shutdown/ShutdownNamed on
// the alias name) removes the alias but never shuts its target down: the
// target is shut down only through its own registration, exactly once.
//
// Example:
//
//	// Register a service with a custom name
//	doger.ProvideNamed(injector, "my-db", func(i doger.Injector) (*PostgresqlDatabase, error) {
//	    return &PostgresqlDatabase{}, nil
//	})
//
//	// Create an alias with custom names
//	doger.AliasNamed[*PostgresqlDatabase, Database](injector, "my-db", "db-interface")
//
//	// Retrieve using the alias name
//	db := doger.MustInvokeNamed[Database](injector, "db-interface")
func AliasNamed[Initial any, Target any](i Injector, initial string, alias string) error {
	_i, err := getInjector(i)
	if err != nil {
		return err
	}
	if err := injectorScope(_i).checkOpen(); err != nil {
		return err
	}

	// first, we check if Initial can be cast to Target
	if !genericCanCastToGeneric[Initial, Target]() {
		return libErrorf("`%s` does not implement `%s`", NameOf[Initial](), NameOf[Target]())
	}

	if ok := _i.serviceExistRec(initial); !ok {
		return libErrorMatching(ErrServiceNotFound, "service `%s` has not been declared", initial)
	}

	return tryProvide(_i, alias, nil, func(_ string, _ any) serviceWrapper[Target] {
		return newServiceAlias[Initial, Target](alias, _i, initial)
	}, nil)
}

// MustAliasNamed is like [AliasNamed] but panics with the error AliasNamed
// would return.
//
// Example:
//
//	doger.MustAliasNamed[*PostgresqlDatabase, Database](injector, "my-db", "db-interface")
func MustAliasNamed[Initial any, Target any](i Injector, initial string, alias string) {
	must0(AliasNamed[Initial, Target](i, initial, alias))
}

/////////////////////////////////////////////////////////////////////////////
// 							Collections
/////////////////////////////////////////////////////////////////////////////

// InvokeAll invokes every visible service whose registered type is
// assignable to T, and returns them as a slice. A nil injector gives
// [ErrNilInjector].
//
// Membership is decided by registered type, without building anything: a
// service is a member when the type it was registered with is assignable to
// T, so services of other types are never built. Eager values are members
// as is. Beware InvokeAll[any]: every service matches, so every one is built.
//
// Members come ancestors first: by scope depth from the root scope down to
// the invoking scope, then by registration order within a scope (an
// override keeps the position of the registration it replaces). The order
// is the same across calls and in clones.
//
// With no match, InvokeAll returns an empty, non-nil slice and a nil error,
// never ErrServiceNotFound. When a member fails to build, it returns nil and
// an error naming the member and wrapping its cause; members built before
// it stay built.
//
// Visibility follows scope ancestry: services of the invoking scope and of
// its ancestors, never of siblings or children. When a name exists in
// several of these scopes, only the nearest service is considered, even if
// it is not assignable to T: it then hides the ancestor's, as InvokeNamed
// would. Inside a provider, the invoking scope is the scope the provider's
// service is registered in. Aliases (Alias, Bind) are never members: their
// target represents itself.
//
// A default registration ([AsDefault]) not resolved yet is left out when a
// non-default member is assignable to its registered type, and neither it
// nor a default that yielded hides a non-default of its name in an ancestor.
// Including a default in the result resolves it.
//
// Inside a provider, the service that provider builds is not a member, so a
// composite can collect every other implementation of its own interface.
// Any other member already in the invocation chain still fails the call
// with ErrCircularDependency.
//
// Invocation hooks fire once per member, with the member's name, as for N
// calls to Invoke. The result is a snapshot: services registered later are
// not added to it, and changing it does not affect later calls; transient
// members give a new instance on every call. Resolve once and keep the
// result outside hot loops.
func InvokeAll[T any](i Injector) ([]T, error) {
	injector, err := getInjector(i)
	if err != nil {
		return nil, err
	}

	return invokeAllByGenericType[T](collectionFrom(injector))
}

// MustInvokeAll is like [InvokeAll] but panics with the error InvokeAll
// would return.
func MustInvokeAll[T any](i Injector) []T {
	return must1(InvokeAll[T](i))
}

// InvokeAllMap returns the same members as [InvokeAll], keyed by service
// name, under the same visibility, alias, self-exclusion, shadowing and
// failure rules. With no match, it returns an empty, non-nil map.
func InvokeAllMap[T any](i Injector) (map[string]T, error) {
	injector, err := getInjector(i)
	if err != nil {
		return nil, err
	}

	return invokeAllMapByGenericType[T](collectionFrom(injector))
}

// MustInvokeAllMap is like [InvokeAllMap] but panics with the error
// InvokeAllMap would return.
func MustInvokeAllMap[T any](i Injector) map[string]T {
	return must1(InvokeAllMap[T](i))
}

/////////////////////////////////////////////////////////////////////////////
// 							Implicit aliases
/////////////////////////////////////////////////////////////////////////////

// InvokeAs invokes a service in the DI container by finding the first service that matches the provided type or interface.
// This function searches through all registered services to find one that can be cast to the requested type T.
// It's useful when you want to retrieve a service by interface without explicitly creating aliases.
//
// When several visible services are assignable to T, it selects the one in
// the nearest scope (the invoking scope before its ancestors), then the
// earliest registration (an override keeps its position), then the smallest
// service name. Aliases are candidates too. A service hidden by a service
// of the same name in a nearer scope is not a candidate, as for InvokeAll.
// Called from a provider, the service that provider builds is not a
// candidate either, nor is an alias whose target chain resolves to it, so a
// decorator can resolve the service it wraps; when it is the only match, the
// error matches ErrServiceNotMatch. This holds for an injector a provider
// stored and uses after it returned too: it still skips that provider's
// service.
// Use InvokeNamed to pin a specific service. A selected service whose value
// is a nil interface gives the zero value of T and a nil error.
//
// Parameters:
//   - i: The injector to search for the service
//
// Returns the service instance and any error that occurred during invocation.
//
// Example:
//
//	// Register a concrete service
//	doger.Provide(injector, func(i doger.Injector) (*PostgresqlDatabase, error) {
//	    return &PostgresqlDatabase{}, nil
//	})
//
//	// Retrieve by interface
//	db, err := doger.InvokeAs[Database](injector)
func InvokeAs[T any](i Injector) (T, error) {
	return invokeByGenericType[T](i)
}

// MustInvokeAs invokes a service in the DI container by finding the first service that matches the provided type or interface.
// This function panics if an error occurs during invocation.
// It's useful when you want to retrieve a service by interface without explicitly creating aliases.
// The selection rule is the one of [InvokeAs].
//
// Parameters:
//   - i: The injector to search for the service
//
// Returns the service instance.
// Panics if the service cannot be found or invoked.
//
// Example:
//
//	// Register a concrete service
//	doger.Provide(injector, func(i doger.Injector) (*PostgresqlDatabase, error) {
//	    return &PostgresqlDatabase{}, nil
//	})
//
//	// Retrieve by interface
//	db := doger.MustInvokeAs[Database](injector)
func MustInvokeAs[T any](i Injector) T {
	return must1(InvokeAs[T](i))
}

/////////////////////////////////////////////////////////////////////////////
// 							Package-level declaration
/////////////////////////////////////////////////////////////////////////////

// Package creates a function that executes multiple service registration functions.
// This function is used to group related service registrations into reusable packages
// that can be applied to any injector instance.
//
// Parameters:
//   - services: Variable number of service registration functions to execute
//
// Returns a function that can be passed to New(), NewWithOpts(), or Scope() to register all services.
//
// Example:
//
//		 // pkg/database/package.go
//			// Create a global `Package` variable
//			var Package = doger.Package(
//			    doger.Lazy[*Database](func(i doger.Injector) (*Database, error) {
//			        return &Database{}, nil
//			    }),
//			    doger.Lazy[*ConnectionPool](func(i doger.Injector) (*ConnectionPool, error) {
//			        return &ConnectionPool{}, nil
//			    }),
//	         ...
//			)
//
//		 // main.go
//			// Apply the package to an injector
//			injector := doger.New(database.Package)
func Package(services ...func(i Injector)) func(Injector) {
	return func(injector Injector) {
		for i := range services {
			services[i](injector)
		}
	}
}

// Lazy creates a function that registers a lazy service using the default service name.
// This function is a convenience wrapper for creating lazy service registration functions
// that can be used in packages.
//
// Parameters:
//   - p: The provider function that creates the service instance
//
// Returns a function that registers the service as lazy when executed.
//
// Example:
//
//	// Global to a package
//	var Package = doger.Package(
//		doger.Lazy[*Database](func(i doger.Injector) (*Database, error) {
//	    	return &Database{}, nil
//		}),
//	)
//
// Options: opts attach opt-ins to the registration, such as [AsStarter] or
// [DependsOnNamed]. A nil option is ignored, and a call without options
// behaves exactly as before options existed.
func Lazy[T any](p Provider[T], opts ...ProvideOption) func(Injector) {
	return func(injector Injector) {
		Provide(injector, p, opts...)
	}
}

// LazyNamed creates a function that registers a lazy service with a custom name.
// This function is a convenience wrapper for creating named lazy service registration functions
// that can be used in packages.
//
// Parameters:
//   - serviceName: The custom name for the service
//   - p: The provider function that creates the service instance
//
// Returns a function that registers the service as lazy with the specified name when executed.
//
// Example:
//
//	// Global to a package
//	var Package = doger.Package(
//		doger.LazyNamed[*Database]("main-db", func(i doger.Injector) (*Database, error) {
//	    	return &Database{}, nil
//		}),
//	)
//
// Options: opts attach opt-ins to the registration, such as [AsStarter] or
// [DependsOnNamed]. A nil option is ignored, and a call without options
// behaves exactly as before options existed.
func LazyNamed[T any](serviceName string, p Provider[T], opts ...ProvideOption) func(Injector) {
	return func(injector Injector) {
		ProvideNamed(injector, serviceName, p, opts...)
	}
}

// Eager creates a function that registers an eager service using the default service name.
// This function is a convenience wrapper for creating eager service registration functions
// that can be used in packages. The service value is provided directly.
//
// Parameters:
//   - value: The service instance to register eagerly
//
// Returns a function that registers the service as eager when executed.
//
// Example:
//
//	// Global to a package
//	var Package = doger.Package(
//		doger.Eager[*Config](&Config{Port: 8080})
//	)
//
// Options: opts attach opt-ins to the registration, such as [AsStarter] or
// [DependsOnNamed]. A nil option is ignored, and a call without options
// behaves exactly as before options existed.
func Eager[T any](value T, opts ...ProvideOption) func(Injector) {
	return func(injector Injector) {
		ProvideValue(injector, value, opts...)
	}
}

// EagerNamed creates a function that registers an eager service with a custom name.
// This function is a convenience wrapper for creating named eager service registration functions
// that can be used in packages. The service value is provided directly.
//
// Parameters:
//   - serviceName: The custom name for the service
//   - value: The service instance to register eagerly
//
// Returns a function that registers the service as eager with the specified name when executed.
//
// Example:
//
//	// Global to a package
//	var Package = doger.Package(
//		doger.EagerNamed[*Config]("app-config", &Config{Port: 8080})
//	)
//
// Options: opts attach opt-ins to the registration, such as [AsStarter] or
// [DependsOnNamed]. A nil option is ignored, and a call without options
// behaves exactly as before options existed.
func EagerNamed[T any](serviceName string, value T, opts ...ProvideOption) func(Injector) {
	return func(injector Injector) {
		ProvideNamedValue(injector, serviceName, value, opts...)
	}
}

// Transient creates a function that registers a transient service using the default service name.
// This function is a convenience wrapper for creating transient service registration functions
// that can be used in packages.
//
// Parameters:
//   - p: The provider function that creates the service instance
//
// Returns a function that registers the service as transient when executed.
//
// Example:
//
//	// Global to a package
//	var Package = doger.Package(
//		doger.Transient[*Logger](func(i doger.Injector) (*Logger, error) {
//	    	return &Logger{}, nil
//		})
//	)
//
// Options: opts attach opt-ins to the registration, such as [AsStarter] or
// [DependsOnNamed]. A nil option is ignored, and a call without options
// behaves exactly as before options existed.
func Transient[T any](p Provider[T], opts ...ProvideOption) func(Injector) {
	return func(injector Injector) {
		ProvideTransient(injector, p, opts...)
	}
}

// TransientNamed creates a function that registers a transient service with a custom name.
// This function is a convenience wrapper for creating named transient service registration functions
// that can be used in packages.
//
// Parameters:
//   - serviceName: The custom name for the service
//   - p: The provider function that creates the service instance
//
// Returns a function that registers the service as transient with the specified name when executed.
//
// Example:
//
//	// Global to a package
//	var Package = doger.Package(
//		doger.TransientNamed[*Logger]("request-logger", func(i doger.Injector) (*Logger, error) {
//	    	return &Logger{}, nil
//		})
//	)
//
// Options: opts attach opt-ins to the registration, such as [AsStarter] or
// [DependsOnNamed]. A nil option is ignored, and a call without options
// behaves exactly as before options existed.
func TransientNamed[T any](serviceName string, p Provider[T], opts ...ProvideOption) func(Injector) {
	return func(injector Injector) {
		ProvideNamedTransient(injector, serviceName, p, opts...)
	}
}

// Bind creates a function that creates a type alias between two types.
// This function is a convenience wrapper for creating service binding functions
// that can be used in packages.
//
// Parameters:
//   - Initial: The original type to bind from
//   - Target: The type to bind to (must be implemented by Initial)
//
// Returns a function that creates the type alias when executed.
// Panics if the binding cannot be created. Like [Alias], the alias never shuts
// its target down.
//
// Example:
//
//	// Global to a package
//	var Package = doger.Package(
//		doger.Bind[*Database, DatabaseInterface]()
//	)
func Bind[Initial any, Target any]() func(Injector) {
	return func(injector Injector) {
		MustAlias[Initial, Target](injector)
	}
}

// BindNamed creates a function that creates a named type alias between two types.
// This function is a convenience wrapper for creating named service binding functions
// that can be used in packages.
//
// Parameters:
//   - initial: The name of the original service
//   - alias: The name for the alias service
//   - Initial: The original type to bind from
//   - Target: The type to bind to (must be implemented by Initial)
//
// Returns a function that creates the named type alias when executed.
// Panics if the binding cannot be created. Like [AliasNamed], the alias never
// shuts its target down.
//
// Example:
//
//	// Global to a package
//	var Package = doger.Package(
//		doger.BindNamed[*Database, DatabaseInterface]("main-db", "db-interface")
//	)
func BindNamed[Initial any, Target any](initial string, alias string) func(Injector) {
	return func(injector Injector) {
		MustAliasNamed[Initial, Target](injector, initial, alias)
	}
}
