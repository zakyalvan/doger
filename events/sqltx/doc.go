// Package sqltx binds database/sql transactions to a context so that doger
// can find them: [Tx] wraps a *sql.Tx with the commit and rollback
// callbacks a *sql.Tx lacks and implements [doger.Tx], and [Source] is the
// matching [doger.TxSource].
//
// Begin a transaction with [InTx] (recommended), [BeginTx], or adopt one
// another library began with [BindTx]. Code that only has the context finds
// the transaction with [From], and an adapter writing an outbox checks that
// it belongs to its own database with [FromDB].
//
// The SQL event transport (events/sql), the watermill wmsql module and the
// msgin adapter share this module, so an application has one context key
// and one TxSource for all of them.
package sqltx
