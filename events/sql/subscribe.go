package eventsql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kartaladev/doger"
)

// settleTimeout bounds the statements a subscription runs on its own
// behalf (acknowledgements, releases, cleanup) once the context they
// belong to may already have ended.
const settleTimeout = 10 * time.Second

// Subscribe registers sub.Group for sub.Destination, idempotently, and
// starts delivering its deliveries to h: a poller goroutine claims them
// with a lease and Workers goroutines run h. A group registered before a
// send's write receives it; a new group starts at the latest event. The
// returned stop function stops claiming, waits for the running handlers
// until its context ends (then cancels their contexts and waits for them
// to return for at most 100ms more, so a handler ignoring its context
// does not hold it), and unregisters an ephemeral group; a second call
// returns nil. ctx is the parent of the handlers' contexts.
func (t *Transport) Subscribe(ctx context.Context, sub doger.Subscription, h doger.EnvelopeHandler) (func(context.Context) error, error) {
	if sub.Destination == "" || sub.Group == "" || h == nil {
		return nil, fmt.Errorf("%w: eventsql: a subscription needs a destination, a group and a handler", doger.ErrSubscriptionNotSupported)
	}
	if _, err := t.db.ExecContext(ctx, t.stmts.UpsertGroup, sub.Destination, sub.Group, sub.Ephemeral); err != nil {
		return nil, fmt.Errorf("doger: eventsql: subscribe: registering group %q: %w", sub.Group, err)
	}

	s := &subscription{
		t:       t,
		sub:     sub,
		handler: h,
		owner:   newToken(),
		work:    make(chan claim),
		idle:    make(chan struct{}, t.cfg.workers),
		done:    make(chan struct{}),
	}
	s.pollCtx, s.stopPolling = context.WithCancel(ctx)
	s.handleCtx, s.cancelHandlers = context.WithCancel(ctx)
	s.start()

	return s.stop, nil
}

// subscription is one running Subscribe.
type subscription struct {
	t       *Transport
	sub     doger.Subscription
	handler doger.EnvelopeHandler
	owner   string // the lease owner of its claims

	pollCtx        context.Context // ends when stop begins
	stopPolling    context.CancelFunc
	handleCtx      context.Context // the parent of the handlers' contexts
	cancelHandlers context.CancelFunc

	work    chan claim    // poller to workers, closed by the poller
	idle    chan struct{} // a token per worker waiting for work, taken per handover
	done    chan struct{} // closed once every goroutine returned
	stopped atomic.Bool   // set by the first stop
}

// claim is a leased delivery and its envelope.
type claim struct {
	seq      int64
	eventKey string
	token    string
	attempt  int
	deadline time.Time // when the lease ends, minus a margin
	env      doger.Envelope
}

// start runs the poller, the heartbeat and the workers, and joins the
// transport's maintenance, closing done once all returned and the
// maintenance, if this was its last member, stopped.
func (s *subscription) start() {
	s.t.joinMaintenance()
	var wg sync.WaitGroup
	wg.Go(s.poll)
	wg.Go(s.heartbeat)
	for range s.t.cfg.workers {
		wg.Go(s.runWorker)
	}
	go func() {
		wg.Wait()
		<-s.t.leaveMaintenance()
		close(s.done)
	}()
}

// poll claims deliveries and hands them to the workers until stop. It
// claims only once a worker is free, so a busy subscription leaves the
// deliveries to the other members; a claim then holds up to BatchSize
// deliveries, each handed over when a worker is free.
func (s *subscription) poll() {
	defer close(s.work)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		if !s.awaitIdle() {
			return
		}
		claims, err := s.claimBatch(s.pollCtx, s.t.cfg.batchSize)
		if err != nil && s.pollCtx.Err() == nil {
			s.t.cfg.logger.Error("doger: eventsql: claiming deliveries", "destination", s.sub.Destination, "group", s.sub.Group, "error", err)
		}
		if !s.dispatch(claims) {
			return
		}
		if len(claims) == s.t.cfg.batchSize {
			if s.pollCtx.Err() != nil {
				return
			}
			continue
		}
		timer.Reset(s.t.cfg.pollInterval)
		select {
		case <-timer.C:
		case <-s.pollCtx.Done():
			return
		}
	}
}

