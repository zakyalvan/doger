package mysql

import (
	"strings"

	eventsql "github.com/kartaladev/doger/events/sql"
)

// Dialect returns the MySQL 8.0+ dialect.
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

func (dialect) Name() string { return "mysql" }

func (dialect) Statements(t eventsql.Tables) eventsql.Statements {
	groups, events, deliveries := tableNames(t)

	return eventsql.Statements{
		TableExists: `SELECT COUNT(*) FROM information_schema.tables
WHERE table_schema = COALESCE(NULLIF(?, ''), DATABASE()) AND table_name = ?`,

		InsertEvent: `INSERT INTO ` + events + ` (event_key, id, destination, event_type, version, content_type, payload, headers, published_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ` + now + `)`,
		FanOut: `INSERT INTO ` + deliveries + ` (event_key, destination, group_name, state, attempts, visible_after, created_at)
SELECT ?, g.destination, g.group_name, 'pending', 0, ` + now + `, ` + now + ` FROM ` + groups + ` g WHERE g.destination = ?`,
		DeleteEvent: `DELETE FROM ` + events + ` WHERE event_key = ?`,

		UpsertGroup: `INSERT INTO ` + groups + ` (destination, group_name, ephemeral, heartbeat_at) VALUES (?, ?, ?, ` + now + `)
ON DUPLICATE KEY UPDATE ephemeral = VALUES(ephemeral), heartbeat_at = VALUES(heartbeat_at)`,
		DeleteGroup:           `DELETE FROM ` + groups + ` WHERE destination = ? AND group_name = ?`,
		DeleteGroupDeliveries: `DELETE FROM ` + deliveries + ` WHERE destination = ? AND group_name = ?`,

		ClaimSelect: `SELECT seq FROM ` + deliveries + `
WHERE destination = ? AND group_name = ? AND state = 'pending' AND visible_after <= ` + now + `
  AND (lease_until IS NULL OR lease_until < ` + now + `)
ORDER BY seq LIMIT ? FOR UPDATE SKIP LOCKED`,
		ClaimMark: `UPDATE ` + deliveries + ` SET lease_owner = ?, lease_token = ?,
  lease_until = ` + now + ` + INTERVAL ? MICROSECOND, attempts = attempts + 1 WHERE seq = ?`,
		ClaimLoad: `SELECT d.seq, d.event_key, d.attempts, TIMESTAMPDIFF(MICROSECOND, ` + now + `, d.lease_until),
  e.id, e.destination, e.event_type, e.version, e.content_type, e.payload, e.headers, e.published_at
FROM ` + deliveries + ` d JOIN ` + events + ` e ON e.event_key = d.event_key
WHERE d.lease_token = ? ORDER BY d.seq`,

		LockEvent:         `SELECT event_key FROM ` + events + ` WHERE event_key = ? FOR UPDATE`,
		AckDelete:         `DELETE FROM ` + deliveries + ` WHERE seq = ? AND lease_token = ?`,
		DeleteOrphanEvent: `DELETE FROM ` + events + ` WHERE event_key = ? AND NOT EXISTS (SELECT 1 FROM ` + deliveries + ` d WHERE d.event_key = ?)`,

		Nack: `UPDATE ` + deliveries + ` SET visible_after = ` + now + ` + INTERVAL ? MICROSECOND,
  lease_owner = NULL, lease_token = NULL, lease_until = NULL, last_error = ? WHERE seq = ? AND lease_token = ?`,
		Dead: `UPDATE ` + deliveries + ` SET state = 'dead', lease_owner = NULL, lease_token = NULL, lease_until = NULL,
  last_error = ? WHERE seq = ? AND lease_token = ?`,

		ListDeadLetters: `SELECT d.seq, d.attempts, COALESCE(d.last_error, ''),
  e.id, e.destination, e.event_type, e.version, e.content_type, e.payload, e.headers, e.published_at
FROM ` + deliveries + ` d JOIN ` + events + ` e ON e.event_key = d.event_key
WHERE d.destination = ? AND d.group_name = ? AND d.state = 'dead' ORDER BY d.seq LIMIT ?`,
		Resubmit: `UPDATE ` + deliveries + ` SET state = 'pending', attempts = 0, visible_after = ` + now + `,
  lease_owner = NULL, lease_token = NULL, lease_until = NULL WHERE seq = ? AND state = 'dead'`,

		Release: `UPDATE ` + deliveries + ` SET lease_owner = NULL, lease_token = NULL, lease_until = NULL,
  attempts = GREATEST(attempts - 1, 0) WHERE seq = ? AND lease_token = ?`,
		AckKeep: `UPDATE ` + deliveries + ` SET state = 'delivered', delivered_at = ` + now + `,
  lease_owner = NULL, lease_token = NULL, lease_until = NULL WHERE seq = ? AND lease_token = ?`,

		ListStaleEphemeral: `SELECT destination, group_name FROM ` + groups + `
WHERE ephemeral = TRUE AND heartbeat_at < ` + now + ` - INTERVAL ? MICROSECOND`,
		DeleteStaleGroup: `DELETE FROM ` + groups + ` WHERE destination = ? AND group_name = ?
  AND ephemeral = TRUE AND heartbeat_at < ` + now + ` - INTERVAL ? MICROSECOND`,
		DeleteDelivered: `DELETE FROM ` + deliveries + ` WHERE state = 'delivered'
  AND delivered_at < ` + now + ` - INTERVAL ? MICROSECOND`,
		ListOrphanDeliveries: `SELECT d.seq FROM ` + deliveries + ` d
WHERE NOT EXISTS (SELECT 1 FROM ` + groups + ` g WHERE g.destination = d.destination AND g.group_name = d.group_name) LIMIT ?`,
		DeleteOrphanDelivery: `DELETE FROM ` + deliveries + ` WHERE seq = ?
  AND NOT EXISTS (SELECT 1 FROM ` + groups + ` g WHERE g.destination = ` + deliveries + `.destination AND g.group_name = ` + deliveries + `.group_name)`,
		ListOrphanEvents: `SELECT e.event_key FROM ` + events + ` e
WHERE NOT EXISTS (SELECT 1 FROM ` + deliveries + ` d WHERE d.event_key = e.event_key) LIMIT ?`,
		StaleGroups: `SELECT destination, group_name, COUNT(*) FROM ` + deliveries + `
WHERE state = 'pending' AND created_at < ` + now + ` - INTERVAL ? MICROSECOND
GROUP BY destination, group_name`,
	}
}

