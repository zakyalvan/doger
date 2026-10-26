package doger

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Unrelated is implemented by no collection fixture.
type Unrelated interface{ unrelated() }

type (
	structHandlers struct {
		Handlers []EventHandler `do:"*"`
	}
	structByName struct {
		ByName map[string]EventHandler `do:"*"`
	}
	structEmpty struct {
		Empty []Unrelated `do:"*"`
	}
	structUnexported struct {
		handlers []EventHandler `do:"*"`
	}
	structCustomTag struct {
		Handlers []EventHandler `inject:"*"`
	}
	structSingle struct {
		H EventHandler `do:"*"`
	}
	structIntKeys struct {
		M map[int]EventHandler `do:"*"`
	}
	structOthers struct {
		Others []EventHandler `do:"*"`
	}

	// handlerKey is a string-kinded type that is not string.
	handlerKey    string
	structKeyKind struct {
		K map[handlerKey]EventHandler `do:"*"`
	}
)

// collectionRoot registers "console" then "audit" in a new root scope.
func collectionRoot() *RootScope {
	root := New()
	provideHandler(root, "console", newConsole)
	provideHandler(root, "audit", newAudit)
	return root
}

func TestInvokeStruct_CollectionFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		check func(t *testing.T, root *RootScope)
	}{
		{name: "slice", check: func(t *testing.T, root *RootScope) {
			t.Helper()
			value, err := InvokeStruct[structHandlers](root)
			require.NoError(t, err)
			assert.Equal(t, []string{"console", "audit"}, handlerNames(value.Handlers))
		}},
		{name: "map", check: func(t *testing.T, root *RootScope) {
			t.Helper()
			value, err := InvokeStruct[structByName](root)
			require.NoError(t, err)
			require.Len(t, value.ByName, 2)
			assert.Equal(t, "console", value.ByName["console"].HandlerName())
			assert.Equal(t, "audit", value.ByName["audit"].HandlerName())
		}},
		{name: "empty", check: func(t *testing.T, root *RootScope) {
			t.Helper()
			value, err := InvokeStruct[structEmpty](root)
			require.NoError(t, err)
			assert.NotNil(t, value.Empty)
			assert.Empty(t, value.Empty)
		}},
		{name: "unexported", check: func(t *testing.T, root *RootScope) {
			t.Helper()
			value, err := InvokeStruct[structUnexported](root)
			require.NoError(t, err)
			assert.Equal(t, []string{"console", "audit"}, handlerNames(value.handlers))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			tt.check(t, collectionRoot())
		})
	}
}

func TestInvokeStruct_CollectionCustomTag(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := NewWithOpts(&InjectorOpts{StructTagKey: "inject"})
	provideHandler(root, "console", newConsole)

	value, err := InvokeStruct[structCustomTag](root)

	require.NoError(t, err)
	assert.Equal(t, []string{"console"}, handlerNames(value.Handlers))
}

func TestInvokeStruct_CollectionInvalidField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		invoke  func(root *RootScope) error
		message string
	}{
		{
			name:    "single value",
			invoke:  func(root *RootScope) error { _, err := InvokeStruct[structSingle](root); return err },
			message: fmt.Sprintf("DI: tag `*` requires a slice or map[string] field, got `%s.H`", NameOf[structSingle]()),
		},
		{
			name:    "map with int keys",
			invoke:  func(root *RootScope) error { _, err := InvokeStruct[structIntKeys](root); return err },
			message: fmt.Sprintf("DI: tag `*` requires a slice or map[string] field, got `%s.M`", NameOf[structIntKeys]()),
		},
		{
			name:    "map with string-kinded keys",
			invoke:  func(root *RootScope) error { _, err := InvokeStruct[structKeyKind](root); return err },
			message: fmt.Sprintf("DI: tag `*` requires a slice or map[string] field, got `%s.K`", NameOf[structKeyKind]()),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			provideHandler(root, "console", newConsole)

			assert.EqualError(t, tt.invoke(root), tt.message)
		})
	}
}

func TestInvokeStruct_CollectionMemberFailure(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	errBuild := errors.New("build failed")
	root := New()
	ProvideNamed(root, "console", func(Injector) (*ConsoleHandler, error) { return nil, errBuild })

	_, err := InvokeStruct[structHandlers](root)

	assert.ErrorIs(t, err, errBuild)
	assert.ErrorContains(t, err, "collection member `console`")
}

func TestInvokeStruct_CollectionExcludesSelf(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	var others []string
	ProvideNamed(root, "composite", func(i Injector) (*CompositeHandler, error) {
		value, err := InvokeStruct[structOthers](i)
		others = handlerNames(value.Others)
		return &CompositeHandler{handlerBase{"composite"}}, err
	})
	provideHandler(root, "console", newConsole)

	_, err := InvokeNamed[*CompositeHandler](root, "composite")

	require.NoError(t, err)
	assert.Equal(t, []string{"console"}, others)
}
