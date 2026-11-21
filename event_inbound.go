package doger

import (
	"context"
	"errors"
	"maps"
	"reflect"
)

// EventPropagator carries request-scoped values, such as a trace context,
// across the transport in the envelope's headers. Inject runs on send with
// the publish context; Extract runs on receive and derives the context the
// remote listener gets. Both are called concurrently. Nil (default):
// no header is set and listener contexts carry no extracted value.
type EventPropagator interface {
	Inject(ctx context.Context) map[string]string
	Extract(ctx context.Context, headers map[string]string) context.Context
}

// ErrCodecMismatch is matched by the permanent error of a received
// envelope whose content type is not the codec's.
var ErrCodecMismatch = errors.New("doger: codec mismatch")

// permanentError marks an error a transport must not redeliver for.
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// PermanentError marks err as permanent: a remote listener returning it
// asks the transport to stop redelivering the event (dead-letter or drop)
// instead of retrying. The result unwraps to err, and [IsPermanentError]
// reports true for it and for any error wrapping it, a *ListenerError
// included. PermanentError(nil) returns nil.
func PermanentError(err error) error {
	if err == nil {
		return nil
	}

	return &permanentError{err: err}
}

// IsPermanentError reports whether err, or an error it wraps, was marked
// with [PermanentError]. Transports call it on the error of an
// EnvelopeHandler. Envelopes that cannot be decoded fail permanently.
func IsPermanentError(err error) bool {
	_, ok := errors.AsType[*permanentError](err)
	return ok
}

// envelopeKey is the context key of the envelope a remote listener
// receives.
type envelopeKey struct{}

// EnvelopeFromContext returns the envelope a remote listener is handling,
// from the context it receives, and false for any other context. Its ID
// lets a listener deduplicate redeliveries.
func EnvelopeFromContext(ctx context.Context) (Envelope, bool) {
	if ctx == nil {
		return Envelope{}, false
	}
	env, ok := ctx.Value(envelopeKey{}).(Envelope)

	return env, ok
}

// inboundHandler returns the handler of the envelopes delivered to remote:
// it decodes the envelope by its type name or alias, gives the listener a
// context holding the extracted headers and the envelope, applies its
// filter, resolves it, building a lazy service once, and calls it. A
// service listener is resolved per delivery by its name in its scope, so a
// registration replaced since Start (OverrideValue...) is the one called.
// Decoding
// failures are permanent; a listener failure is a *ListenerError keeping a
// permanent mark. An envelope of another registered type, sent to the same
// destination, is acknowledged without calling the listener.
func (s *RootScope) inboundHandler(remote *remoteListener) EnvelopeHandler {
	return func(ctx context.Context, env Envelope) error {
		event, ctx, err := s.decodeEnvelope(ctx, env)
		if err != nil {
			return err
		}
		eventType := reflect.TypeOf(event)
		if !eventType.AssignableTo(remote.typ) {
			return nil
		}

		err = s.deliverTo(ctx, remote.listener.current(), event, eventType, false, false)

		return wrapListenerError(remote.listener.id, eventType, err)
	}
}

// decodeEnvelope returns the event env carries, decoded into its
// registered type, and the context of its delivery: ctx with the headers
// extracted and env attached. Every failure is permanent.
func (s *RootScope) decodeEnvelope(ctx context.Context, env Envelope) (any, context.Context, error) {
	info := s.eventTypeByName(env.EventType)
	if info == nil {
		return nil, ctx, PermanentError(libErrorMatching(ErrEventTypeNotFound, "received event type %q is not registered", env.EventType))
	}
	codec := s.codec()
	if env.ContentType != codec.ContentType() {
		return nil, ctx, PermanentError(libErrorMatching(ErrCodecMismatch, "received content type %q, the codec's is %q", env.ContentType, codec.ContentType()))
	}
	event, err := recoverListenerErrorValue(func() (any, error) { return s.decodeEvent(info, env.Payload) })
	if err != nil {
		return nil, ctx, PermanentError(libErrorf("decoding event type %q: %w", env.EventType, err))
	}
	if propagator := s.opts.EventPropagator; propagator != nil {
		ctx, err = recoverListenerErrorValue(func() (context.Context, error) {
			return propagator.Extract(ctx, maps.Clone(env.Headers)), nil
		})
		if err != nil {
			return nil, ctx, PermanentError(libErrorf("extracting headers: %w", err))
		}
	}

	return event, context.WithValue(ctx, envelopeKey{}, env), nil
}

// wrapListenerError returns err as the *ListenerError of the listener id
// on an event of eventType, or nil.
func wrapListenerError(id string, eventType reflect.Type, err error) error {
	if err == nil {
		return nil
	}

	return listenerError(id, eventType, err)
}
