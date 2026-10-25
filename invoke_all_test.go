package doger

import (
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

func newAudit(name string) *AuditHandler { return &AuditHandler{handlerBase{name}} }

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
