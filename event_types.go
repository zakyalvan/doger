package doger

import (
	"errors"
	"maps"
	"reflect"
	"slices"
)

// ErrEventTypeConflict is matched by the error of RegisterEventType when the
// name or an alias is bound to another type, or the type to another name.
var ErrEventTypeConflict = errors.New("doger: event type conflict")

// ErrInvalidEventType is matched by the error of RegisterEventType for an
// empty name, an interface type or an invalid option, and by the error of a
// send or subscription whose mapped destination is empty.
var ErrInvalidEventType = errors.New("doger: invalid event type")

// EventTypeOption configures an event type registered with
// [RegisterEventType].
type EventTypeOption func(*eventTypeOptions)

// eventTypeOptions are the options of one RegisterEventType call.
type eventTypeOptions struct {
	version        int
	destination    string
	destinationSet bool
	aliases        []string
	externalized   bool                // EventExternalized or EventExternalize
	strategy       ExternalizeStrategy // EventExternalize
	strategySet    bool                // EventExternalize was given
}

// EventVersion sets the version of the event type, carried by its
// envelopes. Default: 1. A value below 1 makes RegisterEventType fail with
// an error matching [ErrInvalidEventType]. Given more than once, the last
// one wins.
func EventVersion(n int) EventTypeOption {
	return func(o *eventTypeOptions) {
		o.version = n
	}
}

// EventDestination sets where the events of the type are sent and where its
// remote listeners subscribe. Default: the type's name. An empty value makes
// RegisterEventType fail with an error matching [ErrInvalidEventType].
func EventDestination(destination string) EventTypeOption {
	return func(o *eventTypeOptions) {
		o.destination = destination
		o.destinationSet = true
	}
}

// EventAlias adds names that decode to the type on receive, such as the
// names it was sent under before a rename. An empty name makes
// RegisterEventType fail with an error matching [ErrInvalidEventType], and
// a name bound to another type with one matching [ErrEventTypeConflict].
func EventAlias(names ...string) EventTypeOption {
	return func(o *eventTypeOptions) {
		o.aliases = append(o.aliases, names...)
	}
}

// eventTypeInfo is what the registry knows of one event type.
type eventTypeInfo struct {
	typ         reflect.Type
	name        string
	version     int
	destination string
	aliases     []string

	externalized bool                // the type leaves the process
	strategy     ExternalizeStrategy // its EventExternalize strategy
	strategySet  bool                // EventExternalize was given
}

// eventRegistry is an immutable snapshot of the event types of a container:
// a registration publishes a new one.
type eventRegistry struct {
	byName map[string]*eventTypeInfo // by name and alias
	byType map[reflect.Type]*eventTypeInfo
}

// RegisterEventType binds name, the wire name of the event type E, to E in
// the registry of root; a nil root returns [ErrNilInjector]. E must be a
// concrete type, a pointer type included, so a received payload can be
// decoded into it. Registering the same name for the same type again
// returns nil and applies the newer options.
//
// It returns an error matching [ErrInvalidEventType] for an empty name, an
// interface type or an invalid option, and one matching
// [ErrEventTypeConflict] when the name or an alias is bound to another type
// or E to another name; nothing is registered then. It is safe to call
// concurrently with Publish, inbound deliveries and other registrations,
// before or after Start.
func RegisterEventType[E any](root *RootScope, name string, opts ...EventTypeOption) error {
	if root == nil {
		return ErrNilInjector
	}

	return root.registerEventType(reflect.TypeFor[E](), name, opts)
}

