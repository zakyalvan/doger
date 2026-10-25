# Implementation notes

Records the measurements and findings that tasks ask to keep.

## Baseline (task 0.2)

- `make test`, `make lint` and per-module `go vet ./... && go test -race ./...` passed on every `go.work` module.
- `BenchmarkNew`: 13 allocs/op, 952 B/op.

## Group 3: implicit Runnable handler (F4)

- `BenchmarkNew`: 11 allocs/op, 896 B/op after the refactor.
- A first-time `Runnable.Handle` keeps the first slot, where `NewWithOpts` used to register the default handler, so handler order is unchanged. `TestRunnable_FirstSlot` pins this; a plain append fails it (verified with a temporary, uncommitted mutation).

## Group 4: healthCheckPool race (F5)

- `go test -race -count=2000 -run TestRootScope_HealthCheckDuringShutdown .` (50 iterations each, 100,000 HealthCheck/Shutdown races): 0 races, 0 panics. Not reproduced; the test stays as a race guard and no fix is made.
