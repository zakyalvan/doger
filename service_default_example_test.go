package doger

import "fmt"

type examplePropertySource interface{ Lookup(key string) string }

type exampleEnvSource struct{}

func (s *exampleEnvSource) Lookup(key string) string { return "env:" + key }

type exampleVaultSource struct{}

func (s *exampleVaultSource) Lookup(key string) string { return "vault:" + key }

type exampleDialer struct{ name string }

func ExampleAsDefault() {
	root := New()
	ext := root.Scope("ext")

	// a package registers its dialer in its own scope, as a default
	Provide(ext, func(Injector) (*exampleDialer, error) { return &exampleDialer{name: "package dialer"}, nil }, AsDefault())
	// the application registers its own, afterwards: it still wins
	Provide(root, func(Injector) (*exampleDialer, error) { return &exampleDialer{name: "app dialer"}, nil })

	fmt.Println(MustInvoke[*exampleDialer](ext).name)

	explained, _ := ExplainNamedService(ext, NameOf[*exampleDialer]())
	fmt.Println(explained.DefaultState, "to", explained.DefaultYieldedToScopeName)
	// Output:
	// app dialer
	// yielded to [root]
}

func ExampleAsDefault_byType() {
	root := New()

	// a package ships a default source, resolved by type
	ProvideNamed(root, "env", func(Injector) (*exampleEnvSource, error) { return &exampleEnvSource{}, nil }, AsDefault())
	// the application registers its own, under any name
	ProvideNamed(root, "vault", func(Injector) (*exampleVaultSource, error) { return &exampleVaultSource{}, nil })

	source := MustInvokeAs[examplePropertySource](root)
	fmt.Println(source.Lookup("db.password"))
	// Output: vault:db.password
}
