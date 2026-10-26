package doger

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collectNames returns the names of the EventHandlers collected from scope.
func collectNames(t *testing.T, scope Injector) []string {
	t.Helper()

	handlers, err := InvokeAll[EventHandler](scope)
	require.NoError(t, err)

	return handlerNames(handlers)
}

func TestInvokeAll_DefaultOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rootRegs  []string
		childRegs []string
		order     []string
	}{
		{name: "root only", rootRegs: []string{"c", "a", "b"}, order: []string{"c", "a", "b"}},
		{name: "root then child", rootRegs: []string{"b", "a"}, childRegs: []string{"z", "y"}, order: []string{"b", "a", "z", "y"}},
		{name: "child only", childRegs: []string{"y", "z"}, order: []string{"y", "z"}},
		{name: "one each", rootRegs: []string{"a"}, childRegs: []string{"a2"}, order: []string{"a", "a2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			child := root.Scope("child")
			for _, name := range tt.rootRegs {
				provideHandler(root, name, newConsole)
			}
			for _, name := range tt.childRegs {
				provideHandler(child, name, newConsole)
			}

			assert.Equal(t, tt.order, collectNames(t, child))
		})
	}
}

func TestInvokeAll_DefaultOrderStable(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	root := New()
	expected := make([]string, 0, 50)
	for i := 1; i <= 50; i++ {
		name := fmt.Sprintf("h-%02d", i)
		provideHandler(root, name, newConsole)
		expected = append(expected, name)
	}

	for range 20 {
		assert.Equal(t, expected, collectNames(t, root))
	}
}

func TestInvokeAll_OverrideKeepsPosition(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	for _, name := range []string{"a", "b", "c"} {
		provideHandler(root, name, newConsole)
	}
	OverrideNamed(root, "b", func(Injector) (*ConsoleHandler, error) { return newConsole("b"), nil })

	assert.Equal(t, []string{"a", "b", "c"}, collectNames(t, root))
}

func TestInvokeAll_DefaultOrderClone(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	provideHandler(root, "a", newConsole)
	provideHandler(root, "b", newConsole)
	clone := root.Clone()
	provideHandler(clone, "c", newConsole)

	for range 20 {
		assert.Equal(t, []string{"a", "b", "c"}, collectNames(t, clone))
	}
}

// orderedHandler is an EventHandler implementing Ordered.
type orderedHandler struct {
	handlerBase
	order int
}

func (h *orderedHandler) Order() int { return h.order }

var _ Ordered = (*orderedHandler)(nil)

func TestInvokeAll_Ordered(t *testing.T) {
	t.Parallel()

	// nil means the member does not implement Ordered
	value := func(v int) *int { return &v }

	tests := []struct {
		name   string
		values []*int // for "a", "b", "c", "d"
		order  []string
	}{
		{name: "none ordered", values: []*int{nil, nil, nil, nil}, order: []string{"a", "b", "c", "d"}},
		{name: "3, 1, 2, 0", values: []*int{value(3), value(1), value(2), value(0)}, order: []string{"d", "b", "c", "a"}},
		{name: "1, -, -1, -", values: []*int{value(1), nil, value(-1), nil}, order: []string{"c", "b", "d", "a"}},
		{name: "all equal", values: []*int{value(5), value(5), value(5), value(5)}, order: []string{"a", "b", "c", "d"}},
		{name: "int extremes", values: []*int{value(-2147483648), value(2147483647), value(0), nil}, order: []string{"a", "c", "d", "b"}},
		{name: "zeros and missing", values: []*int{value(0), nil, value(0), nil}, order: []string{"a", "b", "c", "d"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			for i, name := range []string{"a", "b", "c", "d"} {
				if tt.values[i] == nil {
					provideHandler(root, name, newConsole)
					continue
				}
				order := *tt.values[i]
				provideHandler(root, name, func(n string) *orderedHandler {
					return &orderedHandler{handlerBase: handlerBase{n}, order: order}
				})
			}

			assert.Equal(t, tt.order, collectNames(t, root))
		})
	}
}

func TestInvokeAll_OrderedTransient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		call  int
		order []string
	}{
		{name: "call_1", call: 1, order: []string{"flip", "fixed"}},
		{name: "call_2", call: 2, order: []string{"fixed", "flip"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			provideHandler(root, "fixed", func(n string) *orderedHandler { return &orderedHandler{handlerBase: handlerBase{n}} })
			calls := 0
			ProvideNamedTransient(root, "flip", func(Injector) (*orderedHandler, error) {
				calls++
				order := -1 // call 1
				if calls == 2 {
					order = 1
				}
				return &orderedHandler{handlerBase: handlerBase{"flip"}, order: order}, nil
			})

			var names []string
			for range tt.call {
				names = collectNames(t, root)
			}

			assert.Equal(t, tt.order, names)
		})
	}
}

