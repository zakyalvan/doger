package doger

import (
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Collection fixtures. Every handler embeds handlerBase, so it implements
// EventHandler and reports the name it was built with.
type (
	EventHandler interface {
		HandleEvent(event string)
		HandlerName() string
	}

	handlerBase struct{ name string }

	ConsoleHandler      struct{ handlerBase }
	AuditHandler        struct{ handlerBase }
	StaticHandler       struct{ handlerBase }
	NoopHandler         struct{ handlerBase }
	AHandler            struct{ handlerBase }
	BHandler            struct{ handlerBase }
	SiblingHandler      struct{ handlerBase }
	ChildConsoleHandler struct{ handlerBase }
	CompositeHandler    struct{ handlerBase }
	XHandler            struct{ handlerBase }
	ReqHandler          struct{ handlerBase }
)

func (h handlerBase) HandleEvent(string)  {}
func (h handlerBase) HandlerName() string { return h.name }

// provideHandler registers lazy name in scope, building a T named name.
func provideHandler[T any](scope Injector, name string, build func(name string) T, opts ...ProvideOption) {
	ProvideNamed(scope, name, func(Injector) (T, error) { return build(name), nil }, opts...)
}

func newConsole(name string) *ConsoleHandler { return &ConsoleHandler{handlerBase{name}} }
func newAudit(name string) *AuditHandler     { return &AuditHandler{handlerBase{name}} }

// handlerNames returns the names of handlers, in order.
func handlerNames[T EventHandler](handlers []T) []string {
	names := make([]string, 0, len(handlers))
	for _, h := range handlers {
		names = append(names, h.HandlerName())
	}
	return names
}

func TestInvokeAs_HookReceivesTypeName(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	before := &callRecorder{}
	after := &callRecorder{}
	root := NewWithOpts(&InjectorOpts{
		HookBeforeInvocation: []func(*Scope, string){func(_ *Scope, name string) { before.record(name) }},
		HookAfterInvocation:  []func(*Scope, string, error){func(_ *Scope, name string, _ error) { after.record(name) }},
	})
	provideHandler(root, "audit", newAudit)

	_, err := InvokeAs[EventHandler](root)
	require.NoError(t, err)

	typeName := NameOf[EventHandler]()
	is.Equal([]string{typeName}, before.list())
	is.Equal([]string{typeName}, after.list())
}

// membershipFixture registers "console", "audit" and "db" (whose provider
// counts its calls) in a new root scope.
func membershipFixture() (*RootScope, *atomic.Int32) {
	root := New()
	provideHandler(root, "console", newConsole)
	provideHandler(root, "audit", newAudit)
	dbCalls := &atomic.Int32{}
	ProvideNamed(root, "db", func(Injector) (*sql.DB, error) {
		dbCalls.Add(1)
		return &sql.DB{}, nil
	})
	return root, dbCalls
}

func TestInvokeAll_Membership(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		collect func(root *RootScope) ([]string, error)
		members []string
		dbCalls int32
	}{
		{
			name: "EventHandler",
			collect: func(root *RootScope) ([]string, error) {
				handlers, err := InvokeAll[EventHandler](root)
				return handlerNames(handlers), err
			},
			members: []string{"console", "audit"},
		},
		{
			name: "*AuditHandler",
			collect: func(root *RootScope) ([]string, error) {
				handlers, err := InvokeAll[*AuditHandler](root)
				return handlerNames(handlers), err
			},
			members: []string{"audit"},
		},
		{
			name: "*sql.DB",
			collect: func(root *RootScope) ([]string, error) {
				dbs, err := InvokeAll[*sql.DB](root)
				names := make([]string, 0, len(dbs))
				for range dbs {
					names = append(names, "db")
				}
				return names, err
			},
			members: []string{"db"},
			dbCalls: 1,
		},
		{
			name: "fmt.Stringer",
			collect: func(root *RootScope) ([]string, error) {
				stringers, err := InvokeAll[fmt.Stringer](root)
				return make([]string, len(stringers)), err
			},
			members: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root, dbCalls := membershipFixture()

			members, err := tt.collect(root)

			is.NoError(err)
			is.ElementsMatch(tt.members, members)
			is.Equal(tt.dbCalls, dbCalls.Load())
		})
	}
}

func TestInvokeAll_EagerMembers(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	ProvideNamedValue(root, "static", &StaticHandler{handlerBase{"static"}})
	provideHandler(root, "console", newConsole)

	handlers, err := InvokeAll[EventHandler](root)

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"static", "console"}, handlerNames(handlers))
}

// TestInvokeAll_NilInjector is not parallel: it replaces DefaultRootScope.
func TestInvokeAll_NilInjector(t *testing.T) {
	testWithTimeout(t, time.Second)

	previous := DefaultRootScope
	DefaultRootScope = New()
	t.Cleanup(func() { DefaultRootScope = previous })
	provideHandler(DefaultRootScope, "console", newConsole)

	handlers, err := InvokeAll[EventHandler](nil)

	require.NoError(t, err)
	assert.Equal(t, []string{"console"}, handlerNames(handlers))
}

func TestInvokeAll_TypedNilMember(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	ProvideNamed(root, "noop", func(Injector) (*NoopHandler, error) { return nil, nil })

	handlers, err := InvokeAll[EventHandler](root)

	require.NoError(t, err)
	require.Len(t, handlers, 1)
	noop, ok := handlers[0].(*NoopHandler)
	is.True(ok, "the element is a *NoopHandler")
	is.Nil(noop)
}
