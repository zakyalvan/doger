package doger

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ErrCodecBroken is the error of the codec that cannot encode.
var ErrCodecBroken = errors.New("codec broken")

// fakeCodec is a JSON codec with another content type, or one whose
// Marshal fails with marshalErr.
type fakeCodec struct {
	contentType string
	marshalErr  error
}

func (c fakeCodec) ContentType() string { return c.contentType }

func (c fakeCodec) Marshal(v any) ([]byte, error) {
	if c.marshalErr != nil {
		return nil, c.marshalErr
	}
	return json.Marshal(v)
}

func (c fakeCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

var _ EventCodec = fakeCodec{}

func TestCodec_RoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		codec       EventCodec
		register    func(root *RootScope) error
		event       any
		contentType string
	}{
		{name: "json/value", register: func(r *RootScope) error { return RegisterEventType[OrderPlaced](r, "orders.placed") }, event: OrderPlaced{ID: "1"}, contentType: "application/json"},
		{name: "json/pointer", register: func(r *RootScope) error { return RegisterEventType[*OrderPlaced](r, "orders.placed") }, event: &OrderPlaced{ID: "1"}, contentType: "application/json"},
		{name: "custom", codec: fakeCodec{contentType: "text/x-test"}, register: func(r *RootScope) error { return RegisterEventType[OrderPlaced](r, "orders.placed") }, event: OrderPlaced{ID: "1"}, contentType: "text/x-test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := NewWithOpts(&InjectorOpts{EventCodec: tt.codec})
			require.NoError(t, tt.register(root))
			info := root.eventTypeByName("orders.placed")

			env, err := root.newEnvelope(context.Background(), info, tt.event)
			require.NoError(t, err)
			decoded, err := root.decodeEvent(info, env.Payload)

			require.NoError(t, err)
			assert.Equal(t, tt.event, decoded)
			assert.Equal(t, tt.contentType, env.ContentType)
		})
	}
}

func TestCodec_MarshalError(t *testing.T) {
	t.Parallel()

	transport := &recordingTransport{}
	root := NewWithOpts(&InjectorOpts{EventCodec: fakeCodec{contentType: "application/json", marshalErr: ErrCodecBroken}, EventTransport: transport})
	immediateOrders(t, root)

	err := Publish(context.Background(), root, OrderPlaced{ID: "1"})

	var externalizeErr *ExternalizeError
	require.ErrorAs(t, err, &externalizeErr)
	require.ErrorIs(t, err, ErrCodecBroken)
	assert.Empty(t, transport.sent())
}

// uuidV7Millis returns the Unix milliseconds of a UUIDv7 string, failing t
// when id is not one.
func uuidV7Millis(t *testing.T, id string) int64 {
	t.Helper()
	parts := strings.Split(id, "-")
	require.Len(t, parts, 5, id)
	require.Equal(t, []int{8, 4, 4, 4, 12}, []int{len(parts[0]), len(parts[1]), len(parts[2]), len(parts[3]), len(parts[4])}, id)
	raw, err := hex.DecodeString(strings.Join(parts, ""))
	require.NoError(t, err, id)
	require.Equal(t, byte(0x70), raw[6]&0xf0, "version 7: %s", id)
	require.Equal(t, byte(0x80), raw[8]&0xc0, "RFC 9562 variant: %s", id)

	var millis int64
	for _, b := range raw[:6] {
		millis = millis<<8 | int64(b)
	}

	return millis
}

func TestEnvelope_Fields(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		generator func() string
		check     func(t *testing.T, err error, sent []Envelope)
	}{
		{name: "default", check: func(t *testing.T, err error, sent []Envelope) {
			t.Helper()
			require.NoError(t, err)
			require.Len(t, sent, 1)
			env := sent[0]
			assert.Equal(t, at.UnixMilli(), uuidV7Millis(t, env.ID))
			assert.Equal(t, "orders.placed", env.Destination)
			assert.Equal(t, "orders.placed", env.EventType)
			assert.Equal(t, 2, env.Version)
			assert.Equal(t, "application/json", env.ContentType)
			assert.JSONEq(t, `{"ID":"1","Amount":0}`, string(env.Payload))
			assert.True(t, at.Equal(env.PublishedAt))
		}},
		{name: "generator", generator: func() string { return "id-7" }, check: func(t *testing.T, err error, sent []Envelope) {
			t.Helper()
			require.NoError(t, err)
			require.Len(t, sent, 1)
			assert.Equal(t, "id-7", sent[0].ID)
		}},
		{name: "empty_id", generator: func() string { return "" }, check: func(t *testing.T, err error, sent []Envelope) {
			t.Helper()
			var externalizeErr *ExternalizeError
			require.ErrorAs(t, err, &externalizeErr)
			require.ErrorIs(t, err, ErrInvalidEnvelope)
			assert.Empty(t, sent)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			transport := &recordingTransport{}
			root := NewWithOpts(&InjectorOpts{Clock: newFakeClockAt(at), EventTransport: transport, EventIDGenerator: tt.generator})
			require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed", EventVersion(2), EventExternalize(ExternalizeImmediate)))

			err := Publish(context.Background(), root, OrderPlaced{ID: "1"})

			tt.check(t, err, transport.sent())
		})
	}
}