func TestInvokeAll_WithOrderPrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		aOpts []ProvideOption
		bOpts []ProvideOption
		order []string
	}{
		{name: "no options", order: []string{"b", "c", "a"}},
		{name: "a WithOrder(-1)", aOpts: []ProvideOption{WithOrder(-1)}, order: []string{"a", "b", "c"}},
		{name: "b WithOrder(20)", bOpts: []ProvideOption{WithOrder(20)}, order: []string{"c", "a", "b"}},
		{name: "a and b WithOrder(0)", aOpts: []ProvideOption{WithOrder(0)}, bOpts: []ProvideOption{WithOrder(0)}, order: []string{"a", "b", "c"}},
		{name: "a WithOrder(5), WithOrder(-5)", aOpts: []ProvideOption{WithOrder(5), WithOrder(-5)}, order: []string{"a", "b", "c"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			provideHandler(root, "a", func(n string) *orderedHandler { return &orderedHandler{handlerBase: handlerBase{n}, order: 10} }, tt.aOpts...)
			provideHandler(root, "b", newConsole, tt.bOpts...)
			provideHandler(root, "c", func(n string) *orderedHandler { return &orderedHandler{handlerBase: handlerBase{n}} })

			assert.Equal(t, tt.order, collectNames(t, root))
		})
	}
}

func TestInvokeAll_WithOrderHelpers(t *testing.T) {
	t.Parallel()

	second := func(Injector) (*AuditHandler, error) { return newAudit("second"), nil }
	value := newAudit("second")
	o := WithOrder(-1)

	tests := []struct {
		name     string
		register func(root *RootScope)
	}{
		{name: "Provide", register: func(root *RootScope) { Provide(root, second, o) }},
		{name: "ProvideNamed", register: func(root *RootScope) { ProvideNamed(root, "second", second, o) }},
		{name: "ProvideValue", register: func(root *RootScope) { ProvideValue(root, value, o) }},
		{name: "ProvideNamedValue", register: func(root *RootScope) { ProvideNamedValue(root, "second", value, o) }},
		{name: "ProvideTransient", register: func(root *RootScope) { ProvideTransient(root, second, o) }},
		{name: "ProvideNamedTransient", register: func(root *RootScope) { ProvideNamedTransient(root, "second", second, o) }},
		{name: "Override", register: func(root *RootScope) { Override(root, second, o) }},
		{name: "OverrideNamed", register: func(root *RootScope) { OverrideNamed(root, "second", second, o) }},
		{name: "OverrideValue", register: func(root *RootScope) { OverrideValue(root, value, o) }},
		{name: "OverrideNamedValue", register: func(root *RootScope) { OverrideNamedValue(root, "second", value, o) }},
		{name: "OverrideTransient", register: func(root *RootScope) { OverrideTransient(root, second, o) }},
		{name: "OverrideNamedTransient", register: func(root *RootScope) { OverrideNamedTransient(root, "second", second, o) }},
		{name: "Package(Lazy)", register: func(root *RootScope) { Package(Lazy(second, o))(root) }},
		{name: "Package(LazyNamed)", register: func(root *RootScope) { Package(LazyNamed("second", second, o))(root) }},
		{name: "Package(Eager)", register: func(root *RootScope) { Package(Eager(value, o))(root) }},
		{name: "Package(EagerNamed)", register: func(root *RootScope) { Package(EagerNamed("second", value, o))(root) }},
		{name: "Package(Transient)", register: func(root *RootScope) { Package(Transient(second, o))(root) }},
		{name: "Package(TransientNamed)", register: func(root *RootScope) { Package(TransientNamed("second", second, o))(root) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			provideHandler(root, "first", newConsole)
			tt.register(root)

			assert.Equal(t, []string{"second", "first"}, collectNames(t, root))
		})
	}
}

// panickingOrder is an EventHandler whose Order() panics.
type panickingOrder struct {
	handlerBase
	value any
}

func (h *panickingOrder) Order() int { panic(h.value) }

