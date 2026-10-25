package doger

import (
	"fmt"
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
// Play: https://go.dev/play/p/g549GqBbj-n
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
// Play: https://go.dev/play/p/4JutUJ5Rqau
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
// Play: https://go.dev/play/p/9JuTQhLGIlh
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
// Play: https://go.dev/play/p/5TOSiI-c17Y
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
// Play: https://go.dev/play/p/hzOJwtNXwT9
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
	_i := getInjectorOrDefault(i)
	if _i.serviceExist(name) {
		panic(fmt.Errorf("DI: service `%s` has already been declared", name))
	}

	service := serviceCtor(name, valueOrProvider)
	options := buildServiceOptions(opts)
	validateServiceOptions(name, service, options)
	service.setRegistrationIndex(assignRegistrationIndex(_i, name))
	service.setOptions(options)
	_i.serviceSet(name, service)

	_i.RootScope().opts.Logf("DI: service %s injected", name)
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
// Play: https://go.dev/play/p/g549GqBbj-n
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
// Play: https://go.dev/play/p/-gNF1BUEB5Q
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
// Play: https://go.dev/play/p/-gNF1BUEB5Q
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
// Play: https://go.dev/play/p/-gNF1BUEB5Q
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
// Play: https://go.dev/play/p/_wYwBADbCaN
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
// Play: https://go.dev/play/p/_wYwBADbCaN
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
	_i := getInjectorOrDefault(i)

	// Note: We don't check if the service exists here, allowing override
	service := serviceCtor(name, valueOrProvider)
	options := buildServiceOptions(opts)
	validateServiceOptions(name, service, options)
	service.setRegistrationIndex(assignRegistrationIndex(_i, name))
	service.setOptions(options)
	_i.serviceSet(name, service) // @TODO: should we unload/shutdown the previous service ?

	_i.RootScope().opts.Logf("DI: service %s overridden", name)
}

// Invoke retrieves and instantiates a service from the DI container using type inference.
// The service will be created if it hasn't been instantiated yet (for lazy services).
//
// Play: https://go.dev/play/p/4JutUJ5Rqau
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
// Play: https://go.dev/play/p/9JuTQhLGIlh
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
// Play: https://go.dev/play/p/456pBhI36Q2
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
// Play: https://go.dev/play/p/456pBhI36Q2
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
// Play: https://go.dev/play/p/I3_Rznkprpj
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
	structName := inferServiceName[T]()
	output := deepEmpty[T]() // if the struct is hidden behind a pointer, we need to init the struct value deep enough
	value := reflect.ValueOf(&output)

	for value.Elem().Kind() == reflect.Pointer {
		value = value.Elem()
	}

	// Check if the empty value is a struct (before passing a pointer to reflect.ValueOf).
	// It will be checked in invokeByTags, but the error message is different.
	if value.Kind() != reflect.Pointer || value.Elem().Kind() != reflect.Struct {
		return empty[T](), fmt.Errorf("DI: must be a struct or a pointer to a struct, but got `%s`", structName)
	}

	err := invokeByTags(i, structName, value, true)
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
// Play: https://go.dev/play/p/lRKqRT9TQVf
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

// As declares an alias for a service, allowing it to be retrieved using a different type.
// This function creates a type alias where the Alias type can be used to retrieve the Initial service.
// The alias is automatically named using the type name of Alias.
//
// Parameters:
//   - i: The injector to register the alias in
//
// Returns an error if the alias cannot be created (e.g., type incompatibility or missing service).
//
// Play: https://go.dev/play/p/T5zKBRZaZhj
//
// Example:
//
//	// Register a concrete service
//	doger.Provide(injector, func(i doger.Injector) (*PostgresqlDatabase, error) {
//	    return &PostgresqlDatabase{}, nil
//	})
//
//	// Create an alias so it can be retrieved as an interface
//	doger.As[*PostgresqlDatabase, Database](injector)
//
//	// Now both work:
//	db := doger.MustInvoke[*PostgresqlDatabase](injector)
//	db := doger.MustInvoke[Database](injector)
func As[Initial any, Alias any](i Injector) error {
	initialName := NameOf[Initial]()
	aliasName := NameOf[Alias]()

	return AsNamed[Initial, Alias](i, initialName, aliasName)
}

// MustAs declares an alias for a service and panics if an error occurs.
// This is a convenience function that wraps As and panics on error.
//
// Parameters:
//   - i: The injector to register the alias in
//
// Panics if the alias cannot be created (e.g., type incompatibility or missing service).
//
// Play: https://go.dev/play/p/_wGjnRJfwV8
//
// Example:
//
//	doger.MustAs[*PostgresqlDatabase, Database](injector)
func MustAs[Initial any, Alias any](i Injector) {
	must0(As[Initial, Alias](i))
}

