package doger

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEventOptions(t *testing.T) {
	t.Parallel()

	// ran publishes OrderPlaced through root, whose listeners "a" and "b"
	// both fail, and returns the listeners that ran.
	ran := func(root *RootScope) []string {
		rec := &recorder{names: []string{}}
		Listen(root, recordAs[OrderPlaced](rec, "a", ErrOutOfStock))
		Listen(root, recordAs[OrderPlaced](rec, "b", ErrOutOfStock))
		_ = Publish(context.Background(), root, OrderPlaced{ID: "1"})
		return rec.names
	}

	tests := []struct {
		name     string
		opts     *InjectorOpts
		panicMsg string
		ran      []string
	}{
		{name: "EventErrorMode EventFailFast", opts: &InjectorOpts{EventErrorMode: EventFailFast}, ran: []string{"a"}},
		{name: "EventErrorMode EventContinue", opts: &InjectorOpts{EventErrorMode: EventContinue}, ran: []string{"a", "b"}},
		{name: "EventErrorMode 99", opts: &InjectorOpts{EventErrorMode: 99}, panicMsg: "doger: invalid EventErrorMode 99"},
		{name: "zero value", opts: &InjectorOpts{}, ran: []string{"a"}},
	}
	parallelism := []struct {
		name  string
		value uint
		limit int
	}{
		{name: "EventAsyncParallelism 0", value: 0, limit: runtime.GOMAXPROCS(0) * 4},
		{name: "EventAsyncParallelism 1", value: 1, limit: 1},
	}
	for _, tt := range parallelism {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := NewWithOpts(&InjectorOpts{EventAsyncParallelism: tt.value})
			blocking := listenBlocking(root)
			defer root.Shutdown()
			defer close(blocking.release)

			blocking.fill(t, root, tt.limit)
			listenerErrorMatching(context.DeadlineExceeded)(t, probe(root))
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.panicMsg != "" {
				assert.PanicsWithError(t, tt.panicMsg, func() { NewWithOpts(tt.opts) })
				return
			}
			assert.Equal(t, tt.ran, ran(NewWithOpts(tt.opts)))
		})
	}
}

func TestEventErrorMode_String(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "EventFailFast", EventFailFast.String())
	assert.Equal(t, "EventContinue", EventContinue.String())
	assert.Equal(t, "EventErrorMode(7)", EventErrorMode(7).String())
}
