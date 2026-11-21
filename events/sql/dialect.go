package eventsql

import (
	"fmt"
	"regexp"
)

// Dialect is the SQL of one database engine. It holds SQL text only and
// imports no driver. postgres.Dialect() and mysql.Dialect() are the
// supported ones; another engine with FOR UPDATE SKIP LOCKED (for example
// MariaDB 10.6+) can implement it outside this module.
type Dialect interface {
	// Name names the engine, for logs and errors.
	Name() string
	// Statements returns the statements of the transport on tables t,
	// with every identifier quoted.
	Statements(t Tables) Statements
	// DDL returns the statements creating the tables t and their
	// indexes, each idempotent (IF NOT EXISTS).
	DDL(t Tables) []string
	// SchemaLockSQL returns the statements taking and releasing the
	// session lock that serializes EnsureSchema; each takes one argument,
	// the lock name, and each returns a single row whose single column is
	// 1 once the lock is held, respectively released. A connection whose
	// release does not return 1 is discarded, not pooled.
	SchemaLockSQL() (lock, unlock string)
}

// Tables names the tables of a transport: the prefix followed by
// "events", "event_deliveries" and "event_groups". The names are
// validated, unquoted, and may be schema-qualified ("app.doger_events").
type Tables struct {
	Prefix     string
	Events     string
	Deliveries string
	Groups     string
}

// prefixPattern is what a table prefix must match: an optional schema and
// a name start, each an SQL identifier without quoting.
var prefixPattern = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*\.)?[A-Za-z_][A-Za-z0-9_]*$`)

// maxPrefixBytes is the longest table prefix: with "event_deliveries" the
// longest table name stays within the identifier limits of PostgreSQL (63)
// and MySQL (64).
const maxPrefixBytes = 40

// NewTables returns the tables for prefix, or an error matching
// ErrInvalidTableName when prefix does not match
// ^([A-Za-z_][A-Za-z0-9_]*\.)?[A-Za-z_][A-Za-z0-9_]*$ or is longer than 40
// bytes. Dialects call it so that no unvalidated identifier reaches SQL.
func NewTables(prefix string) (Tables, error) {
	if err := validateIdent(prefix); err != nil {
		return Tables{}, err
	}

	return Tables{
		Prefix:     prefix,
		Events:     prefix + "events",
		Deliveries: prefix + "event_deliveries",
		Groups:     prefix + "event_groups",
	}, nil
}

// validateIdent returns an error matching ErrInvalidTableName unless
// prefix is a valid table prefix.
func validateIdent(prefix string) error {
	if len(prefix) > maxPrefixBytes || !prefixPattern.MatchString(prefix) {
		return fmt.Errorf("%w: table prefix %q must match %s and be at most %d bytes",
			ErrInvalidTableName, prefix, prefixPattern, maxPrefixBytes)
	}

	return nil
}

// Statements are the SQL statements of the transport, built by a Dialect
// for a set of Tables. Placeholders follow the driver of the engine ($n
// for PostgreSQL, ? for MySQL); each field documents its arguments, in
// order.
type Statements struct {
	// TableExists counts the tables named $2 in schema $1, the current
	// schema (or database) when $1 is empty.
	TableExists string

	// InsertEvent stores an event: event_key, id, destination,
	// event_type, version, content_type, payload, headers, published_at
	// (Unix nanoseconds); created_at is the database time.
	InsertEvent string
	// FanOut inserts a pending delivery of event_key ($1) for every group
	// registered for destination ($2), visible at once.
	FanOut string
	// DeleteEvent deletes the event event_key ($1).
	DeleteEvent string

	// UpsertGroup registers group_name ($2) for destination ($1), with
	// ephemeral ($3) and the heartbeat at the database time; an existing
	// registration is updated.
	UpsertGroup string
	// DeleteGroup unregisters group_name ($2) of destination ($1).
	DeleteGroup string
	// DeleteGroupDeliveries deletes the deliveries of group_name ($2) of
	// destination ($1).
	DeleteGroupDeliveries string

	// ClaimSelect locks, skipping locked rows, the seq of up to $3
	// claimable deliveries of group_name ($2) of destination ($1), oldest
	// first: pending, visible, and not leased or with an expired lease.
	ClaimSelect string
	// ClaimMark leases delivery seq ($4) to lease_owner ($1) with
	// lease_token ($2) for $3 microseconds from the database time, and
	// counts an attempt.
	ClaimMark string
	// ClaimLoad returns the deliveries leased with lease_token ($1), oldest
	// first, joined to their events: seq, event_key, attempts, the lease's
	// remaining microseconds by the database clock, id, destination,
	// event_type, version, content_type, payload, headers, published_at.
	ClaimLoad string

	// LockEvent locks the event event_key ($1) (SELECT ... FOR UPDATE), so
	// the acknowledgements of one event run one at a time.
	LockEvent string
	// AckDelete deletes delivery seq ($1) while it is leased with
	// lease_token ($2).
	AckDelete string
	// DeleteOrphanEvent deletes the event event_key ($1) when no delivery
	// of event_key ($2, the same key) remains.
	DeleteOrphanEvent string

	// Nack makes delivery seq ($3), while leased with lease_token ($4),
	// visible again $1 microseconds from the database time, with
	// last_error ($2), and ends its lease.
	Nack string
	// Dead dead-letters delivery seq ($2), while leased with lease_token
	// ($3), with last_error ($1), and ends its lease.
	Dead string

	// ListDeadLetters returns up to $3 dead deliveries of group_name ($2)
	// of destination ($1), oldest first, joined to their events: seq,
	// attempts, last_error, id, destination, event_type, version,
	// content_type, payload, headers, published_at.
	ListDeadLetters string
	// Resubmit makes the dead delivery seq ($1) pending and visible at
	// once, with its attempts reset.
	Resubmit string

	// Release ends the lease of delivery seq ($1), while leased with
	// lease_token ($2), uncounting its attempt: a stopping member hands it
	// to the others at once.
	Release string
	// AckKeep marks delivery seq ($1), while leased with lease_token ($2),
	// delivered at the database time and ends its lease (KeepDelivered).
	AckKeep string

	// ListStaleEphemeral returns the destination and group_name of the
	// ephemeral groups whose heartbeat is older than $1 microseconds.
	ListStaleEphemeral string
	// DeleteStaleGroup unregisters group_name ($2) of destination ($1)
	// when it is ephemeral and its heartbeat is older than $3
	// microseconds.
	DeleteStaleGroup string
	// DeleteDelivered deletes the delivered deliveries older than $1
	// microseconds.
	DeleteDelivered string
	// ListOrphanDeliveries returns the seq of up to $1 deliveries whose
	// destination and group_name are not registered.
	ListOrphanDeliveries string
	// DeleteOrphanDelivery deletes delivery seq ($1) when its destination
	// and group_name are not registered.
	DeleteOrphanDelivery string
	// ListOrphanEvents returns the event_key of up to $1 events no delivery
	// refers to.
	ListOrphanEvents string
	// StaleGroups returns destination, group_name and the count of the
	// pending deliveries older than $1 microseconds, per group.
	StaleGroups string
}