// now is the database time, in UTC with microseconds: every persisted time
// comes from the database clock.
const now = `UTC_TIMESTAMP(6)`

// tableOptions makes names compare byte for byte (utf8mb4_bin): destinations
// and groups are case-sensitive, as they are on PostgreSQL.
const tableOptions = ` ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`

func (dialect) DDL(t eventsql.Tables) []string {
	groups, events, deliveries := tableNames(t)

	return []string{
		`CREATE TABLE IF NOT EXISTS ` + groups + ` (
    destination  VARCHAR(255) NOT NULL,
    group_name   VARCHAR(255) NOT NULL,
    ephemeral    BOOLEAN      NOT NULL,
    heartbeat_at DATETIME(6)  NOT NULL,
    PRIMARY KEY (destination, group_name)
)` + tableOptions,
		`CREATE TABLE IF NOT EXISTS ` + events + ` (
    event_key    VARCHAR(32)  NOT NULL PRIMARY KEY,
    id           VARCHAR(255) NOT NULL,
    destination  VARCHAR(255) NOT NULL,
    event_type   VARCHAR(255) NOT NULL,
    version      INT          NOT NULL,
    content_type VARCHAR(255) NOT NULL,
    payload      LONGBLOB     NOT NULL,
    headers      TEXT         NOT NULL,
    published_at BIGINT       NOT NULL,
    created_at   DATETIME(6)  NOT NULL
)` + tableOptions,
		`CREATE TABLE IF NOT EXISTS ` + deliveries + ` (
    seq           BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
    event_key     VARCHAR(32)  NOT NULL,
    destination   VARCHAR(255) NOT NULL,
    group_name    VARCHAR(255) NOT NULL,
    state         VARCHAR(16)  NOT NULL,
    attempts      INT          NOT NULL,
    visible_after DATETIME(6)  NOT NULL,
    lease_owner   VARCHAR(64),
    lease_token   VARCHAR(32),
    lease_until   DATETIME(6),
    last_error    TEXT,
    created_at    DATETIME(6)  NOT NULL,
    delivered_at  DATETIME(6),
    UNIQUE KEY event_group (event_key, group_name),
    KEY claim (destination, group_name, state, visible_after, seq),
    KEY lease (lease_token)
)` + tableOptions,
	}
}

func (dialect) SchemaLockSQL() (lock, unlock string) {
	// GET_LOCK names are server-wide: the database keeps two schemas
	// with the same prefix apart
	return `SELECT GET_LOCK(LEFT(CONCAT(COALESCE(DATABASE(), ''), '.', ?), 64), 10)`,
		`SELECT RELEASE_LOCK(LEFT(CONCAT(COALESCE(DATABASE(), ''), '.', ?), 64))`
}

// tableNames returns the quoted names of the tables, shared by the DDL
// and the statements.
func tableNames(t eventsql.Tables) (groups, events, deliveries string) {
	return quote(t.Groups), quote(t.Events), quote(t.Deliveries)
}

// quote quotes a validated, possibly schema-qualified identifier:
// app.doger_events becomes `app`.`doger_events`.
func quote(ident string) string {
	parts := strings.Split(ident, ".")
	for i, p := range parts {
		parts[i] = "`" + p + "`"
	}
	return strings.Join(parts, ".")
}
