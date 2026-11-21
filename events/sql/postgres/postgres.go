package postgres

import (
	"strings"

	eventsql "github.com/kartaladev/doger/events/sql"
)

// Dialect returns the PostgreSQL 13+ dialect.
func Dialect() eventsql.Dialect { return dialect{} }

// DDL returns the reference statements creating the tables of prefix and
// their indexes, in order, for the application's migration tool; review
// them against your data volumes. It returns an error matching
// eventsql.ErrInvalidTableName for an invalid prefix.
func DDL(prefix string) ([]string, error) {
	tables, err := eventsql.NewTables(prefix)
	if err != nil {
		return nil, err
	}

	return dialect{}.DDL(tables), nil
}

type dialect struct{}

func (dialect) Name() string { return "postgres" }

func (dialect) Statements(t eventsql.Tables) eventsql.Statements {
	groups, events, deliveries := tableNames(t)

	return eventsql.Statements{
		TableExists: `SELECT COUNT(*) FROM information_schema.tables
WHERE table_schema = COALESCE(NULLIF($1, ''), current_schema()) AND table_name = $2`,

		InsertEvent: `INSERT INTO ` + events + ` (event_key, id, destination, event_type, version, content_type, payload, headers, published_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())`,
		FanOut: `INSERT INTO ` + deliveries + ` (event_key, destination, group_name, state, attempts, visible_after, created_at)
SELECT $1::varchar, g.destination, g.group_name, 'pending', 0, now(), now() FROM ` + groups + ` g WHERE g.destination = $2`,
		DeleteEvent: `DELETE FROM ` + events + ` WHERE event_key = $1`,

		UpsertGroup: `INSERT INTO ` + groups + ` (destination, group_name, ephemeral, heartbeat_at) VALUES ($1, $2, $3, now())
ON CONFLICT (destination, group_name) DO UPDATE SET ephemeral = EXCLUDED.ephemeral, heartbeat_at = EXCLUDED.heartbeat_at`,
		DeleteGroup:           `DELETE FROM ` + groups + ` WHERE destination = $1 AND group_name = $2`,
		DeleteGroupDeliveries: `DELETE FROM ` + deliveries + ` WHERE destination = $1 AND group_name = $2`,

		ClaimSelect: `SELECT seq FROM ` + deliveries + `
WHERE destination = $1 AND group_name = $2 AND state = 'pending' AND visible_after <= now()
  AND (lease_until IS NULL OR lease_until < now())
ORDER BY seq LIMIT $3 FOR UPDATE SKIP LOCKED`,
		ClaimMark: `UPDATE ` + deliveries + ` SET lease_owner = $1, lease_token = $2,
  lease_until = now() + $3::bigint * interval '1 microsecond', attempts = attempts + 1 WHERE seq = $4`,
		ClaimLoad: `SELECT d.seq, d.event_key, d.attempts,
  CAST(EXTRACT(EPOCH FROM (d.lease_until - now())) * 1000000 AS BIGINT),
  e.id, e.destination, e.event_type, e.version, e.content_type, e.payload, e.headers, e.published_at
FROM ` + deliveries + ` d JOIN ` + events + ` e ON e.event_key = d.event_key
WHERE d.lease_token = $1 ORDER BY d.seq`,

		LockEvent:         `SELECT event_key FROM ` + events + ` WHERE event_key = $1 FOR UPDATE`,
		AckDelete:         `DELETE FROM ` + deliveries + ` WHERE seq = $1 AND lease_token = $2`,
		DeleteOrphanEvent: `DELETE FROM ` + events + ` WHERE event_key = $1 AND NOT EXISTS (SELECT 1 FROM ` + deliveries + ` d WHERE d.event_key = $2)`,

		Nack: `UPDATE ` + deliveries + ` SET visible_after = now() + $1::bigint * interval '1 microsecond',
  lease_owner = NULL, lease_token = NULL, lease_until = NULL, last_error = $2 WHERE seq = $3 AND lease_token = $4`,
		Dead: `UPDATE ` + deliveries + ` SET state = 'dead', lease_owner = NULL, lease_token = NULL, lease_until = NULL,
  last_error = $1 WHERE seq = $2 AND lease_token = $3`,

		ListDeadLetters: `SELECT d.seq, d.attempts, COALESCE(d.last_error, ''),
  e.id, e.destination, e.event_type, e.version, e.content_type, e.payload, e.headers, e.published_at
FROM ` + deliveries + ` d JOIN ` + events + ` e ON e.event_key = d.event_key
WHERE d.destination = $1 AND d.group_name = $2 AND d.state = 'dead' ORDER BY d.seq LIMIT $3`,
		Resubmit: `UPDATE ` + deliveries + ` SET state = 'pending', attempts = 0, visible_after = now(),
  lease_owner = NULL, lease_token = NULL, lease_until = NULL WHERE seq = $1 AND state = 'dead'`,

		Release: `UPDATE ` + deliveries + ` SET lease_owner = NULL, lease_token = NULL, lease_until = NULL,
  attempts = GREATEST(attempts - 1, 0) WHERE seq = $1 AND lease_token = $2`,
		AckKeep: `UPDATE ` + deliveries + ` SET state = 'delivered', delivered_at = now(),
  lease_owner = NULL, lease_token = NULL, lease_until = NULL WHERE seq = $1 AND lease_token = $2`,

		ListStaleEphemeral: `SELECT destination, group_name FROM ` + groups + `
WHERE ephemeral AND heartbeat_at < now() - $1::bigint * interval '1 microsecond'`,
		DeleteStaleGroup: `DELETE FROM ` + groups + ` WHERE destination = $1 AND group_name = $2
  AND ephemeral AND heartbeat_at < now() - $3::bigint * interval '1 microsecond'`,
		DeleteDelivered: `DELETE FROM ` + deliveries + ` WHERE state = 'delivered'
  AND delivered_at < now() - $1::bigint * interval '1 microsecond'`,
		ListOrphanDeliveries: `SELECT d.seq FROM ` + deliveries + ` d
WHERE NOT EXISTS (SELECT 1 FROM ` + groups + ` g WHERE g.destination = d.destination AND g.group_name = d.group_name) LIMIT $1`,
		DeleteOrphanDelivery: `DELETE FROM ` + deliveries + ` WHERE seq = $1
  AND NOT EXISTS (SELECT 1 FROM ` + groups + ` g WHERE g.destination = ` + deliveries + `.destination AND g.group_name = ` + deliveries + `.group_name)`,
		ListOrphanEvents: `SELECT e.event_key FROM ` + events + ` e
WHERE NOT EXISTS (SELECT 1 FROM ` + deliveries + ` d WHERE d.event_key = e.event_key) LIMIT $1`,
		StaleGroups: `SELECT destination, group_name, COUNT(*) FROM ` + deliveries + `
WHERE state = 'pending' AND created_at < now() - $1::bigint * interval '1 microsecond'
GROUP BY destination, group_name`,
	}
}

