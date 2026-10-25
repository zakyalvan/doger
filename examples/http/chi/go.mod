module github.com/kartaladev/doger/examples/http/chi

go 1.27

require github.com/go-chi/chi/v5 v5.5.0

require (
	github.com/kartaladev/doger v0.0.0-00010101000000-000000000000
	github.com/kartaladev/doger/http/chi v0.0.0-00010101000000-000000000000
	github.com/samber/go-type-to-string v1.8.0 // indirect
)

replace github.com/kartaladev/doger => ../../../

replace github.com/kartaladev/doger/http/chi => ../../../http/chi
