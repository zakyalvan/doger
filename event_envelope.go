package doger

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"time"
)

// EventCodec encodes the payload of externalized events and decodes the
// payload of received ones. It is called concurrently. The default,
// used when [InjectorOpts.EventCodec] is nil, is an encoding/json codec
// whose ContentType is "application/json".
type EventCodec interface {
	// ContentType names the encoding, carried by each Envelope; a received
	// envelope of another content type is rejected as permanent.
	ContentType() string
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

// jsonCodec is the default EventCodec.
type jsonCodec struct{}

func (jsonCodec) ContentType() string                { return "application/json" }
func (jsonCodec) Marshal(v any) ([]byte, error)      { return json.Marshal(v) }
func (jsonCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// ErrInvalidEnvelope is matched by the error of an externalization whose
// envelope cannot be built, such as one with an empty ID.
var ErrInvalidEnvelope = errors.New("doger: invalid envelope")

// codec returns the codec of the root.
func (s *RootScope) codec() EventCodec {
	if codec := s.opts.EventCodec; codec != nil {
		return codec
	}

	return jsonCodec{}
}

// newEnvelope returns the envelope of event, of the externalized type
// info, published with ctx. A panic of the codec or the ID generator is
// returned as an error.
func (s *RootScope) newEnvelope(ctx context.Context, info *eventTypeInfo, event any) (Envelope, error) {
	return recoverListenerErrorValue(func() (Envelope, error) { return s.buildEnvelope(ctx, info, event) })
}

// buildEnvelope encodes event, of the externalized type info, into a new
// envelope.
func (s *RootScope) buildEnvelope(ctx context.Context, info *eventTypeInfo, event any) (Envelope, error) {
	codec := s.codec()
	payload, err := codec.Marshal(event)
	if err != nil {
		return Envelope{}, err
	}
	destination, err := s.mappedDestination(info)
	if err != nil {
		return Envelope{}, err
	}
	now := s.opts.Clock.Now()
	id := s.newEventID(now)
	if id == "" {
		return Envelope{}, libErrorMatching(ErrInvalidEnvelope, "empty envelope ID")
	}

	var headers map[string]string
	if propagator := s.opts.EventPropagator; propagator != nil {
		headers = maps.Clone(propagator.Inject(ctx))
	}

	return Envelope{
		ID:          id,
		Destination: destination,
		EventType:   info.name,
		ContentType: codec.ContentType(),
		Version:     info.version,
		Payload:     payload,
		Headers:     headers,
		PublishedAt: now,
	}, nil
}

// newEventID returns the ID of an event published at now: from
// InjectorOpts.EventIDGenerator when set, a UUIDv7 otherwise.
func (s *RootScope) newEventID(now time.Time) string {
	if generate := s.opts.EventIDGenerator; generate != nil {
		return generate()
	}

	return newUUIDv7(now)
}

// newUUIDv7 returns a UUIDv7 (RFC 9562) for now: 48 bits of Unix
// milliseconds, the version and variant bits, and 74 random bits.
func newUUIDv7(now time.Time) string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	var millis [8]byte
	binary.BigEndian.PutUint64(millis[:], uint64(now.UnixMilli()))
	copy(raw[:6], millis[2:])
	raw[6] = raw[6]&0x0f | 0x70
	raw[8] = raw[8]&0x3f | 0x80

	return formatUUID(raw)
}

// decodeEvent decodes payload into a new value of the type of info: a
// pointer when the type is a pointer type.
func (s *RootScope) decodeEvent(info *eventTypeInfo, payload []byte) (any, error) {
	target := reflect.New(info.typ)
	if err := s.codec().Unmarshal(payload, target.Interface()); err != nil {
		return nil, err
	}

	return target.Elem().Interface(), nil
}
