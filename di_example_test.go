package doger

import (
	"fmt"
)

func ExampleNameOf() {
	type exampleService struct {
		Name string
	}

	name := NameOf[*exampleService]()

	fmt.Println(name)
	// Output: *github.com/kartaladev/doger.exampleService
}

func ExampleProvide() {
	type exampleService struct {
		Name string
	}

	injector := New()

	Provide(injector, func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "test-service"}, nil
	})
	service, err := Invoke[*exampleService](injector)

	fmt.Println(err)
	fmt.Println(service.Name)
	// Output:
	// <nil>
	// test-service
}

func ExampleProvideNamed() {
	type exampleService struct {
		Name string
	}

	injector := New()

	ProvideNamed(injector, "my-service", func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "test-service"}, nil
	})
	service, err := InvokeNamed[*exampleService](injector, "my-service")

	fmt.Println(err)
	fmt.Println(service.Name)
	// Output:
	// <nil>
	// test-service
}

func ExampleProvideValue() {
	type exampleService struct {
		Name string
	}

	injector := New()

	service := &exampleService{Name: "eager-service"}
	ProvideValue(injector, service)
	retrieved, err := Invoke[*exampleService](injector)

	fmt.Println(err)
	fmt.Println(retrieved.Name)
	// Output:
	// <nil>
	// eager-service
}

func ExampleProvideNamedValue() {
	type exampleService struct {
		Name string
	}

	injector := New()

	service := &exampleService{Name: "named-eager-service"}
	ProvideNamedValue(injector, "my-eager-service", service)
	retrieved, err := InvokeNamed[*exampleService](injector, "my-eager-service")

	fmt.Println(err)
	fmt.Println(retrieved.Name)
	// Output:
	// <nil>
	// named-eager-service
}

func ExampleProvideTransient() {
	type exampleService struct {
		Name string
	}

	injector := New()

	ProvideTransient(injector, func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "test-service"}, nil
	})
	service1, _ := Invoke[*exampleService](injector)
	service2, _ := Invoke[*exampleService](injector)

	fmt.Println(service1 != service2)
	// Output: true
}

func ExampleProvideNamedTransient() {
	type exampleService struct {
		Name string
	}

	injector := New()

	ProvideNamedTransient(injector, "transient-service", func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "test-service"}, nil
	})
	service1, _ := InvokeNamed[*exampleService](injector, "transient-service")
	service2, _ := InvokeNamed[*exampleService](injector, "transient-service")

	fmt.Println(service1 != service2)
	// Output: true
}

func ExampleOverride() {
	type exampleService struct {
		Name string
	}

	injector := New()

	Provide(injector, func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "test-service"}, nil
	})
	Override(injector, func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "overridden-service"}, nil
	})
	service, _ := Invoke[*exampleService](injector)

	fmt.Println(service.Name)
	// Output: overridden-service
}

func ExampleOverrideNamed() {
	type exampleService struct {
		Name string
	}

	injector := New()

	ProvideNamed(injector, "my-service", func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "test-service"}, nil
	})
	OverrideNamed(injector, "my-service", func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "overridden-named-service"}, nil
	})
	service, _ := InvokeNamed[*exampleService](injector, "my-service")

	fmt.Println(service.Name)
	// Output: overridden-named-service
}

func ExampleOverrideValue() {
	type exampleService struct {
		Name string
	}

	injector := New()

	Provide(injector, func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "test-service"}, nil
	})
	OverrideValue(injector, &exampleService{Name: "overridden-value-service"})
	service, _ := Invoke[*exampleService](injector)

	fmt.Println(service.Name)
	// Output: overridden-value-service
}

func ExampleOverrideNamedValue() {
	type exampleService struct {
		Name string
	}

	injector := New()

	ProvideNamed(injector, "my-service", func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "test-service"}, nil
	})
	OverrideNamedValue(injector, "my-service", &exampleService{Name: "overridden-named-value-service"})
	service, _ := InvokeNamed[*exampleService](injector, "my-service")

	fmt.Println(service.Name)
	// Output: overridden-named-value-service
}

func ExampleOverrideTransient() {
	type exampleService struct {
		Name string
	}

	injector := New()

	Provide(injector, func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "test-service"}, nil
	})
	OverrideTransient(injector, func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "overridden-service"}, nil
	})
	service1, _ := Invoke[*exampleService](injector)
	service2, _ := Invoke[*exampleService](injector)

	fmt.Println(service1 != service2)
	// Output: true
}

func ExampleOverrideNamedTransient() {
	type exampleService struct {
		Name string
	}

	injector := New()

	ProvideNamed(injector, "my-service", func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "test-service"}, nil
	})
	OverrideNamedTransient(injector, "my-service", func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "overridden-service"}, nil
	})
	service1, _ := InvokeNamed[*exampleService](injector, "my-service")
	service2, _ := InvokeNamed[*exampleService](injector, "my-service")

	fmt.Println(service1 != service2)
	// Output: true
}