// AsNamed declares a named alias for a named service.
// This function allows you to create aliases with custom names for both the initial service and the alias.
//
// Parameters:
//   - i: The injector to register the alias in
//   - initial: The name of the existing service to alias
//   - alias: The name for the new alias service
//
// Returns an error if the alias cannot be created (e.g., type incompatibility or missing service).
//
// Play: https://go.dev/play/p/h1R5rxKizwR
//
// Example:
//
//	// Register a service with a custom name
//	doger.ProvideNamed(injector, "my-db", func(i doger.Injector) (*PostgresqlDatabase, error) {
//	    return &PostgresqlDatabase{}, nil
//	})
//
//	// Create an alias with custom names
//	doger.AsNamed[*PostgresqlDatabase, Database](injector, "my-db", "db-interface")
//
//	// Retrieve using the alias name
//	db := doger.MustInvokeNamed[Database](injector, "db-interface")
func AsNamed[Initial any, Alias any](i Injector, initial string, alias string) error {
	// first, we check if Initial can be cast to Alias
	if !genericCanCastToGeneric[Initial, Alias]() {
		return fmt.Errorf("DI: `%s` does not implement `%s`", initial, alias)
	}

	_i := getInjectorOrDefault(i)
	if ok := _i.serviceExistRec(initial); !ok {
		return fmt.Errorf("DI: service `%s` has not been declared", initial)
	}

	provide(i, alias, nil, func(_ string, _ any) serviceWrapper[Alias] {
		return newServiceAlias[Initial, Alias](alias, i, initial)
	}, nil)

	return nil
}

// MustAsNamed declares a named alias for a named service and panics if an error occurs.
// This is a convenience function that wraps AsNamed and panics on error.
//
// Parameters:
//   - i: The injector to register the alias in
//   - initial: The name of the existing service to alias
//   - alias: The name for the new alias service
//
// Panics if the alias cannot be created (e.g., type incompatibility or missing service).
//
// Play: https://go.dev/play/p/8QI2XUm9yLH
//
// Example:
//
//	doger.MustAsNamed[*PostgresqlDatabase, Database](injector, "my-db", "db-interface")
func MustAsNamed[Initial any, Alias any](i Injector, initial string, alias string) {
	must0(AsNamed[Initial, Alias](i, initial, alias))
}

/////////////////////////////////////////////////////////////////////////////
// 							Implicit aliases
/////////////////////////////////////////////////////////////////////////////

// InvokeAs invokes a service in the DI container by finding the first service that matches the provided type or interface.
// This function searches through all registered services to find one that can be cast to the requested type T.
// It's useful when you want to retrieve a service by interface without explicitly creating aliases.
//
// Parameters:
//   - i: The injector to search for the service
//
// Returns the service instance and any error that occurred during invocation.
//
// Play: https://go.dev/play/p/1uXvNenBbgk
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
//
// Parameters:
//   - i: The injector to search for the service
//
// Returns the service instance.
// Panics if the service cannot be found or invoked.
//
// Play: https://go.dev/play/p/29gb2TJG4m5
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
// Play: https://go.dev/play/p/kmf8aOVyj96
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
// Play: https://go.dev/play/p/M6-wd1qt-GZ
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
// Play: https://go.dev/play/p/xAs-exXR9Sz
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
// Play: https://go.dev/play/p/M6-wd1qt-GZ
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
// Play: https://go.dev/play/p/pvByI4EkFEJ
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
// Play: https://go.dev/play/p/M6-wd1qt-GZ
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
// Play: https://go.dev/play/p/d9IJOAbKRUH
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
//   - Alias: The type to bind to (must be implemented by Initial)
//
// Returns a function that creates the type alias when executed.
// Panics if the binding cannot be created.
//
// Play: https://go.dev/play/p/j69I52whJr2
//
// Example:
//
//	// Global to a package
//	var Package = doger.Package(
//		doger.Bind[*Database, DatabaseInterface]()
//	)
func Bind[Initial any, Alias any]() func(Injector) {
	return func(injector Injector) {
		MustAs[Initial, Alias](injector)
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
//   - Alias: The type to bind to (must be implemented by Initial)
//
// Returns a function that creates the named type alias when executed.
// Panics if the binding cannot be created.
//
// Play: https://go.dev/play/p/m2gW7s9_qgq
//
// Example:
//
//	// Global to a package
//	var Package = doger.Package(
//		doger.BindNamed[*Database, DatabaseInterface]("main-db", "db-interface")
//	)
func BindNamed[Initial any, Alias any](initial string, alias string) func(Injector) {
	return func(injector Injector) {
		MustAsNamed[Initial, Alias](injector, initial, alias)
	}
}
