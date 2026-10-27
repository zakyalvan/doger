package doger

import (
	"errors"
	"fmt"
)

type examplePool struct{}

func (p *examplePool) Shutdown() { fmt.Println("pool closed") }

type exampleCacheClient struct{ pool *examplePool }

type exampleCacheApp struct{ client *exampleCacheClient }

func (a *exampleCacheApp) Shutdown() { fmt.Println("app closed") }

func ExampleExport() {
	root := New()
	redis := root.Scope("redis")

	// the pool stays private to "redis"; only the client is published
	Provide(redis, func(Injector) (*examplePool, error) { return &examplePool{}, nil })
	Provide(redis, func(i Injector) (*exampleCacheClient, error) {
		return &exampleCacheClient{pool: MustInvoke[*examplePool](i)}, nil
	})
	MustExport[*exampleCacheClient](redis)

	Provide(root, func(i Injector) (*exampleCacheApp, error) {
		return &exampleCacheApp{client: MustInvoke[*exampleCacheClient](i)}, nil
	})
	MustInvoke[*exampleCacheApp](root)

	_, err := Invoke[*examplePool](root)
	fmt.Println("pool visible from root:", !errors.Is(err, ErrServiceNotFound))

	// the app, which depends on the export, shuts down before "redis"
	root.Shutdown()
	// Output:
	// pool visible from root: false
	// app closed
	// pool closed
}

type exampleDatabase struct{ dsn string }

func ExampleExportAs() {
	root := New()
	primary := root.Scope("primary")
	replica := root.Scope("replica")

	// both scopes register the same type: publish them under distinct names
	ProvideValue(primary, &exampleDatabase{dsn: "primary"})
	ProvideValue(replica, &exampleDatabase{dsn: "replica"})
	MustExport[*exampleDatabase](primary, ExportAs("db-primary"))
	MustExport[*exampleDatabase](replica, ExportAs("db-replica"))

	fmt.Println(MustInvokeNamed[*exampleDatabase](root, "db-primary").dsn)
	fmt.Println(MustInvokeNamed[*exampleDatabase](root, "db-replica").dsn)
	// Output:
	// primary
	// replica
}