func TestEnvelope_DefaultIDs(t *testing.T) {
	t.Parallel()

	clock := newFakeClockAt(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	transport := &recordingTransport{}
	root := NewWithOpts(&InjectorOpts{Clock: clock, EventTransport: transport})
	immediateOrders(t, root)

	batches := make([][]string, 0, 10)
	for range 10 {
		before := len(transport.sent())
		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() {
				for range 10 {
					assert.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
				}
			})
		}
		wg.Wait()
		sent := transport.sent()[before:]
		batch := make([]string, 0, len(sent))
		for _, env := range sent {
			batch = append(batch, env.ID)
		}
		batches = append(batches, batch)
		clock.Advance(time.Millisecond)
	}

	seen := map[string]bool{}
	for i, batch := range batches {
		for _, id := range batch {
			seen[id] = true
			if i > 0 {
				assert.Greater(t, id, slices.Max(batches[i-1]), "a later batch sorts after an earlier one")
			}
		}
	}
	assert.Len(t, seen, 1000)
}

// tracePropagator copies the context value ctxTestKey to the header
// "trace", and back.
type tracePropagator struct{}

func (tracePropagator) Inject(ctx context.Context) map[string]string {
	if trace, ok := ctx.Value(ctxTestKey).(string); ok {
		return map[string]string{"trace": trace}
	}
	return nil
}

func (tracePropagator) Extract(ctx context.Context, headers map[string]string) context.Context {
	if trace, ok := headers["trace"]; ok {
		return context.WithValue(ctx, ctxTestKey, trace)
	}
	return ctx
}

var _ EventPropagator = tracePropagator{}

// propagatedContext publishes OrderPlaced{ID: "1"} from root "a" to a
// remote listener on root "b", both with propagator, with a context
// holding "trace"="t-9", and returns the context the listener received.
func propagatedContext(t *testing.T, propagator EventPropagator) context.Context {
	t.Helper()
	transport := &loopbackTransport{}
	a := NewWithOpts(&InjectorOpts{EventTransport: transport, EventPropagator: propagator})
	b := NewWithOpts(&InjectorOpts{EventTransport: transport, EventPropagator: propagator})
	t.Cleanup(func() {
		a.Shutdown()
		b.Shutdown()
	})
	immediateOrders(t, a)
	require.NoError(t, RegisterEventType[OrderPlaced](b, "orders.placed"))
	var got []context.Context
	Listen(b, func(ctx context.Context, _ OrderPlaced) error {
		got = append(got, ctx)
		return nil
	}, ListenerID("mail"), ListenerDelivery(DeliverOnce))
	require.NoError(t, b.Start())

	require.NoError(t, Publish(context.WithValue(context.Background(), ctxTestKey, "t-9"), a, OrderPlaced{ID: "1"}))

	require.Len(t, got, 1)
	return got[0]
}

func TestPropagator_RoundTrip(t *testing.T) {
	t.Parallel()

	ctx := propagatedContext(t, tracePropagator{})

	assert.Equal(t, "t-9", ctx.Value(ctxTestKey))
	env, ok := EnvelopeFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, map[string]string{"trace": "t-9"}, env.Headers)
	assert.Equal(t, "orders.placed", env.EventType)
}

func TestPropagator_None(t *testing.T) {
	t.Parallel()

	ctx := propagatedContext(t, nil)

	assert.Nil(t, ctx.Value(ctxTestKey))
	env, ok := EnvelopeFromContext(ctx)
	require.True(t, ok)
	assert.Empty(t, env.Headers)
}