// awaitIdle takes the token of a free worker, and reports false once the
// subscription stops.
func (s *subscription) awaitIdle() bool {
	select {
	case <-s.idle:
		return true
	case <-s.pollCtx.Done():
		return false
	}
}

// dispatch hands claims to the workers one at a time, the token poll took
// covering the first, and reports whether it handed them all. A claim
// that waited for a worker past its lease is skipped and released, its
// attempt uncounted, for the next claim of any member. On stop it
// releases the claims not handed.
func (s *subscription) dispatch(claims []claim) bool {
	var skipped []claim
	defer func() { s.release(skipped) }()
	haveToken := true
	for i, c := range claims {
		if !haveToken && !s.awaitIdle() {
			skipped = append(skipped, claims[i:]...)
			return false
		}
		haveToken = true
		if !time.Now().Before(c.deadline) {
			skipped = append(skipped, c)
			continue
		}
		select {
		case s.work <- c:
			haveToken = false
		case <-s.pollCtx.Done():
			skipped = append(skipped, claims[i:]...)
			return false
		}
	}
	if haveToken {
		s.idle <- struct{}{} // unused: the worker is still free
	}

	return true
}

// runWorker runs the handler on each claim the poller hands over.
func (s *subscription) runWorker() {
	for {
		s.idle <- struct{}{} // never blocks: one token per worker at most
		c, ok := <-s.work
		if !ok {
			return
		}
		s.handle(c)
	}
}

// handle runs the handler on c and settles the delivery.
func (s *subscription) handle(c claim) {
	herr := s.run(c)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.handleCtx), settleTimeout)
	defer cancel()
	switch err := s.settle(ctx, c, herr); {
	case errors.Is(err, ErrLeaseLost):
		s.t.cfg.logger.Warn("doger: eventsql: result after the lease was lost, ignored", "id", c.env.ID, "group", s.sub.Group, "error", err)
	case err != nil:
		s.t.cfg.logger.Error("doger: eventsql: settling a delivery", "id", c.env.ID, "group", s.sub.Group, "error", err)
	}
}

// settle records the outcome of c, whose handler returned herr, while c
// holds its lease: handled, it is acknowledged; interrupted by stop,
// requeued; failed permanently or for the last time, dead-lettered;
// failed otherwise, retried after the backoff.
func (s *subscription) settle(ctx context.Context, c claim, herr error) error {
	switch {
	case herr == nil:
		return s.ack(ctx, c)
	case s.handleCtx.Err() != nil:
		return s.requeue(ctx, c, herr)
	case doger.IsPermanentError(herr) || c.attempt >= s.t.cfg.maxAttempts:
		return s.update(ctx, s.t.db, s.t.stmts.Dead, errorText(herr), c.seq, c.token)
	}
	if delay, ok := s.delay(c.attempt); ok {
		return s.update(ctx, s.t.db, s.t.stmts.Nack, delay.Microseconds(), errorText(herr), c.seq, c.token)
	}

	return nil
}

// requeue makes c, whose handler stop interrupted, claimable again at
// once by any member. Its attempt stays counted, since the handler ran: a
// handler that always hangs until stopped is dead-lettered eventually.
func (s *subscription) requeue(ctx context.Context, c claim, herr error) error {
	return s.update(ctx, s.t.db, s.t.stmts.Nack, int64(0), errorText(fmt.Errorf("interrupted by stop: %w", herr)), c.seq, c.token)
}

// delay returns the backoff after attempt, and false when the policy
// panicked: the lease then expires and the delivery is retried.
func (s *subscription) delay(attempt int) (d time.Duration, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			s.t.cfg.logger.Error("doger: eventsql: the Backoff policy panicked", "panic", fmt.Sprint(r))
			ok = false
		}
	}()

	return s.t.cfg.backoff.Delay(attempt), true
}

