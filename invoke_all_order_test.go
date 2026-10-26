package doger

import (
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