func ExampleInvoke() {
	type exampleService struct {
		Name string
	}

	injector := New()

	Provide(injector, func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "test-service"}, nil
	})
	service, err := Invoke[*exampleService](injector)

	fmt.Println(err)
	fmt.Println(service.Name)
	// Output:
	// <nil>
	// test-service
}

func ExampleInvokeNamed() {
	type exampleService struct {
		Name string
	}

	injector := New()

	ProvideNamed(injector, "my-service", func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "test-service"}, nil
	})
	service, err := InvokeNamed[*exampleService](injector, "my-service")

	fmt.Println(err)
	fmt.Println(service.Name)
	// Output:
	// <nil>
	// test-service
}

func ExampleMustInvoke() {
	type exampleService struct {
		Name string
	}

	injector := New()

	Provide(injector, func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "test-service"}, nil
	})
	service := MustInvoke[*exampleService](injector)

	fmt.Println(service.Name)
	// Output: test-service
}

func ExampleMustInvokeNamed() {
	type exampleService struct {
		Name string
	}

	injector := New()

	ProvideNamed(injector, "my-service", func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "test-service"}, nil
	})
	service := MustInvokeNamed[*exampleService](injector, "my-service")

	fmt.Println(service.Name)
	// Output: test-service
}

func ExampleInvokeStruct() {
	type exampleService struct {
		Name string
	}
	type exampleStructService struct {
		Service *exampleService `do:""`
	}

	injector := New()

	Provide(injector, func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "test-service"}, nil
	})
	service, err := InvokeStruct[exampleStructService](injector)

	fmt.Println(err)
	fmt.Println(service.Service.Name)
	// Output:
	// <nil>
	// test-service
}

func ExampleInvokeStruct_named() {
	type exampleService struct {
		Name string
	}
	type exampleStructStruct struct {
		Service *exampleService `do:"my-service"`
	}

	injector := New()

	ProvideNamed(injector, "my-service", func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "test-service"}, nil
	})
	service, err := InvokeStruct[exampleStructStruct](injector)

	fmt.Println(err)
	fmt.Println(service.Service.Name)
	// Output:
	// <nil>
	// test-service
}

func ExampleMustInvokeStruct() {
	type exampleService struct {
		Name string
	}
	type exampleStructService struct {
		Service *exampleService `do:""`
	}

	injector := New()

	Provide(injector, func(i Injector) (*exampleService, error) {
		return &exampleService{Name: "test-service"}, nil
	})
	service := MustInvokeStruct[exampleStructService](injector)

	fmt.Println(service.Service.Name)
	// Output: test-service
}

type IMyService interface {
	GetName() string
}

type myService struct {
	Name string
}

func (s *myService) GetName() string {
	return s.Name
}

func (s *myService) HealthCheck() error {
	return nil
}

func (s *myService) Shutdown() error {
	return nil
}

func aliasMyServiceProvider(i Injector) (*myService, error) {
	return &myService{Name: "alias-test-service"}, nil
}

func ExampleAs() {
	injector := New()

	Provide(injector, aliasMyServiceProvider)
	err := As[*myService, IMyService](injector)

	fmt.Println(err)

	// Now both work:
	service1, _ := Invoke[*myService](injector)
	service2, _ := Invoke[IMyService](injector)

	fmt.Println(service1.GetName())
	fmt.Println(service2.GetName())
	// Output:
	// <nil>
	// alias-test-service
	// alias-test-service
}

func ExampleMustAs() {
	injector := New()

	Provide(injector, aliasMyServiceProvider)
	MustAs[*myService, IMyService](injector)

	// Now both work:
	service1 := MustInvoke[*myService](injector)
	service2 := MustInvoke[IMyService](injector)

	fmt.Println(service1.GetName())
	fmt.Println(service2.GetName())
	// Output:
	// alias-test-service
	// alias-test-service
}

func ExampleAsNamed() {
	injector := New()

	ProvideNamed(injector, "my-service", aliasMyServiceProvider)
	err := AsNamed[*myService, IMyService](injector, "my-service", "my-interface")

	fmt.Println(err)

	// Retrieve using the alias name
	service, _ := InvokeNamed[IMyService](injector, "my-interface")
	fmt.Println(service.GetName())
	// Output:
	// <nil>
	// alias-test-service
}

func ExampleMustAsNamed() {
	injector := New()

	ProvideNamed(injector, "my-service", aliasMyServiceProvider)
	MustAsNamed[*myService, IMyService](injector, "my-service", "my-interface")

	// Retrieve using the alias name
	service := MustInvokeNamed[IMyService](injector, "my-interface")
	fmt.Println(service.GetName())
	// Output: alias-test-service
}

func ExampleInvokeAs() {
	injector := New()

	Provide(injector, aliasMyServiceProvider)
	service, err := InvokeAs[IMyService](injector)

	fmt.Println(err)
	fmt.Println(service.GetName())
	// Output:
	// <nil>
	// alias-test-service
}