// registerEventType registers typ under name with opts.
func (s *RootScope) registerEventType(typ reflect.Type, name string, opts []EventTypeOption) error {
	if typ.Kind() == reflect.Interface {
		return libErrorMatching(ErrInvalidEventType, "event type `%s` is an interface type: a concrete type is required", typ)
	}
	if name == "" {
		return libErrorMatching(ErrInvalidEventType, "event type `%s` needs a non-empty name", typ)
	}
	info, err := newEventTypeInfo(typ, name, opts)
	if err != nil {
		return err
	}

	s.eventTypesMu.Lock()
	defer s.eventTypesMu.Unlock()

	current := s.eventTypes.Load()
	if err := current.conflict(info); err != nil {
		return err
	}
	next := current.with(info)
	s.eventTypes.Store(next)

	return nil
}

// conflict returns an error matching ErrEventTypeConflict when info's type
// is registered under another name, or one of its names is bound to
// another type. A nil registry has no conflict.
func (r *eventRegistry) conflict(info *eventTypeInfo) error {
	if r == nil {
		return nil
	}
	if existing := r.byType[info.typ]; existing != nil && existing.name != info.name {
		return libErrorMatching(ErrEventTypeConflict, "event type `%s` is registered as %q", info.typ, existing.name)
	}
	for _, n := range append([]string{info.name}, info.aliases...) {
		if existing := r.byName[n]; existing != nil && existing.typ != info.typ {
			return libErrorMatching(ErrEventTypeConflict, "name %q is bound to event type `%s`", n, existing.typ)
		}
	}

	return nil
}

// with returns a copy of r registering info, in place of the previous
// registration of its type. A nil registry is empty.
func (r *eventRegistry) with(info *eventTypeInfo) *eventRegistry {
	next := &eventRegistry{byName: map[string]*eventTypeInfo{}, byType: map[reflect.Type]*eventTypeInfo{}}
	if r != nil {
		next.byName = maps.Clone(r.byName)
		next.byType = maps.Clone(r.byType)
		if old := r.byType[info.typ]; old != nil {
			for _, n := range old.aliases {
				delete(next.byName, n)
			}
		}
	}
	next.byType[info.typ] = info
	next.byName[info.name] = info
	for _, alias := range info.aliases {
		next.byName[alias] = info
	}

	return next
}

// newEventTypeInfo returns the registration of typ under name with opts.
func newEventTypeInfo(typ reflect.Type, name string, opts []EventTypeOption) (*eventTypeInfo, error) {
	options := eventTypeOptions{version: 1}
	applyOptions(&options, opts)
	if err := validateEventTypeOptions(name, &options); err != nil {
		return nil, err
	}
	destination := name
	if options.destinationSet {
		destination = options.destination
	}

	return &eventTypeInfo{
		typ: typ, name: name, version: options.version, destination: destination, aliases: options.aliases,
		externalized: options.externalized, strategy: options.strategy, strategySet: options.strategySet,
	}, nil
}

// validateEventTypeOptions returns an error matching ErrInvalidEventType
// for options the event type name cannot have.
func validateEventTypeOptions(name string, options *eventTypeOptions) error {
	if options.version < 1 {
		return libErrorMatching(ErrInvalidEventType, "invalid version %d of event type %q", options.version, name)
	}
	if options.destinationSet && options.destination == "" {
		return libErrorMatching(ErrInvalidEventType, "empty destination for event type %q", name)
	}
	if !validStrategy(options.strategy) {
		return libErrorMatching(ErrInvalidEventType, "invalid ExternalizeStrategy %d for event type %q", int(options.strategy), name)
	}
	if slices.Contains(options.aliases, "") {
		return libErrorMatching(ErrInvalidEventType, "empty alias for event type %q", name)
	}

	return nil
}

// eventTypeByName returns the event type registered under name, as its name
// or an alias, or nil.
func (s *RootScope) eventTypeByName(name string) *eventTypeInfo {
	registry := s.eventTypes.Load()
	if registry == nil {
		return nil
	}

	return registry.byName[name]
}

// eventTypeOf returns the registration of typ, or nil.
func (s *RootScope) eventTypeOf(typ reflect.Type) *eventTypeInfo {
	registry := s.eventTypes.Load()
	if registry == nil {
		return nil
	}

	return registry.byType[typ]
}
