package eventsql_test

import (
	"context"
	"database/sql"
	"log"
	"os"
	"time"

	"github.com/kartaladev/doger"
	eventsql "github.com/kartaladev/doger/events/sql"
	"github.com/kartaladev/doger/events/sql/postgres"
	"github.com/kartaladev/doger/events/sqltx"
)

// New takes the *sql.DB, opened with the driver of your choice, and the
// dialect of its engine. Every option has a default; the ones given here
// override them. The transport becomes the container's EventTransport.
func ExampleNew() {
	db, err := sql.Open("pgx", os.Getenv("DOGER_SQL_POSTGRES_DSN"))
	if err != nil {
		log.Print(err)
		return
	}
	defer db.Close()

	transport, err := eventsql.New(db, postgres.Dialect(),
		eventsql.TablePrefix("shop_"),               // default "doger_"
		eventsql.Workers(8),                         // default 4
		eventsql.PollInterval(500*time.Millisecond), // default 1s
	)
	if err != nil {
		log.Print(err)
		return
	}

	root := doger.NewWithOpts(&doger.InjectorOpts{EventTransport: transport})
	defer root.Shutdown()
}

// EnsureSchema creates the missing tables and indexes, for development and
// tests. In production, apply postgres.DDL (or mysql.DDL) with your
// migration tool instead, and check it at startup with CheckSchema.
func ExampleTransport_EnsureSchema() {
	db, err := sql.Open("pgx", os.Getenv("DOGER_SQL_POSTGRES_DSN"))
	if err != nil {
		log.Print(err)
		return
	}
	defer db.Close()
	transport, err := eventsql.New(db, postgres.Dialect())
	if err != nil {
		log.Print(err)
		return
	}

	if err := transport.EnsureSchema(context.Background()); err != nil {
		log.Print(err)
		return
	}
	if err := transport.CheckSchema(context.Background()); err != nil {
		log.Print(err) // matches eventsql.ErrSchemaMissing, naming the missing tables
	}
}

// SendTx writes the envelope in the transaction sqltx.InTx binds to the
// context: it is delivered once the transaction commits, never when it
// rolls back. A doger container calls it for you when it publishes inside
// sqltx.InTx (ExternalizeAuto picks the outbox); call it directly only
// when you build envelopes yourself.
func ExampleTransport_SendTx() {
	db, err := sql.Open("pgx", os.Getenv("DOGER_SQL_POSTGRES_DSN"))
	if err != nil {
		log.Print(err)
		return
	}
	defer db.Close()
	transport, err := eventsql.New(db, postgres.Dialect())
	if err != nil {
		log.Print(err)
		return
	}

	err = sqltx.InTx(context.Background(), db, func(ctx context.Context) error {
		tx, _ := sqltx.From(ctx)
		if _, err := tx.SQL().ExecContext(ctx, "UPDATE stock SET quantity = quantity - 1 WHERE sku = $1", "A-1"); err != nil {
			return err
		}
		return transport.SendTx(ctx, doger.Envelope{
			ID:          "0190b6c8-0000-7000-8000-000000000001",
			Destination: "orders.placed",
			EventType:   "orders.placed",
			ContentType: "application/json",
			Version:     1,
			Payload:     []byte(`{"id":"1"}`),
			PublishedAt: time.Now(),
		})
	})
	if err != nil {
		log.Print(err)
	}
}
