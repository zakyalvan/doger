package doger

import (
	"context"
	"fmt"
	"reflect"
	"slices"
)

// kindID identifies a kind. NewKind allocates a new one, so two kinds with
// the same name stay distinct.
type kindID struct {
	name string
}

// kindBinding opts a registration into a kind.
type kindBinding struct {
	id       *kindID
	meta     any
	target   reflect.Type // the kind's T
	deferred bool         // registered under an interface type: checked on the instance at Start
}

// Stage says when a kind handler runs during Start.
type Stage int

const (
	// StageBeforeStart runs after every candidate is built, before any Starter starts.
	StageBeforeStart Stage = iota
	// StageAfterStart runs after every Starter has started.
	StageAfterStart
)

// Kind is a typed component kind: services opted in with [Kind.With] are
// passed, at Start, to the handler registered with [Kind.Handle]. T is the
// interface (or type) every component must satisfy, and M is the metadata
// attached to each registration.
type Kind[T, M any] struct {
	id *kindID
}

// Component is a service opted into a kind, as passed to the kind's handler.
//
// Scope is the scope the service is registered in. For a service of the root
// scope it is the root's *Scope, not the *RootScope: a Shutdown reached
// through it bypasses the root lifecycle.
type Component[T, M any] struct {
	Scope    *Scope
	Name     string
	Instance T
	Meta     M
}

// kindEntry is a type-erased handler registration.
type kindEntry struct {
	id    *kindID
	stage Stage
	run   func(ctx context.Context, scope *Scope, name string, instance any, meta any) error
}

// NewKind creates a kind with target type T, metadata type M, and a name used
// in errors.
func NewKind[T, M any](name string) Kind[T, M] {
	return Kind[T, M]{id: &kindID{name: name}}
}

// Name returns the kind's name, or "" for a zero Kind.
func (k Kind[T, M]) Name() string {
	if k.id == nil {
		return ""
	}

	return k.id.name
}

// With returns a registration option that opts the service into the kind
// with the given metadata.
//
// Registration panics when the option is applied to a transient service,
// applied twice to one registration, or applied to a concrete type not
// assignable to T. A service registered under an interface type is checked
// on its instance at Start instead. With panics on a zero Kind.
func (k Kind[T, M]) With(meta M) ProvideOption {
	k.mustBeCreated()

	return func(o *serviceOptions) {
		o.kinds = append(o.kinds, kindBinding{id: k.id, meta: meta, target: reflect.TypeFor[T]()})
	}
}

// Handle registers the kind's handler on root (nil means [DefaultRootScope]).
// A later call for the same kind replaces both handler and stage; handlers
// of one stage run kind by kind, in the order kinds were first given a
// handler. Opted-in services of a kind without a handler are built at Start,
// and nothing else happens to them.
//
// At Start, the handler is called once per component, in dependency order,
// then scope depth, then registration order, with the Start-time context.
// The first error, or a recovered panic, stops Start. A handler call that
// failed during Refresh or Scope.Start is made again for that component by
// the next one that covers it. A handler may call
// [DeclareDependency]; edges declared during [StageBeforeStart] order the
// Starters of the same Start. Do not call Shutdown from a handler.
//
// Handle panics on a zero Kind, an unknown stage or a nil handler.
func (k Kind[T, M]) Handle(root *RootScope, stage Stage, handler func(context.Context, Component[T, M]) error) {
	k.mustBeCreated()
	if stage != StageBeforeStart && stage != StageAfterStart {
		panic(fmt.Errorf("DI: invalid stage %d for kind `%s`", stage, k.id.name))
	}
	if handler == nil {
		panic(fmt.Errorf("DI: nil handler for kind `%s`", k.id.name))
	}
	if root == nil {
		root = DefaultRootScope
	}

	entry := k.newEntry(stage, handler)

	root.kindsMu.Lock()
	defer root.kindsMu.Unlock()

	for i := range root.kinds {
		if root.kinds[i].id == k.id {
			root.kinds[i] = entry // keeps the slot: order of first Handle
			return
		}
	}
	if k.id == runnableKind.id {
		// the implicit default handler occupies the first slot: keep it
		root.kinds = slices.Insert(root.kinds, 0, entry)
		return
	}
	root.kinds = append(root.kinds, entry)
}

// newEntry type-erases handler into a kind entry for stage.
func (k Kind[T, M]) newEntry(stage Stage, handler func(context.Context, Component[T, M]) error) kindEntry {
	return kindEntry{
		id:    k.id,
		stage: stage,
		run: func(ctx context.Context, scope *Scope, name string, instance any, meta any) error {
			typed, ok := instance.(T)
			if !ok {
				return fmt.Errorf("DI: service `%s` does not implement `%s` required by kind `%s`", name, reflect.TypeFor[T]().String(), k.id.name)
			}
			typedMeta, _ := meta.(M)

			return handler(ctx, Component[T, M]{Scope: scope, Name: name, Instance: typed, Meta: typedMeta})
		},
	}
}

// target returns the kind's target type T.
func (k Kind[T, M]) target() reflect.Type {
	return reflect.TypeFor[T]()
}

func (k Kind[T, M]) mustBeCreated() {
	if k.id == nil {
		panic(fmt.Errorf("DI: kind not created with NewKind"))
	}
}
