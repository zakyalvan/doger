package eventsql

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

// maintenance is the table-wide periodic step of a transport: one loop
// per transport, running while at least one of its subscriptions runs.
type maintenance struct {
	mu      sync.Mutex
	members int
	stop    context.CancelFunc
	done    chan struct{} // closed once the loop returned
}

// closedChan is returned by leaveMaintenance to a member that was not the
// last: there is nothing to wait for.
var closedChan = func() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}()

// joinMaintenance counts a running subscription, starting the loop for
// the first.
func (t *Transport) joinMaintenance() {
	t.maint.mu.Lock()
	defer t.maint.mu.Unlock()
	t.maint.members++
	if t.maint.members > 1 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.maint.stop, t.maint.done = cancel, done
	go func() {
		defer close(done)
		t.maintain(ctx)
	}()
}

// leaveMaintenance uncounts a subscription whose goroutines returned,
// stopping the loop after the last; the channel is closed once the loop
// returned.
func (t *Transport) leaveMaintenance() <-chan struct{} {
	t.maint.mu.Lock()
	defer t.maint.mu.Unlock()
	t.maint.members--
	if t.maint.members > 0 {
		return closedChan
	}
	t.maint.stop()

	return t.maint.done
}

// maintenanceInterval is the period of the maintenance and of the
// heartbeats.
func (t *Transport) maintenanceInterval() time.Duration {
	return max(min(10*t.cfg.pollInterval, t.cfg.ephemeralTTL/3), minMaintenanceInterval)
}

// maintain runs the periodic step until ctx ends: reaping ephemeral groups
// without a heartbeat, and removing delivered rows, deliveries of groups
// that no longer exist and events no delivery refers to. Several
// transports may do it at once: every delete is idempotent.
func (t *Transport) maintain(ctx context.Context) {
	ticker := time.NewTicker(t.maintenanceInterval())
	defer ticker.Stop()
	var lastStaleCheck time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		t.maintenance(ctx, &lastStaleCheck)
	}
}

// maintenance runs one periodic step; lastStaleCheck is when the stale
// group check last ran.
func (t *Transport) maintenance(ctx context.Context, lastStaleCheck *time.Time) {
	logErr := func(what string, err error) {
		if err != nil && ctx.Err() == nil {
			t.cfg.logger.Error("doger: eventsql: "+what, "error", err)
		}
	}
	logErr("reaping ephemeral groups", t.reap(ctx))
	if t.cfg.keepDelivered > 0 {
		_, err := t.db.ExecContext(ctx, t.stmts.DeleteDelivered, t.cfg.keepDelivered.Microseconds())
		logErr("deleting delivered rows", err)
	}
	logErr("deleting deliveries of removed groups", t.deleteOrphanDeliveries(ctx))
	logErr("deleting orphan events", t.deleteOrphanEvents(ctx))
	if time.Since(*lastStaleCheck) >= staleCheckInterval {
		*lastStaleCheck = time.Now()
		logErr("checking stale groups", t.warnStaleGroups(ctx))
	}
}

// minMaintenanceInterval bounds the maintenance interval of a tiny
// PollInterval or EphemeralTTL.
const minMaintenanceInterval = time.Millisecond

// Stale groups: how old a pending delivery makes its group stale, and how
// often a transport looks for them.
const (
	staleAge           = 24 * time.Hour
	staleCheckInterval = time.Hour
)

// reap removes the ephemeral groups whose heartbeat is older than
// EphemeralTTL, with their deliveries.
func (t *Transport) reap(ctx context.Context) error {
	ttl := t.cfg.ephemeralTTL.Microseconds()
	stale, err := t.listStaleEphemeral(ctx, ttl)
	if err != nil {
		return err
	}
	for _, g := range stale {
		if err := t.reapGroup(ctx, g[0], g[1], ttl); err != nil {
			return err
		}
	}

	return nil
}

// listStaleEphemeral returns the destination and group of the ephemeral
// groups without a heartbeat for ttl microseconds.
func (t *Transport) listStaleEphemeral(ctx context.Context, ttl int64) ([][2]string, error) {
	scan := func(rows *sql.Rows) (g [2]string, err error) {
		err = rows.Scan(&g[0], &g[1])
		return g, err
	}

	return queryAll(ctx, t.db, scan, t.stmts.ListStaleEphemeral, ttl)
}

// reapGroup removes an ephemeral group still without a heartbeat, and its
// deliveries, in one transaction.
func (t *Transport) reapGroup(ctx context.Context, destination, group string, ttl int64) error {
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, t.stmts.DeleteStaleGroup, destination, group, ttl)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return err // a heartbeat came meanwhile
	}
	if _, err := tx.ExecContext(ctx, t.stmts.DeleteGroupDeliveries, destination, group); err != nil {
		return err
	}

	return tx.Commit()
}

// orphanBatch is how many orphan events, and orphan deliveries, one
// maintenance step deletes.
const orphanBatch = 100

// deleteOrphanDeliveries deletes the deliveries of groups that no longer
// exist: an outbox transaction that fanned out to a group and committed
// after the group was removed leaves them. Each is deleted by its key,
// rechecking the group, so the delete locks only that row.
func (t *Transport) deleteOrphanDeliveries(ctx context.Context) error {
	seqs, err := queryAll(ctx, t.db, scanOne[int64], t.stmts.ListOrphanDeliveries, orphanBatch)
	if err != nil {
		return err
	}
	for _, seq := range seqs {
		if _, err := t.db.ExecContext(ctx, t.stmts.DeleteOrphanDelivery, seq); err != nil {
			return err
		}
	}

	return nil
}

// deleteOrphanEvents deletes events no delivery refers to (left by
// KeepDelivered, reaped or dropped groups), each under its row lock as an
// acknowledgement does, so an event whose deliveries are being written is
// never deleted.
func (t *Transport) deleteOrphanEvents(ctx context.Context) error {
	keys, err := queryAll(ctx, t.db, scanOne[string], t.stmts.ListOrphanEvents, orphanBatch)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if err := t.deleteOrphanEvent(ctx, key); err != nil {
			return err
		}
	}

	return nil
}

// deleteOrphanEvent deletes event key when no delivery refers to it.
func (t *Transport) deleteOrphanEvent(ctx context.Context, key string) error {
	tx, err := t.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var locked string
	if err := tx.QueryRowContext(ctx, t.stmts.LockEvent, key).Scan(&locked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, t.stmts.DeleteOrphanEvent, key, key); err != nil {
		return err
	}

	return tx.Commit()
}

// warnStaleGroups logs a warning for every group whose oldest pending
// delivery is older than a day: usually a listener removed from the code,
// whose group should be dropped.
func (t *Transport) warnStaleGroups(ctx context.Context) error {
	rows, err := t.db.QueryContext(ctx, t.stmts.StaleGroups, staleAge.Microseconds())
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var destination, group string
		var n int64
		if err := rows.Scan(&destination, &group, &n); err != nil {
			return err
		}
		t.cfg.logger.Warn("doger: eventsql: a group has pending deliveries older than a day; drop it with DropGroup if its listener was removed",
			"destination", destination, "group", group, "stale_deliveries", n)
	}

	return rows.Err()
}
