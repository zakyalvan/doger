package sqltx_test

import (
	"context"
	"fmt"

	"github.com/kartaladev/doger/events/sqltx"
)

// InTx commits when the function returns nil and runs the OnCommit
// callbacks after the commit. Any layer that has the context finds the
// transaction with From and runs its statements on SQL().
func ExampleInTx() {
	db, rec := openFakeDB() // a *sql.DB opened with your driver
	defer db.Close()

	err := sqltx.InTx(context.Background(), db, func(ctx context.Context) error {
		tx, _ := sqltx.From(ctx)
		tx.OnCommit(func(context.Context) { fmt.Println("committed") })
		_, err := tx.SQL().ExecContext(ctx, "UPDATE stock SET quantity = quantity - 1")
		return err
	})

	fmt.Println(err, rec.Events())
	// Output:
	// committed
	// <nil> [begin commit]
}

// BindTx adopts a transaction another library began. End it through the
// returned Tx: committing the *sql.Tx directly skips the callbacks.
func ExampleBindTx() {
	db, _ := openFakeDB()
	defer db.Close()
	raw, _ := db.BeginTx(context.Background(), nil) // begun by another library

	ctx, tx := sqltx.BindTx(context.Background(), db, raw)
	tx.OnRollback(func(context.Context) { fmt.Println("rolled back") })
	_, active := sqltx.From(ctx)
	fmt.Println("active:", active)

	_ = tx.Rollback()
	_, active = sqltx.From(ctx)
	fmt.Println("active:", active)
	// Output:
	// active: true
	// rolled back
	// active: false
}