func TestInvokeAll_WithOrderSkipsOrderMethod(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	provideHandler(root, "a", func(n string) *panickingOrder {
		return &panickingOrder{handlerBase: handlerBase{n}, value: "must not be called"}
	}, WithOrder(1))

	handlers, err := InvokeAll[EventHandler](root)

	require.NoError(t, err)
	assert.Len(t, handlers, 1)
}

func TestWithOrder_NoEffectOnInvoke(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	provideHandler(root, "a", newConsole, WithOrder(-100))

	handler, err := InvokeNamed[*ConsoleHandler](root, "a")

	require.NoError(t, err)
	assert.Equal(t, "a", handler.HandlerName())
}

var errOrderBroken = errors.New("order broken")

func TestInvokeAll_OrderPanic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		bad   func(Injector) (EventHandler, error)
		match func(t *testing.T, err error)
	}{
		{
			name: "error ErrOrderBroken",
			bad: func(Injector) (EventHandler, error) {
				return &panickingOrder{handlerBase: handlerBase{"bad"}, value: errOrderBroken}, nil
			},
			match: func(t *testing.T, err error) { t.Helper(); assert.ErrorIs(t, err, errOrderBroken) },
		},
		{
			name: "string boom",
			bad: func(Injector) (EventHandler, error) {
				return &panickingOrder{handlerBase: handlerBase{"bad"}, value: "boom"}, nil
			},
			match: func(t *testing.T, err error) { t.Helper(); assert.ErrorContains(t, err, "boom") },
		},
		{
			name: "Order() on a nil *Handler",
			bad: func(Injector) (EventHandler, error) {
				return (*orderedHandler)(nil), nil
			},
			match: func(t *testing.T, err error) { t.Helper(); assert.ErrorContains(t, err, "nil pointer") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			provideHandler(root, "ok", func(n string) *orderedHandler { return &orderedHandler{handlerBase: handlerBase{n}} })
			ProvideNamed(root, "bad", tt.bad)

			var handlers []EventHandler
			var err error
			require.NotPanics(t, func() { handlers, err = InvokeAll[EventHandler](root) })

			assert.Nil(t, handlers)
			assert.ErrorContains(t, err, "bad")
			tt.match(t, err)
		})
	}
}

func TestMustInvokeAll_OrderPanic(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	provideHandler(root, "bad", func(n string) *panickingOrder {
		return &panickingOrder{handlerBase: handlerBase{n}, value: errOrderBroken}
	})

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		MustInvokeAll[EventHandler](root)
	}()

	err, ok := recovered.(error)
	require.True(t, ok, "the call panics with an error")
	assert.ErrorIs(t, err, errOrderBroken)
}

func TestInvokeAllMap_IgnoresOrdered(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	provideHandler(root, "bad", func(n string) *panickingOrder {
		return &panickingOrder{handlerBase: handlerBase{n}, value: errOrderBroken}
	})

	handlers, err := InvokeAllMap[EventHandler](root)

	require.NoError(t, err)
	assert.Contains(t, handlers, "bad")
}

func TestInvokeAll_OrderDoesNotAffectShutdown(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	rec := &callRecorder{}
	ProvideNamed(root, "a", func(Injector) (*shutdownHandler, error) {
		return &shutdownHandler{handlerBase: handlerBase{"a"}, rec: rec}, nil
	}, WithOrder(10))
	ProvideNamed(root, "b", func(Injector) (*shutdownHandler, error) {
		return &shutdownHandler{handlerBase: handlerBase{"b"}, rec: rec}, nil
	}, WithOrder(-10))
	var seen []string
	ProvideNamed(root, "bus", func(i Injector) (*shutdownBus, error) {
		handlers, err := InvokeAll[EventHandler](i)
		seen = handlerNames(handlers)
		return &shutdownBus{rec: rec}, err
	})

	_, err := InvokeNamed[*shutdownBus](root, "bus")
	require.NoError(t, err)
	root.Shutdown()

	events := rec.list()
	is.Less(indexOf(events, "shutdown bus"), indexOf(events, "shutdown a"))
	is.Less(indexOf(events, "shutdown bus"), indexOf(events, "shutdown b"))
	is.Equal([]string{"b", "a"}, seen)
}

func TestInvokeAll_OrderDoesNotAffectStart(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	rec := &callRecorder{}
	provideRecordingStarter(root, rec, "a", WithOrder(10))
	provideRecordingStarter(root, rec, "b", WithOrder(-10))

	require.NoError(t, root.Start())
	assert.Equal(t, []string{"a", "b"}, rec.list())
}