// update runs a settling statement with q, whose last two arguments are
// the seq and the lease token, returning ErrLeaseLost when it affected no
// row.
func (s *subscription) update(ctx context.Context, q querier, stmt string, args ...any) error {
	res, err := q.ExecContext(ctx, stmt, args...)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrLeaseLost
	}

	return nil
}

// maxErrorBytes is the most of a handler error stored as last_error.
const maxErrorBytes = 2048

// errorText returns the text of err as every engine stores it: valid
// UTF-8 without NUL bytes (PostgreSQL rejects both in text), truncated to
// maxErrorBytes.
func errorText(err error) string {
	text := err.Error()
	if len(text) > maxErrorBytes {
		text = text[:maxErrorBytes]
	}

	return strings.ReplaceAll(strings.ToValidUTF8(text, ""), "\x00", "")
}

// run calls the handler, a panic becoming an error.
func (s *subscription) run(c claim) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("doger: eventsql: handler panicked: %v", r)
		}
	}()

	ctx, cancel := s.handlerContext(c)
	defer cancel()

	return s.handler(ctx, c.env)
}

// handlerContext returns the context of the handler of c: it carries the
// attempt and ends at the claim's deadline, or when stop gives up waiting.
func (s *subscription) handlerContext(c claim) (context.Context, context.CancelFunc) {
	return context.WithDeadline(context.WithValue(s.handleCtx, attemptKey{}, c.attempt), c.deadline)
}

// claimBatch leases up to n claimable deliveries of the group in one
// transaction and returns them with their envelopes.
func (s *subscription) claimBatch(ctx context.Context, n int) ([]claim, error) {
	tx, err := s.t.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	seqs, err := s.selectClaimable(ctx, tx, n)
	if err != nil {
		return nil, err
	}
	if len(seqs) == 0 {
		return nil, nil
	}

	token := newToken()
	for _, seq := range seqs {
		if _, err := tx.ExecContext(ctx, s.t.stmts.ClaimMark, s.owner, token, s.t.cfg.lease.Microseconds(), seq); err != nil {
			return nil, err
		}
	}
	claims, err := s.load(ctx, tx, token)
	if err != nil {
		return nil, err
	}
	if claims, err = s.deadLetterExhausted(ctx, tx, claims); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return claims, nil
}

// deadLetterExhausted dead-letters in tx the claims beyond MaxAttempts,
// whose last attempt ended without a result (its member crashed), and
// returns the others.
func (s *subscription) deadLetterExhausted(ctx context.Context, tx *sql.Tx, claims []claim) ([]claim, error) {
	kept := claims[:0]
	for _, c := range claims {
		if c.attempt <= s.t.cfg.maxAttempts {
			kept = append(kept, c)
			continue
		}
		reason := fmt.Errorf("doger: eventsql: attempts exhausted: %d attempts ended without a result, MaxAttempts is %d", c.attempt-1, s.t.cfg.maxAttempts)
		if err := s.update(ctx, tx, s.t.stmts.Dead, errorText(reason), c.seq, c.token); err != nil {
			return nil, err
		}
	}

	return kept, nil
}

// selectClaimable locks up to n claimable deliveries in tx and returns
// their seq.
func (s *subscription) selectClaimable(ctx context.Context, tx *sql.Tx, n int) ([]int64, error) {
	return queryAll(ctx, tx, scanOne[int64], s.t.stmts.ClaimSelect, s.sub.Destination, s.sub.Group, n)
}

// load returns the deliveries leased with token.
func (s *subscription) load(ctx context.Context, tx *sql.Tx, token string) ([]claim, error) {
	rows, err := tx.QueryContext(ctx, s.t.stmts.ClaimLoad, token)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var claims []claim
	for rows.Next() {
		var (
			c         = claim{token: token}
			remaining int64
			headers   string
			published int64
		)
		if err := rows.Scan(&c.seq, &c.eventKey, &c.attempt, &remaining, &c.env.ID, &c.env.Destination, &c.env.EventType,
			&c.env.Version, &c.env.ContentType, &c.env.Payload, &headers, &published); err != nil {
			return nil, err
		}
		if err := decodeEnvelope(&c.env, headers, published); err != nil {
			return nil, err
		}
		c.deadline = time.Now().Add(time.Duration(remaining)*time.Microsecond - leaseMargin(s.t.cfg.lease))
		claims = append(claims, c)
	}

	return claims, rows.Err()
}

