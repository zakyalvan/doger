package sqltx_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/doger"
	"github.com/kartaladev/doger/events/sqltx"
)

// ErrOutOfStock is a business error of the InTx tests.
var ErrOutOfStock = errors.New("out of stock")

// logRecorder is a slog.Handler recording every message with its
// attributes, one line per record.
type logRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (r *logRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *logRecorder) WithAttrs([]slog.Attr) slog.Handler       { return r }
func (r *logRecorder) WithGroup(string) slog.Handler            { return r }

func (r *logRecorder) Handle(_ context.Context, rec slog.Record) error {
	var b strings.Builder
	b.WriteString(rec.Message)
	rec.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value.Any())
		return true
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, b.String())

	return nil
}

// Text returns every recorded line.
func (r *logRecorder) Text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "\n")
}

// recordDefaultLogger makes a recorder slog.Default() for the test. Tests
// calling it cannot run in parallel.
func recordDefaultLogger(t *testing.T) *logRecorder {
	t.Helper()
	rec := &logRecorder{}
	previous := slog.Default()
	slog.SetDefault(slog.New(rec))
	t.Cleanup(func() { slog.SetDefault(previous) })

	return rec
}

// ran records the callbacks that ran, in order.
type ran struct {
	mu    sync.Mutex
	names []string
}

func (r *ran) fn(name string) func(context.Context) {
	return func(context.Context) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.names = append(r.names, name)
	}
}

func (r *ran) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.names...)
}

func TestBeginTx(t *testing.T) {
	t.Parallel()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name       string
		ctx        context.Context
		nilDB      bool
		opts       *sql.TxOptions
		failBegin  error
		wantErr    error
		wantLevel  driver.IsolationLevel
		checkLevel bool
	}{
		{name: "background", ctx: context.Background()},
		{name: "serializable", ctx: context.Background(), opts: &sql.TxOptions{Isolation: sql.LevelSerializable}, wantLevel: driver.IsolationLevel(sql.LevelSerializable), checkLevel: true},
		{name: "cancelled_context", ctx: cancelled, wantErr: context.Canceled},
		{name: "nil_context", ctx: nil},
		{name: "nil_db", ctx: context.Background(), nilDB: true, wantErr: sqltx.ErrNilDB},
		{name: "begin_fails", ctx: context.Background(), failBegin: ErrBeginFailed, wantErr: ErrBeginFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db, rec := newFakeDB(t)
			rec.failBegin = tt.failBegin
			if tt.nilDB {
				db = nil
			}

			ctx, tx, err := sqltx.BeginTx(tt.ctx, db, tt.opts)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, tx)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, tx)
			t.Cleanup(func() { _ = tx.Rollback() })
			assert.Same(t, db, tx.DB())
			assert.NotNil(t, tx.SQL())
			got, ok := sqltx.From(ctx)
			assert.True(t, ok)
			assert.Same(t, tx, got)
			if tt.checkLevel {
				assert.Equal(t, []driver.IsolationLevel{tt.wantLevel}, rec.Isolations())
			}
		})
	}
}

func TestTx_Callbacks(t *testing.T) { // not parallel: replaces slog.Default()
	tests := []struct {
		name       string
		failCommit error
		panicC1    bool
		call       func(tx *sqltx.Tx) (first, second error)
		wantFirst  error
		wantSecond error
		wantRan    []string
		wantLog    string
	}{
		{
			name:    "commit",
			call:    func(tx *sqltx.Tx) (error, error) { return tx.Commit(), nil },
			wantRan: []string{"c1", "c2"},
		},
		{
			name:    "rollback",
			call:    func(tx *sqltx.Tx) (error, error) { return tx.Rollback(), nil },
			wantRan: []string{"r1"},
		},
		{
			name:       "commit_fails",
			failCommit: ErrCommitFailed,
			call:       func(tx *sqltx.Tx) (error, error) { return tx.Commit(), nil },
			wantFirst:  ErrCommitFailed,
			wantRan:    []string{"r1"},
		},
		{
			name:       "commit_twice",
			call:       func(tx *sqltx.Tx) (error, error) { return tx.Commit(), tx.Commit() },
			wantSecond: sql.ErrTxDone,
			wantRan:    []string{"c1", "c2"},
		},
		{
			name:       "commit_then_rollback",
			call:       func(tx *sqltx.Tx) (error, error) { return tx.Commit(), tx.Rollback() },
			wantSecond: sql.ErrTxDone,
			wantRan:    []string{"c1", "c2"},
		},
		{
			name:    "panicking_callback",
			panicC1: true,
			call:    func(tx *sqltx.Tx) (error, error) { return tx.Commit(), nil },
			wantRan: []string{"c1", "c2"},
			wantLog: "kaboom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := recordDefaultLogger(t)
			db, rec := newFakeDB(t)
			rec.failCommit = tt.failCommit
			_, tx, err := sqltx.BeginTx(context.Background(), db, nil)
			require.NoError(t, err)

			r := &ran{}
			c1 := r.fn("c1")
			if tt.panicC1 {
				c1 = func(ctx context.Context) {
					r.fn("c1")(ctx)
					panic("kaboom")
				}
			}
			tx.OnCommit(c1)
			tx.OnCommit(r.fn("c2"))
			tx.OnRollback(r.fn("r1"))

			first, second := tt.call(tx)

			if tt.wantFirst != nil {
				require.ErrorIs(t, first, tt.wantFirst)
			} else {
				require.NoError(t, first)
			}
			if tt.wantSecond != nil {
				require.ErrorIs(t, second, tt.wantSecond)
			} else {
				require.NoError(t, second)
			}
			assert.Equal(t, tt.wantRan, r.Names())
			if tt.wantLog != "" {
				assert.Contains(t, logs.Text(), tt.wantLog)
			}
		})
	}
}