func ExampleMustInvokeAs() {
	injector := New()

	Provide(injector, aliasMyServiceProvider)
	service := MustInvokeAs[IMyService](injector)

	fmt.Println(service.GetName())
	// Output: alias-test-service
}

type exampleNotifier interface{ Notify(msg string) string }

type exampleEmailNotifier struct{}

func (*exampleEmailNotifier) Notify(msg string) string { return "email: " + msg }

type exampleSMSNotifier struct{}

func (*exampleSMSNotifier) Notify(msg string) string { return "sms: " + msg }

func ExampleInvokeAll() {
	injector := New()
	Provide(injector, func(Injector) (*exampleEmailNotifier, error) { return &exampleEmailNotifier{}, nil })
	Provide(injector, func(Injector) (*exampleSMSNotifier, error) { return &exampleSMSNotifier{}, nil })
	ProvideValue(injector, "not a notifier")

	// Every service whose registered type implements exampleNotifier, in
	// registration order. With no match, the result is an empty slice.
	notifiers, err := InvokeAll[exampleNotifier](injector)
	if err != nil {
		fmt.Println(err)
		return
	}

	for _, notifier := range notifiers {
		fmt.Println(notifier.Notify("deploy done"))
	}
	// Output:
	// email: deploy done
	// sms: deploy done
}

func ExampleInvokeAllMap() {
	injector := New()
	ProvideNamed(injector, "email", func(Injector) (*exampleEmailNotifier, error) { return &exampleEmailNotifier{}, nil })
	ProvideNamed(injector, "sms", func(Injector) (*exampleSMSNotifier, error) { return &exampleSMSNotifier{}, nil })

	// The same members as InvokeAll, keyed by service name.
	notifiers, err := InvokeAllMap[exampleNotifier](injector)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(notifiers["sms"].Notify("deploy done"))
	fmt.Println(len(notifiers))
	// Output:
	// sms: deploy done
	// 2
}

type exampleMiddleware struct {
	name  string
	order int
}

func (m *exampleMiddleware) Order() int { return m.order }

func ExampleOrdered() {
	injector := New()
	ProvideNamedValue(injector, "logging", &exampleMiddleware{name: "logging", order: 10})
	ProvideNamedValue(injector, "recover", &exampleMiddleware{name: "recover"})
	ProvideNamedValue(injector, "auth", &exampleMiddleware{name: "auth"})

	// Lowest Order() first; equal values keep registration order.
	middlewares := MustInvokeAll[*exampleMiddleware](injector)
	for _, middleware := range middlewares {
		fmt.Println(middleware.name, middleware.Order())
	}
	// Output:
	// recover 0
	// auth 0
	// logging 10
}

// exampleVendorFilter stands for a third-party type you cannot edit to add
// an Order method.
type exampleVendorFilter struct{ name string }

func (f *exampleVendorFilter) Notify(msg string) string { return f.name + ": " + msg }

func ExampleWithOrder() {
	injector := New()
	ProvideNamedValue(injector, "audit", &exampleVendorFilter{name: "audit"})
	ProvideNamedValue(injector, "ratelimit", &exampleVendorFilter{name: "ratelimit"}, WithOrder(-1))

	// WithOrder positions a registration without touching its type.
	for _, notifier := range MustInvokeAll[exampleNotifier](injector) {
		fmt.Println(notifier.Notify("hello"))
	}
	// Output:
	// ratelimit: hello
	// audit: hello
}

type exampleNotifierHub struct {
	All    []exampleNotifier          `do:"*"`
	ByName map[string]exampleNotifier `do:"*"`
}

func ExampleInvokeStruct_collection() {
	injector := New()
	ProvideNamed(injector, "email", func(Injector) (*exampleEmailNotifier, error) { return &exampleEmailNotifier{}, nil })
	ProvideNamed(injector, "sms", func(Injector) (*exampleSMSNotifier, error) { return &exampleSMSNotifier{}, nil })

	// do:"*" fills a slice with InvokeAll and a map with InvokeAllMap.
	hub := MustInvokeStruct[exampleNotifierHub](injector)

	for _, notifier := range hub.All {
		fmt.Println(notifier.Notify("hi"))
	}
	fmt.Println(len(hub.ByName))
	// Output:
	// email: hi
	// sms: hi
	// 2
}

func ExampleInvokeCollection() {
	injector := New()
	ProvideNamed(injector, "email", func(Injector) (*exampleEmailNotifier, error) { return &exampleEmailNotifier{}, nil })

	notifiers := InvokeCollection[exampleNotifier](injector)

	// Registered after the collection was created: still a member.
	ProvideNamed(injector, "sms", func(Injector) (*exampleSMSNotifier, error) { return &exampleSMSNotifier{}, nil })

	all, _ := notifiers.All() // resolved now
	for _, notifier := range all {
		fmt.Println(notifier.Notify("hi"))
	}
	// Output:
	// email: hi
	// sms: hi
}
