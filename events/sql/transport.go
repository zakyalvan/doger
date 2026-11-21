package eventsql

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/kartaladev/doger"
)

// Transport is the SQL event transport; see New.
type Transport struct {
	db      *sql.DB
	dialect Dialect
	cfg     config
	stmts   Statements // the dialect's statements on cfg.tables, built once
	maint   maintenance
}

var _ doger.EventTransport = (*Transport)(nil)

// config is the validated configuration of a Transport.
type config struct {
	tables          Tables
	pollInterval    time.Duration
	batchSize       int
	workers         int
	lease           time.Duration
	maxAttempts     int
	ephemeralTTL    time.Duration
	keepDelivered   time.Duration
	maxPayloadBytes int
	backoff         BackoffPolicy
	logger          *slog.Logger
}

// New returns a transport storing its events in db with the SQL of
// dialect. It returns an error matching ErrNilDB, ErrNilDialect,
// ErrInvalidOption (naming the option) or ErrInvalidTableName, and never
// contacts the database: create the schema with a migration (see the
// dialects' DDL) or EnsureSchema.
func New(db *sql.DB, dialect Dialect, opts ...Option) (*Transport, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if dialect == nil {
		return nil, ErrNilDialect
	}
	cfg, err := newConfig(opts)
	if err != nil {
		return nil, err
	}

	return &Transport{db: db, dialect: dialect, cfg: cfg, stmts: dialect.Statements(cfg.tables)}, nil
}

// newConfig returns the defaults with opts applied, validated.
func newConfig(opts []Option) (config, error) {
	c := config{
		tables:          Tables{Prefix: defaultPrefix},
		pollInterval:    defaultPollInterval,
		batchSize:       defaultBatchSize,
		workers:         defaultWorkers,
		lease:           defaultLease,
		maxAttempts:     defaultMaxAttempts,
		ephemeralTTL:    defaultEphemeralTTL,
		maxPayloadBytes: defaultMaxPayloadBytes,
		backoff:         DefaultBackoff(),
		logger:          slog.Default(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&c)
		}
	}
	if err := c.validate(); err != nil {
		return config{}, err
	}
	tables, err := NewTables(c.tables.Prefix)
	if err != nil {
		return config{}, err
	}
	c.tables = tables

	return c, nil
}

// validate returns an error matching ErrInvalidOption, naming the option,
// for the first invalid value.
func (c *config) validate() error {
	switch {
	case c.pollInterval <= 0:
		return fmt.Errorf("%w: PollInterval must be positive, got %v", ErrInvalidOption, c.pollInterval)
	case c.batchSize < 1:
		return fmt.Errorf("%w: BatchSize must be at least 1, got %d", ErrInvalidOption, c.batchSize)
	case c.workers < 1:
		return fmt.Errorf("%w: Workers must be at least 1, got %d", ErrInvalidOption, c.workers)
	case c.lease <= 0:
		return fmt.Errorf("%w: Lease must be positive, got %v", ErrInvalidOption, c.lease)
	case c.maxAttempts < 1:
		return fmt.Errorf("%w: MaxAttempts must be at least 1, got %d", ErrInvalidOption, c.maxAttempts)
	case c.ephemeralTTL <= 0:
		return fmt.Errorf("%w: EphemeralTTL must be positive, got %v", ErrInvalidOption, c.ephemeralTTL)
	case c.keepDelivered < 0:
		return fmt.Errorf("%w: KeepDelivered must not be negative, got %v", ErrInvalidOption, c.keepDelivered)
	case c.maxPayloadBytes < 1:
		return fmt.Errorf("%w: MaxPayloadBytes must be at least 1, got %d", ErrInvalidOption, c.maxPayloadBytes)
	case c.backoff == nil:
		return fmt.Errorf("%w: Backoff must not be nil", ErrInvalidOption)
	case c.logger == nil:
		return fmt.Errorf("%w: Logger must not be nil", ErrInvalidOption)
	}

	return nil
}