func TestTx_LateCallback(t *testing.T) { // not parallel: replaces slog.Default()
	logs := recordDefaultLogger(t)
	db, _ := newFakeDB(t)
	_, tx, err := sqltx.BeginTx(context.Background(), db, nil)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	r := &ran{}
	tx.OnCommit(r.fn("late"))

	assert.Empty(t, r.Names())
	assert.Contains(t, logs.Text(), "finished transaction")
}

func TestTx_ConcurrentCallbacks(t *testing.T) {
	t.Parallel()
	db, _ := newFakeDB(t)
	_, tx, err := sqltx.BeginTx(context.Background(), db, nil)
	require.NoError(t, err)

	const n = 50
	counts := make([]int, n)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			tx.OnCommit(func(context.Context) {
				mu.Lock()
				defer mu.Unlock()
				counts[i]++
			})
		})
	}
	wg.Wait()
	require.NoError(t, tx.Commit())

	for i, c := range counts {
		assert.Equal(t, 1, c, "callback %d", i)
	}
}

func TestInTx(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		fnErr        error
		fnPanic      any
		failCommit   error
		wantErr      error
		wantRecorded []string
		wantRan      []string
	}{
		{name: "returns_nil", wantRecorded: []string{"begin", "commit"}, wantRan: []string{"c"}},
		{name: "returns_error", fnErr: ErrOutOfStock, wantErr: ErrOutOfStock, wantRecorded: []string{"begin", "rollback"}, wantRan: []string{"r"}},
		{name: "panics", fnPanic: "kaboom", wantRecorded: []string{"begin", "rollback"}, wantRan: []string{"r"}},
		{name: "commit_fails", failCommit: ErrCommitFailed, wantErr: ErrCommitFailed, wantRecorded: []string{"begin", "commit"}, wantRan: []string{"r"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db, rec := newFakeDB(t)
			rec.failCommit = tt.failCommit
			r := &ran{}
			fn := func(ctx context.Context) error {
				tx, ok := sqltx.From(ctx)
				require.True(t, ok)
				tx.OnCommit(r.fn("c"))
				tx.OnRollback(r.fn("r"))
				if tt.fnPanic != nil {
					panic(tt.fnPanic)
				}
				return tt.fnErr
			}

			var err error
			if tt.fnPanic != nil {
				assert.PanicsWithValue(t, tt.fnPanic, func() { _ = sqltx.InTx(context.Background(), db, fn) })
			} else {
				err = sqltx.InTx(context.Background(), db, fn)
			}

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantRecorded, rec.Events())
			assert.Equal(t, tt.wantRan, r.Names())
		})
	}
}

func TestInTx_Arguments(t *testing.T) {
	t.Parallel()

	t.Run("nil_db", func(t *testing.T) {
		t.Parallel()
		called := false
		err := sqltx.InTx(context.Background(), nil, func(context.Context) error {
			called = true
			return nil
		})
		require.ErrorIs(t, err, sqltx.ErrNilDB)
		assert.False(t, called)
	})

	t.Run("nil_fn", func(t *testing.T) {
		t.Parallel()
		db, _ := newFakeDB(t)
		assert.PanicsWithError(t, "doger: sqltx: nil InTx function", func() {
			_ = sqltx.InTx(context.Background(), db, nil)
		})
	})
}

func TestBindTx(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		end          func(tx *sqltx.Tx, raw *sql.Tx) error
		wantRecorded []string
		wantRan      int
		wantActive   bool
	}{
		{name: "tx_commit", end: func(tx *sqltx.Tx, _ *sql.Tx) error { return tx.Commit() }, wantRecorded: []string{"begin", "commit"}, wantRan: 1},
		{name: "tx_rollback", end: func(tx *sqltx.Tx, _ *sql.Tx) error { return tx.Rollback() }, wantRecorded: []string{"begin", "rollback"}},
		{name: "raw_commit", end: func(_ *sqltx.Tx, raw *sql.Tx) error { return raw.Commit() }, wantRecorded: []string{"begin", "commit"}, wantActive: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db, rec := newFakeDB(t)
			raw, err := db.BeginTx(context.Background(), nil)
			require.NoError(t, err)
			ctx2, tx := sqltx.BindTx(context.Background(), db, raw)
			assert.Same(t, db, tx.DB())
			assert.Same(t, raw, tx.SQL())
			r := &ran{}
			tx.OnCommit(r.fn("c"))

			require.NoError(t, tt.end(tx, raw))

			assert.Equal(t, tt.wantRecorded, rec.Events())
			assert.Len(t, r.Names(), tt.wantRan)
			_, active := sqltx.From(ctx2)
			assert.Equal(t, tt.wantActive, active)
		})
	}
}

func TestBindTx_Panics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		nilDB   bool
		nilTx   bool
		message string
	}{
		{name: "nil_tx", nilTx: true, message: "doger: sqltx: nil *sql.Tx"},
		{name: "nil_db", nilDB: true, message: "doger: sqltx: nil *sql.DB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db, _ := newFakeDB(t)
			raw, err := db.BeginTx(context.Background(), nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = raw.Rollback() })
			argDB, argTx := db, raw
			if tt.nilDB {
				argDB = nil
			}
			if tt.nilTx {
				argTx = nil
			}

			assert.PanicsWithError(t, tt.message, func() { sqltx.BindTx(context.Background(), argDB, argTx) })
		})
	}
}

// the Tx is a doger.Tx
var _ doger.Tx = (*sqltx.Tx)(nil)
