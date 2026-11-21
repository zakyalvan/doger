package eventsql

import (
	"errors"
	"log/slog"
	"time"
)

// Errors of New.
var (
	// ErrNilDB is matched by the error of New for a nil *sql.DB.
	ErrNilDB = errors.New("doger: eventsql: nil *sql.DB")
	// ErrNilDialect is matched by the error of New for a nil Dialect.
	ErrNilDialect = errors.New("doger: eventsql: nil Dialect")
	// ErrInvalidOption is matched by the error of New for an invalid
	// option, whose message names the option.
	ErrInvalidOption = errors.New("doger: eventsql: invalid option")
	// ErrInvalidTableName is matched by the error of New, and of the
	// dialects' DDL, for an invalid table prefix.
	ErrInvalidTableName = errors.New("doger: eventsql: invalid table name")
)

// Option configures a Transport (see New). Given more than once, the last
// one wins.
type Option func(*config)

// Defaults of the options.
const (
	defaultPrefix          = "doger_"
	defaultPollInterval    = time.Second
	defaultBatchSize       = 16
	defaultWorkers         = 4
	defaultLease           = 30 * time.Second
	defaultMaxAttempts     = 10
	defaultEphemeralTTL    = 5 * time.Minute
	defaultMaxPayloadBytes = 1 << 20
)

// TablePrefix sets the prefix of the table names: prefix + "events",
// "event_deliveries" and "event_groups". Default "doger_". It may name a
// schema ("app.doger_"). New fails with ErrInvalidTableName unless it
// matches ^([A-Za-z_][A-Za-z0-9_]*\.)?[A-Za-z_][A-Za-z0-9_]*$ and is at
// most 40 bytes.
func TablePrefix(prefix string) Option { return func(c *config) { c.tables.Prefix = prefix } }

// PollInterval sets how long a subscription waits before claiming again
// when its last claim found fewer deliveries than it asked for. Default
// 1s; a full batch claims again at once. It must be positive.
func PollInterval(d time.Duration) Option { return func(c *config) { c.pollInterval = d } }

// BatchSize sets how many deliveries a subscription claims at most at
// once. Default 16. It must be at least 1.
func BatchSize(n int) Option { return func(c *config) { c.batchSize = n } }

// Workers sets how many handlers of a subscription run at once. Default 4.
// It must be at least 1.
func Workers(n int) Option { return func(c *config) { c.workers = n } }

// Lease sets how long a claimed delivery stays reserved for the member
// that claimed it, which is also the handler's deadline (minus a margin).
// A delivery whose lease expired, after a crash or a slow handler, is
// claimed again. Default 30s. It must be positive.
func Lease(d time.Duration) Option { return func(c *config) { c.lease = d } }

// MaxAttempts sets how many times a delivery is handed to a handler that
// keeps failing before it is dead-lettered. Default 10. It must be at
// least 1.
func MaxAttempts(n int) Option { return func(c *config) { c.maxAttempts = n } }

// EphemeralTTL sets how long an ephemeral group (a DeliverAll group with a
// generated instance ID) survives without a heartbeat before a running
// subscription removes it with its deliveries, after a crash. Default 5m.
// It must be positive.
func EphemeralTTL(d time.Duration) Option { return func(c *config) { c.ephemeralTTL = d } }

// KeepDelivered keeps acknowledged deliveries for d, marked delivered,
// for auditing; they are deleted after d. Default 0: deleted at once. It
// must not be negative.
func KeepDelivered(d time.Duration) Option { return func(c *config) { c.keepDelivered = d } }

// MaxPayloadBytes sets the largest payload Send and SendTx accept; a
// larger one fails with a permanent error matching ErrPayloadTooLarge.
// Default 1 MiB. It must be at least 1.
func MaxPayloadBytes(n int) Option { return func(c *config) { c.maxPayloadBytes = n } }

// Backoff sets how long a delivery whose handler failed waits before its
// next attempt. Default: DefaultBackoff(), exponential from 1s, ×2, capped
// at 5m, ±10% jitter. It must not be nil.
func Backoff(policy BackoffPolicy) Option { return func(c *config) { c.backoff = policy } }

// Logger sets where the transport logs what it cannot return: lost
// leases, failing claims and cleanups, stale groups. Default
// slog.Default(). It must not be nil.
func Logger(l *slog.Logger) Option { return func(c *config) { c.logger = l } }
