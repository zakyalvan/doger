package doger

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExplainNamedService_DefaultState(t *testing.T) {
	t.Parallel()

	dialerName := NameOf[*dfDialer]()
	invokeFromExt := func(t *testing.T, ext *Scope) {
		t.Helper()
		_, err := Invoke[*dfDialer](ext)
		require.NoError(t, err)
	}

	tests := []struct {
		name    string
		setup   func(t *testing.T, root *RootScope, ext *Scope)
		state   DefaultState
		text    string
		scope   string
		service string
	}{
		{name: "unresolved", setup: func(_ *testing.T, _ *RootScope, ext *Scope) {
			Provide(ext, dialerProvider("default-dialer", nil), AsDefault())
		}, state: DefaultStateUnresolved, text: "unresolved"},
		{name: "chosen", setup: func(t *testing.T, _ *RootScope, ext *Scope) {
			t.Helper()
			Provide(ext, dialerProvider("default-dialer", nil), AsDefault())
			invokeFromExt(t, ext)
		}, state: DefaultStateChosen, text: "chosen"},
		{name: "yielded", setup: func(t *testing.T, root *RootScope, ext *Scope) {
			t.Helper()
			Provide(ext, dialerProvider("default-dialer", nil), AsDefault())
			Provide(root, dialerProvider("app-dialer", nil))
			invokeFromExt(t, ext)
		}, state: DefaultStateYielded, text: "yielded", scope: DefaultRootScopeName, service: dialerName},
		{name: "not_default", setup: func(_ *testing.T, _ *RootScope, ext *Scope) {
			Provide(ext, dialerProvider("app-dialer", nil))
		}, state: DefaultStateNone, text: "not-default"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			ext := root.Scope("ext")
			tt.setup(t, root, ext)

			output, ok := ExplainNamedService(ext, dialerName)

			require.True(t, ok)
			assert.Equal(t, tt.state, output.DefaultState)
			assert.Equal(t, tt.text, output.DefaultState.String())
			assert.Equal(t, tt.scope, output.DefaultYieldedToScopeName)
			assert.Equal(t, tt.service, output.DefaultYieldedToService)
		})
	}
}

func TestExplainNamedService_DefaultStateJSON(t *testing.T) {
	t.Parallel()

	dialerName := NameOf[*dfDialer]()

	t.Run("yielded", func(t *testing.T) {
		t.Parallel()

		root := New()
		ext := root.Scope("ext")
		Provide(ext, dialerProvider("default-dialer", nil), AsDefault())
		Provide(root, dialerProvider("app-dialer", nil))
		_, err := Invoke[*dfDialer](ext)
		require.NoError(t, err)
		output, ok := ExplainNamedService(ext, dialerName)
		require.True(t, ok)

		encoded, err := json.Marshal(output)

		require.NoError(t, err)
		var fields map[string]any
		require.NoError(t, json.Unmarshal(encoded, &fields))
		assert.Equal(t, "yielded", fields["default_state"])
		assert.Equal(t, DefaultRootScopeName, fields["default_yielded_to_scope_name"])
		assert.Equal(t, dialerName, fields["default_yielded_to_service"])

		var decoded ExplainServiceOutput
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		assert.Equal(t, DefaultStateYielded, decoded.DefaultState)
	})

	t.Run("not_default_omits_fields", func(t *testing.T) {
		t.Parallel()

		root := New()
		Provide(root, dialerProvider("app-dialer", nil))
		output, ok := ExplainNamedService(root, dialerName)
		require.True(t, ok)

		encoded, err := json.Marshal(output)

		require.NoError(t, err)
		var fields map[string]any
		require.NoError(t, json.Unmarshal(encoded, &fields))
		assert.NotContains(t, fields, "default_state")
		assert.NotContains(t, fields, "default_yielded_to_scope_name")
		assert.NotContains(t, fields, "default_yielded_to_service")
	})

	t.Run("unknown_text", func(t *testing.T) {
		t.Parallel()

		var state DefaultState
		assert.EqualError(t, state.UnmarshalText([]byte("maybe")), `DI: unknown default state "maybe"`)
	})
}

func TestExplainInjector_DoesNotDecideDefault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		register func(i Injector)
	}{
		{name: "eager", register: func(i Injector) { ProvideValue(i, &dfPool{}, AsDefault()) }},
		{name: "lazy", register: func(i Injector) { Provide(i, poolProvider(&dfPool{}), AsDefault()) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			tt.register(root)

			_ = ExplainInjector(root)

			output, ok := ExplainNamedService(root, NameOf[*dfPool]())
			require.True(t, ok)
			assert.Equal(t, DefaultStateUnresolved, output.DefaultState, "explaining must not decide a default")
			assert.NotPanics(t, func() { Provide(root, poolProvider(&dfPool{})) }, "the default stays replaceable")
		})
	}
}