func (dialect) DDL(t eventsql.Tables) []string {
	groups, events, deliveries := tableNames(t)
	index := localName(t.Deliveries)

	return []string{
		`CREATE TABLE IF NOT EXISTS ` + groups + ` (
    destination  VARCHAR(255) NOT NULL,
    group_name   VARCHAR(255) NOT NULL,
    ephemeral    BOOLEAN      NOT NULL,
    heartbeat_at TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (destination, group_name)
)`,
		`CREATE TABLE IF NOT EXISTS ` + events + ` (
    event_key    VARCHAR(32)  NOT NULL PRIMARY KEY,
    id           VARCHAR(255) NOT NULL,
    destination  VARCHAR(255) NOT NULL,
    event_type   VARCHAR(255) NOT NULL,
    version      INTEGER      NOT NULL,
    content_type VARCHAR(255) NOT NULL,
    payload      BYTEA        NOT NULL,
    headers      TEXT         NOT NULL,
    published_at BIGINT       NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL
)`,
		`CREATE TABLE IF NOT EXISTS ` + deliveries + ` (
    seq           BIGSERIAL    NOT NULL PRIMARY KEY,
    event_key     VARCHAR(32)  NOT NULL,
    destination   VARCHAR(255) NOT NULL,
    group_name    VARCHAR(255) NOT NULL,
    state         VARCHAR(16)  NOT NULL,
    attempts      INTEGER      NOT NULL,
    visible_after TIMESTAMPTZ  NOT NULL,
    lease_owner   VARCHAR(64),
    lease_token   VARCHAR(32),
    lease_until   TIMESTAMPTZ,
    last_error    TEXT,
    created_at    TIMESTAMPTZ  NOT NULL,
    delivered_at  TIMESTAMPTZ,
    UNIQUE (event_key, group_name)
)`,
		`CREATE INDEX IF NOT EXISTS ` + quote(index+"_claim") + ` ON ` + deliveries +
			` (destination, group_name, state, visible_after, seq)`,
		`CREATE INDEX IF NOT EXISTS ` + quote(index+"_lease") + ` ON ` + deliveries + ` (lease_token)`,
	}
}

func (dialect) SchemaLockSQL() (lock, unlock string) {
	return `SELECT 1 FROM (SELECT pg_advisory_lock(hashtext($1))) AS l`,
		`SELECT CASE WHEN pg_advisory_unlock(hashtext($1)) THEN 1 ELSE 0 END`
}

// tableNames returns the quoted names of the tables, shared by the DDL
// and the statements.
func tableNames(t eventsql.Tables) (groups, events, deliveries string) {
	return quote(t.Groups), quote(t.Events), quote(t.Deliveries)
}

// quote quotes a validated, possibly schema-qualified identifier:
// app.doger_events becomes "app"."doger_events".
func quote(ident string) string {
	parts := strings.Split(ident, ".")
	for i, p := range parts {
		parts[i] = `"` + p + `"`
	}
	return strings.Join(parts, ".")
}

// localName returns ident without its schema: an index lives in the schema
// of its table and is named without one.
func localName(ident string) string {
	return ident[strings.LastIndexByte(ident, '.')+1:]
}
