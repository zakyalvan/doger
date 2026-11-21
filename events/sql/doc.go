// Package eventsql is a doger event transport on PostgreSQL 13+ or MySQL
// 8.0+, through database/sql: the database an application already runs
// carries its events between processes, with a transactional outbox.
//
// [New] takes the *sql.DB (opened with any driver) and a [Dialect]
// (postgres.Dialect() or mysql.Dialect(), in the subpackages). The
// returned [*Transport] implements doger.EventTransport and
// doger.TxEventSender: Send stores an event and one delivery per group
// registered for its destination, Subscribe registers a group and claims
// its deliveries with a lease and FOR UPDATE SKIP LOCKED, and SendTx
// writes in the transaction sqltx binds to the context.
//
// The caller owns the schema: apply postgres.DDL or mysql.DDL with a
// migration tool, or call [Transport.EnsureSchema] in development and
// tests.
package eventsql