// ack deletes the delivery of c while c still holds its lease, and its
// event once no delivery of it remains.
func (s *subscription) ack(ctx context.Context, c claim) error {
	if s.t.cfg.keepDelivered > 0 {
		return s.update(ctx, s.t.db, s.t.stmts.AckKeep, c.seq, c.token)
	}
	tx, err := s.t.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var key string
	if err := tx.QueryRowContext(ctx, s.t.stmts.LockEvent, c.eventKey).Scan(&key); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := s.update(ctx, tx, s.t.stmts.AckDelete, c.seq, c.token); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.t.stmts.DeleteOrphanEvent, c.eventKey, c.eventKey); err != nil {
		return err
	}

	return tx.Commit()
}

// release ends the leases of claims that never started, so other members
// claim them at once; a failure is logged and the lease expires instead.
func (s *subscription) release(claims []claim) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.pollCtx), settleTimeout)
	defer cancel()
	for _, c := range claims {
		if _, err := s.t.db.ExecContext(ctx, s.t.stmts.Release, c.seq, c.token); err != nil {
			s.t.cfg.logger.Error("doger: eventsql: releasing a delivery", "id", c.env.ID, "group", s.sub.Group, "error", err)
		}
	}
}

// heartbeat refreshes the registration of the group until stop, so an
// ephemeral group with a running member is never reaped.
func (s *subscription) heartbeat() {
	ticker := time.NewTicker(s.t.maintenanceInterval())
	defer ticker.Stop()
	for {
		select {
		case <-s.pollCtx.Done():
			return
		case <-ticker.C:
		}
		_, err := s.t.db.ExecContext(s.pollCtx, s.t.stmts.UpsertGroup, s.sub.Destination, s.sub.Group, s.sub.Ephemeral)
		if err != nil && s.pollCtx.Err() == nil {
			s.t.cfg.logger.Error("doger: eventsql: heartbeat", "destination", s.sub.Destination, "group", s.sub.Group, "error", err)
		}
	}
}

// stop stops the subscription; see Subscribe.
func (s *subscription) stop(ctx context.Context) error {
	if !s.stopped.CompareAndSwap(false, true) {
		return nil
	}
	s.stopPolling()
	var err error
	select {
	case <-s.done:
	case <-ctx.Done():
		err = ctx.Err()
		s.cancelHandlers()
		grace := time.NewTimer(stopGrace)
		select {
		case <-s.done:
		case <-grace.C: // a handler ignores its context: not waited for
		}
		grace.Stop()
	}
	s.cancelHandlers()
	if s.sub.Ephemeral {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
		defer cancel()
		if derr := s.removeGroup(cctx); derr != nil {
			s.t.cfg.logger.Error("doger: eventsql: removing an ephemeral group", "group", s.sub.Group, "error", derr)
		}
	}

	return err
}

// stopGrace is how long stop, once its context ended and the handlers'
// contexts are cancelled, waits for the handlers to return: a handler
// ignoring its context is not waited for.
const stopGrace = 100 * time.Millisecond

// removeGroup unregisters the group and deletes its deliveries in one
// transaction. A delivery an outbox transaction commits afterwards is
// deleted by the maintenance (see Transport.maintenance).
func (s *subscription) removeGroup(ctx context.Context) error {
	tx, err := s.t.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range []string{s.t.stmts.DeleteGroup, s.t.stmts.DeleteGroupDeliveries} {
		if _, err := tx.ExecContext(ctx, stmt, s.sub.Destination, s.sub.Group); err != nil {
			return err
		}
	}

	return tx.Commit()
}
