package database

import (
	"fmt"

	"github.com/kartaladev/doger"
)

// DatabasePackage is the global package for database-related services.
var DatabasePackage = doger.Package(
	doger.Lazy(func(i doger.Injector) (*Database, error) {
		config := doger.MustInvokeAs[Configuration](i)
		return &Database{
			URL:       fmt.Sprintf("postgres://localhost:5432/%s", config.GetAppName()),
			Connected: false,
		}, nil
	}),
	doger.Eager(&Cache{
		Data: make(map[string]any),
	}),
)
