package doger

import (
	"database/sql"
	"errors"
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

	// Bus is not an EventHandler. Its provider records what it collected.
	Bus struct{ names []string }
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

// typedHandlerNames returns "<type> <name>" for each handler.
func typedHandlerNames(handlers []EventHandler) []string {
	names := make([]string, 0, len(handlers))
	for _, h := range handlers {
		names = append(names, fmt.Sprintf("%T %s", h, h.HandlerName()))
	}
	return names
}

func TestInvokeAll_Visibility(t *testing.T) {
	t.Parallel()

	aHandler := func(scope Injector, name string) {
		provideHandler(scope, name, func(n string) *AHandler { return &AHandler{handlerBase{n}} })
	}
	bHandler := func(scope Injector, name string) {
		provideHandler(scope, name, func(n string) *BHandler { return &BHandler{handlerBase{n}} })
	}
	sqlDB := func(scope Injector, name string) {
		ProvideNamed(scope, name, func(Injector) (*sql.DB, error) { return &sql.DB{}, nil })
	}

	tests := []struct {
		name      string
		rootName  string
		rootReg   func(Injector, string)
		childName string
		childReg  func(Injector, string)
		from      string
		members   []string
	}{
		{name: "child sees root and child", rootName: "a", rootReg: aHandler, childName: "b", childReg: bHandler, from: "child", members: []string{"*doger.AHandler a", "*doger.BHandler b"}},
		{name: "root sees root only", rootName: "a", rootReg: aHandler, childName: "b", childReg: bHandler, from: "root", members: []string{"*doger.AHandler a"}},
		{name: "child shadows root", rootName: "x", rootReg: aHandler, childName: "x", childReg: bHandler, from: "child", members: []string{"*doger.BHandler x"}},
		{name: "non-matching child shadows root", rootName: "x", rootReg: aHandler, childName: "x", childReg: sqlDB, from: "child", members: []string{}},
		{name: "sibling sees root and sibling", rootName: "x", rootReg: aHandler, childName: "x", childReg: bHandler, from: "sibling", members: []string{"*doger.AHandler x", "*doger.SiblingHandler s"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			tt.rootReg(root, tt.rootName)
			child := root.Scope("child")
			tt.childReg(child, tt.childName)
			sibling := root.Scope("sibling")
			provideHandler(sibling, "s", func(n string) *SiblingHandler { return &SiblingHandler{handlerBase{n}} })
			from := map[string]Injector{"root": root, "child": child, "sibling": sibling}[tt.from]

			handlers, err := InvokeAll[EventHandler](from)

			require.NoError(t, err)
			assert.ElementsMatch(t, tt.members, typedHandlerNames(handlers))
		})
	}
}

func TestInvokeAll_AnyType(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	var aCalls, bCalls atomic.Int32
	ProvideNamed(root, "a", func(Injector) (int, error) { aCalls.Add(1); return 1, nil })
	ProvideNamed(root, "b", func(Injector) (string, error) { bCalls.Add(1); return "b", nil })
	require.NoError(t, AsNamed[int, any](root, "a", "alias-a"))

	members, err := InvokeAll[any](root)

	require.NoError(t, err)
	is.ElementsMatch([]any{1, "b"}, members)
	is.Equal(int32(1), aCalls.Load())
	is.Equal(int32(1), bCalls.Load())
}

func TestInvokeAll_ProviderScopeVisibility(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	provideHandler(root, "a", func(n string) *AHandler { return &AHandler{handlerBase{n}} })
	ProvideNamed(root, "bus", func(i Injector) (*Bus, error) {
		handlers, err := InvokeAll[EventHandler](i)
		return &Bus{names: handlerNames(handlers)}, err
	})
	child := root.Scope("child")
	provideHandler(child, "b", func(n string) *BHandler { return &BHandler{handlerBase{n}} })

	bus, err := InvokeNamed[*Bus](child, "bus")

	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, bus.names)
}

func TestInvokeAll_AliasExcluded(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	audit := newAudit("audit")
	Provide(root, func(Injector) (*AuditHandler, error) { return audit, nil })
	require.NoError(t, As[*AuditHandler, EventHandler](root))

	handlers, err := InvokeAll[EventHandler](root)

	require.NoError(t, err)
	require.Len(t, handlers, 1)
	assert.Same(t, audit, handlers[0])
}

// collectionLen calls one collection function for EventHandler and returns
// whether the result is nil, its length and the error.
type collectionCall struct {
	name string
	call func(Injector) (isNil bool, length int, err error)
}

var invokeAllCall = collectionCall{name: "InvokeAll", call: func(i Injector) (bool, int, error) {
	handlers, err := InvokeAll[EventHandler](i)
	return handlers == nil, len(handlers), err
}}

func TestInvokeAll_Empty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		function collectionCall
		register func(*RootScope)
	}{
		{name: "InvokeAll_no_services", function: invokeAllCall, register: func(*RootScope) {}},
		{name: "InvokeAll_only_db", function: invokeAllCall, register: func(root *RootScope) {
			ProvideNamed(root, "db", func(Injector) (*sql.DB, error) { return &sql.DB{}, nil })
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			tt.register(root)

			isNil, length, err := tt.function.call(root)

			is.NoError(err)
			is.False(isNil, "the result is non-nil")
			is.Zero(length)
		})
	}
}

var (
	errAuditDown  = errors.New("service down")
	errAuditPanic = errors.New("kaboom")
)

func TestInvokeAll_MemberFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		provider func(i Injector) (*AuditHandler, error)
		cause    error
	}{
		{name: "returns an error", provider: func(Injector) (*AuditHandler, error) { return nil, errAuditDown }, cause: errAuditDown},
		{name: "panics with an error", provider: func(Injector) (*AuditHandler, error) { panic(errAuditPanic) }, cause: errAuditPanic},
		{name: "invokes a missing service", provider: func(i Injector) (*AuditHandler, error) {
			_, err := InvokeNamed[int](i, "missing")
			return nil, err
		}, cause: ErrServiceNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			provideHandler(root, "console", newConsole)
			ProvideNamed(root, "audit", tt.provider)

			handlers, err := InvokeAll[EventHandler](root)

			is.Nil(handlers)
			is.ErrorContains(err, "collection member `audit`")
			is.ErrorIs(err, tt.cause)
		})
	}
}

// mustCollectionCall calls one Must collection function for EventHandler
// and returns the length of its result.
type mustCollectionCall struct {
	name string
	call func(Injector) int
}

var mustInvokeAllCall = mustCollectionCall{name: "MustInvokeAll", call: func(i Injector) int {
	return len(MustInvokeAll[EventHandler](i))
}}

func TestMustInvokeAll_Panics(t *testing.T) {
	t.Parallel()

	for _, function := range []mustCollectionCall{mustInvokeAllCall} {
		t.Run(function.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			ProvideNamed(root, "audit", func(Injector) (*AuditHandler, error) { return nil, errAuditDown })

			var recovered any
			func() {
				defer func() { recovered = recover() }()
				function.call(root)
			}()

			err, ok := recovered.(error)
			require.True(t, ok, "the call panics with an error")
			assert.ErrorIs(t, err, errAuditDown)
		})
	}
}

func TestMustInvokeAll_Success(t *testing.T) {
	t.Parallel()

	for _, function := range []mustCollectionCall{mustInvokeAllCall} {
		t.Run(function.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			provideHandler(root, "console", newConsole)
			provideHandler(root, "audit", newAudit)

			var length int
			assert.NotPanics(t, func() { length = function.call(root) })
			assert.Equal(t, 2, length)
		})
	}
}
