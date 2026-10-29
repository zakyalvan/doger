module github.com/kartaladev/doger/scheduler/gocron

go 1.27

require (
	github.com/go-co-op/gocron/v2 v2.23.0
	github.com/google/uuid v1.6.0
	github.com/jonboulle/clockwork v0.5.0
	github.com/kartaladev/doger v0.0.0-00010101000000-000000000000
	github.com/stretchr/testify v1.12.1
	go.uber.org/goleak v1.3.0
)

require (
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/samber/go-type-to-string v1.8.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
)

replace github.com/kartaladev/doger => ../../
