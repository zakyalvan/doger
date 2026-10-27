package doger

import (
	"errors"
	"fmt"
	"os"
)

// exampleKV is the client the example extension provides.
type exampleKV struct{ addr string }

// exampleKVPool is the private pool behind exampleKV.
type exampleKVPool struct{ addr string }

// exampleKVExtension is a small extension as a doger<lib> module would ship
// it: options as plain data, a pointer constructor, services registered as
// defaults and the client exported.
type exampleKVExtension struct {
	Addr        string // the code option; empty means: from properties, then "localhost:6379"
	InstanceKey string // the instance option; empty means: no instance
}

// exampleKVExtensionOf is the extension's constructor.
func exampleKVExtensionOf(addr string) *exampleKVExtension { return &exampleKVExtension{Addr: addr} }

func (e *exampleKVExtension) Name() string     { return "example-kv" }
func (e *exampleKVExtension) Instance() string { return e.InstanceKey }

func (e *exampleKVExtension) Extend(x *ExtensionContext) error {
	// precedence: code option, then properties, then the extension's default
	addr := e.Addr
	if addr == "" {
		addr = "localhost:6379"
		if value, ok := x.Properties().Lookup("addr"); ok {
			addr = value
		}
	}

	Provide(x.Scope(), func(Injector) (*exampleKVPool, error) { return &exampleKVPool{addr: addr}, nil }, AsDefault())
	Provide(x.Scope(), func(i Injector) (*exampleKV, error) {
		return &exampleKV{addr: MustInvoke[*exampleKVPool](i).addr}, nil
	}, AsDefault())

	return ExtensionExport[*exampleKV](x)
}

func ExampleExtend() {
	root := New()

	err := Extend(root, exampleKVExtensionOf("cache:6379"))
	fmt.Println("installed:", err == nil)

	// the client is exported; the pool stays private to the extension's scope
	fmt.Println("client:", MustInvoke[*exampleKV](root).addr)
	_, err = Invoke[*exampleKVPool](root)
	fmt.Println("pool visible from root:", !errors.Is(err, ErrServiceNotFound))
	fmt.Println("private scope:", root.Children()[0].Name())

	// installing the same extension again with the same options is a no-op
	fmt.Println("again:", Extend(root, exampleKVExtensionOf("cache:6379")))
	// with other options, it conflicts
	fmt.Println("conflict:", errors.Is(Extend(root, exampleKVExtensionOf("other:6379")), ErrExtensionConflict))
	// Output:
	// installed: true
	// client: cache:6379
	// pool visible from root: false
	// private scope: ext:example-kv
	// again: <nil>
	// conflict: true
}

func ExampleExtensionExport() {
	root := New()

	// the extension exports its client as a default: the application's own
	// client wins, whether it is registered before or after Extend
	MustExtend := func(ext Extension) {
		if err := Extend(root, ext); err != nil {
			panic(err)
		}
	}
	MustExtend(exampleKVExtensionOf("cache:6379"))
	ProvideValue(root, &exampleKV{addr: "app:6379"})

	fmt.Println(MustInvoke[*exampleKV](root).addr)
	// Output:
	// app:6379
}

func ExampleEnvPropertySource() {
	// the default source reads DOGER_<KEY>: a dot becomes "_", a dash "__",
	// and the slash before an instance key "___"
	_ = os.Setenv("DOGER_EXAMPLE__KV_ADDR", "env:6379")
	defer func() { _ = os.Unsetenv("DOGER_EXAMPLE__KV_ADDR") }()

	fromEnv := New()
	_ = Extend(fromEnv, exampleKVExtensionOf(""))
	fmt.Println(MustInvoke[*exampleKV](fromEnv).addr)

	// the application chooses another source with InjectorOpts.PropertySource
	fromOpts := NewWithOpts(&InjectorOpts{PropertySource: EnvPropertySource("MYAPP_")})
	_ = Extend(fromOpts, exampleKVExtensionOf(""))
	fmt.Println(MustInvoke[*exampleKV](fromOpts).addr)

	// an instance reads "<name>/<instance>.<key>": the slash becomes "___"
	_ = os.Setenv("DOGER_EXAMPLE__KV___QUEUE_ADDR", "queue:6379")
	defer func() { _ = os.Unsetenv("DOGER_EXAMPLE__KV___QUEUE_ADDR") }()

	queue := New()
	_ = Extend(queue, &exampleKVExtension{InstanceKey: "queue"})
	fmt.Println(MustInvoke[*exampleKV](queue).addr)
	// Output:
	// env:6379
	// localhost:6379
	// queue:6379
}
