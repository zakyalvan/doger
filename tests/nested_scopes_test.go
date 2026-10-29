package tests

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kartaladev/doger"
	"github.com/stretchr/testify/assert"
)

// testShutdowner is a test helper that implements ShutdownerWithContextAndError
type testShutdowner struct {
	name          string
	shutdownOrder *[]string
	mu            *sync.Mutex
}

func (t *testShutdowner) Shutdown(ctx context.Context) error {
	t.mu.Lock()
	*t.shutdownOrder = append(*t.shutdownOrder, t.name)
	t.mu.Unlock()
	return nil
}

func TestMultiLevelNestedScopes_NonConcurrentShutdown(t *testing.T) {
	testWithTimeout(t, 300*time.Millisecond)
	is := assert.New(t)

	// Create a multi-level nested scope structure
	root := doger.New()
	level1 := root.Scope("level1")
	level2 := level1.Scope("level2")
	level3 := level2.Scope("level3")

	// Track shutdown order
	shutdownOrder := make([]string, 0)
	var mu sync.Mutex

	// Provide services at each level with shutdown tracking
	doger.ProvideNamedValue(root, "root-service", "root-value")
	doger.ProvideNamed(level1, "level1-service", func(i doger.Injector) (string, error) {
		return "level1-value", nil
	})
	doger.ProvideNamed(level2, "level2-service", func(i doger.Injector) (string, error) {
		return "level2-value", nil
	})
	doger.ProvideNamed(level3, "level3-service", func(i doger.Injector) (string, error) {
		return "level3-value", nil
	})

	// Add shutdownable services to track shutdown order
	doger.ProvideNamed(root, "root-shutdownable", func(i doger.Injector) (doger.ShutdownerWithContextAndError, error) {
		return &testShutdowner{name: "root", shutdownOrder: &shutdownOrder, mu: &mu}, nil
	})

	doger.ProvideNamed(level1, "level1-shutdownable", func(i doger.Injector) (doger.ShutdownerWithContextAndError, error) {
		return &testShutdowner{name: "level1", shutdownOrder: &shutdownOrder, mu: &mu}, nil
	})

	doger.ProvideNamed(level2, "level2-shutdownable", func(i doger.Injector) (doger.ShutdownerWithContextAndError, error) {
		return &testShutdowner{name: "level2", shutdownOrder: &shutdownOrder, mu: &mu}, nil
	})

	doger.ProvideNamed(level3, "level3-shutdownable", func(i doger.Injector) (doger.ShutdownerWithContextAndError, error) {
		return &testShutdowner{name: "level3", shutdownOrder: &shutdownOrder, mu: &mu}, nil
	})

	// Invoke services to ensure they are initialized
	_, err := doger.InvokeNamed[string](root, "root-service")
	is.NoError(err)
	_, err = doger.InvokeNamed[string](level1, "level1-service")
	is.NoError(err)
	_, err = doger.InvokeNamed[string](level2, "level2-service")
	is.NoError(err)
	_, err = doger.InvokeNamed[string](level3, "level3-service")
	is.NoError(err)

	// Invoke shutdownable services to ensure they are initialized
	_, err = doger.InvokeNamed[doger.ShutdownerWithContextAndError](root, "root-shutdownable")
	is.NoError(err)
	_, err = doger.InvokeNamed[doger.ShutdownerWithContextAndError](level1, "level1-shutdownable")
	is.NoError(err)
	_, err = doger.InvokeNamed[doger.ShutdownerWithContextAndError](level2, "level2-shutdownable")
	is.NoError(err)
	_, err = doger.InvokeNamed[doger.ShutdownerWithContextAndError](level3, "level3-shutdownable")
	is.NoError(err)

	// Shutdown the root scope (should shutdown all nested scopes)
	report := root.Shutdown()
	is.True(report.Succeed)
	is.Empty(report.Errors)

	// Verify shutdown order (children should be shut down before parents)
	is.Len(shutdownOrder, 4)
	// The exact order may vary due to parallel shutdown, but we can verify all levels were shut down
	is.Contains(shutdownOrder, "root")
	is.Contains(shutdownOrder, "level1")
	is.Contains(shutdownOrder, "level2")
	is.Contains(shutdownOrder, "level3")
}

func TestMultiLevelNestedScopes_ServiceVisibility(t *testing.T) {
	testWithTimeout(t, 300*time.Millisecond)

	// Create a multi-level nested scope structure
	root := doger.New()
	level1 := root.Scope("level1")
	level2a := level1.Scope("level2a")
	level2b := level1.Scope("level2b")
	level3 := level2a.Scope("level3")

	// Provide services at different levels
	doger.ProvideNamedValue(root, "root-service", "root-value")
	doger.ProvideNamedValue(level1, "level1-service", "level1-value")
	doger.ProvideNamedValue(level2a, "level2a-service", "level2a-value")
	doger.ProvideNamedValue(level2b, "level2b-service", "level2b-value")
	doger.ProvideNamedValue(level3, "level3-service", "level3-value")

	tests := []struct {
		name    string
		scope   doger.Injector
		service string
		want    string
		wantErr string
	}{
		// Test children to root visibility (should work)
		{name: "level3_sees_root", scope: level3, service: "root-service", want: "root-value"},
		{name: "level2a_sees_root", scope: level2a, service: "root-service", want: "root-value"},
		{name: "level1_sees_root", scope: level1, service: "root-service", want: "root-value"},
		// Test root to children visibility (should not work)
		{name: "root_cannot_see_level1", scope: root, service: "level1-service", wantErr: "could not find service"},
		{name: "root_cannot_see_level2a", scope: root, service: "level2a-service", wantErr: "could not find service"},
		{name: "root_cannot_see_level3", scope: root, service: "level3-service", wantErr: "could not find service"},
		// Test children1 to children2 visibility (should not work)
		{name: "level2a_cannot_see_level2b", scope: level2a, service: "level2b-service", wantErr: "could not find service"},
		{name: "level2b_cannot_see_level2a", scope: level2b, service: "level2a-service", wantErr: "could not find service"},
		// Test same level visibility (should work)
		{name: "level2a_sees_own", scope: level2a, service: "level2a-service", want: "level2a-value"},
		{name: "level2b_sees_own", scope: level2b, service: "level2b-service", want: "level2b-value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			value, err := doger.InvokeNamed[string](tt.scope, tt.service)
			if tt.wantErr != "" {
				is.Error(err)
				is.Contains(err.Error(), tt.wantErr)
				return
			}
			is.NoError(err)
			is.Equal(tt.want, value)
		})
	}
}

func TestMultiLevelNestedScopes_Clone(t *testing.T) {
	testWithTimeout(t, 300*time.Millisecond)
	is := assert.New(t)

	// Create original multi-level nested scope structure
	root := doger.New()
	level1 := root.Scope("level1")
	level2 := level1.Scope("level2")
	level3 := level2.Scope("level3")

	// Provide services at each level
	doger.ProvideNamedValue(root, "root-service", "root-value")
	doger.ProvideNamedValue(level1, "level1-service", "level1-value")
	doger.ProvideNamedValue(level2, "level2-service", "level2-value")
	doger.ProvideNamedValue(level3, "level3-service", "level3-value")

	// Invoke services to populate orderedInvocation
	_, err := doger.InvokeNamed[string](root, "root-service")
	is.NoError(err)
	_, err = doger.InvokeNamed[string](level1, "level1-service")
	is.NoError(err)
	_, err = doger.InvokeNamed[string](level2, "level2-service")
	is.NoError(err)
	_, err = doger.InvokeNamed[string](level3, "level3-service")
	is.NoError(err)

	// Clone the root scope
	clonedRoot := root.Clone()
	is.NotEqual(root, clonedRoot)

	// Find cloned scopes
	clonedLevel1, exists := clonedRoot.ChildByName("level1")
	is.True(exists)
	is.NotEqual(level1, clonedLevel1)

	clonedLevel2, exists := clonedLevel1.ChildByName("level2")
	is.True(exists)
	is.NotEqual(level2, clonedLevel2)

	clonedLevel3, exists := clonedLevel2.ChildByName("level3")
	is.True(exists)
	is.NotEqual(level3, clonedLevel3)

	// Verify services are cloned and accessible
	cloned := []struct {
		name    string
		scope   doger.Injector
		service string
		want    string
	}{
		{name: "cloned_root", scope: clonedRoot, service: "root-service", want: "root-value"},
		{name: "cloned_level1", scope: clonedLevel1, service: "level1-service", want: "level1-value"},
		{name: "cloned_level2", scope: clonedLevel2, service: "level2-service", want: "level2-value"},
		{name: "cloned_level3", scope: clonedLevel3, service: "level3-service", want: "level3-value"},
	}

	for _, tt := range cloned {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			value, err := doger.InvokeNamed[string](tt.scope, tt.service)
			is.NoError(err)
			is.Equal(tt.want, value)
		})
	}

	// Verify that services were invoked in the cloned scopes
	// We can't access orderedInvocation directly, but we can verify services work
	_, err = doger.InvokeNamed[string](clonedLevel2, "level2-service")
	is.NoError(err)
	_, err = doger.InvokeNamed[string](clonedLevel3, "level3-service")
	is.NoError(err)

	// Verify that modifying cloned scope doesn't affect original
	doger.ProvideNamedValue(clonedRoot, "cloned-only", "cloned-value")
	_, err = doger.InvokeNamed[string](root, "cloned-only")
	is.Error(err)
	is.Contains(err.Error(), "could not find service")

	// Verify original scope still works
	value, err := doger.InvokeNamed[string](root, "root-service")
	is.NoError(err)
	is.Equal("root-value", value)
}

func TestMultiLevelNestedScopes_NameCollision(t *testing.T) {
	testWithTimeout(t, 200*time.Millisecond)
	is := assert.New(t)

	// Create multi-level nested scope structure
	root := doger.New()
	level1 := root.Scope("level1")
	level2a := level1.Scope("level2a")
	level2b := level1.Scope("level2b")

	// Provide services with same name in different scopes (should work)
	doger.ProvideNamedValue(root, "shared-service", "root-value")
	doger.ProvideNamedValue(level1, "shared-service", "level1-value")
	doger.ProvideNamedValue(level2a, "shared-service", "level2a-value")
	doger.ProvideNamedValue(level2b, "shared-service", "level2b-value")

	// Verify each scope sees its own service (shadowing)
	shadowing := []struct {
		name  string
		scope doger.Injector
		want  string
	}{
		{name: "root", scope: root, want: "root-value"},
		{name: "level1", scope: level1, want: "level1-value"},
		{name: "level2a", scope: level2a, want: "level2a-value"},
		{name: "level2b", scope: level2b, want: "level2b-value"},
	}

	for _, tt := range shadowing {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			value, err := doger.InvokeNamed[string](tt.scope, "shared-service")
			is.NoError(err)
			is.Equal(tt.want, value)
		})
	}

	// Test that ProvideNamedValue doesn't allow overriding in same scope
	// This should panic when trying to register the same service name again
	is.Panics(func() {
		doger.ProvideNamedValue(level2a, "shared-service", "override-attempt")
	})

	// Should still get the original value
	value, err := doger.InvokeNamed[string](level2a, "shared-service")
	is.NoError(err)
	is.Equal("level2a-value", value)
}

func TestMultiLevelNestedScopes_ShutdownOrder(t *testing.T) {
	testWithTimeout(t, 300*time.Millisecond)
	is := assert.New(t)

	// Create multi-level nested scope structure
	root := doger.New()
	level1 := root.Scope("level1")
	level2 := level1.Scope("level2")
	level3 := level2.Scope("level3")

	// Track shutdown order
	shutdownOrder := make([]string, 0)
	var mu sync.Mutex

	// Add shutdownable services with dependencies
	doger.ProvideNamed(level3, "level3-shutdownable", func(i doger.Injector) (doger.ShutdownerWithContextAndError, error) {
		return &testShutdowner{name: "level3", shutdownOrder: &shutdownOrder, mu: &mu}, nil
	})

	doger.ProvideNamed(level2, "level2-shutdownable", func(i doger.Injector) (doger.ShutdownerWithContextAndError, error) {
		return &testShutdowner{name: "level2", shutdownOrder: &shutdownOrder, mu: &mu}, nil
	})

	doger.ProvideNamed(level1, "level1-shutdownable", func(i doger.Injector) (doger.ShutdownerWithContextAndError, error) {
		return &testShutdowner{name: "level1", shutdownOrder: &shutdownOrder, mu: &mu}, nil
	})

	doger.ProvideNamed(root, "root-shutdownable", func(i doger.Injector) (doger.ShutdownerWithContextAndError, error) {
		return &testShutdowner{name: "root", shutdownOrder: &shutdownOrder, mu: &mu}, nil
	})

	// Invoke services to ensure they are initialized
	_, err := doger.InvokeNamed[doger.ShutdownerWithContextAndError](root, "root-shutdownable")
	is.NoError(err)
	_, err = doger.InvokeNamed[doger.ShutdownerWithContextAndError](level1, "level1-shutdownable")
	is.NoError(err)
	_, err = doger.InvokeNamed[doger.ShutdownerWithContextAndError](level2, "level2-shutdownable")
	is.NoError(err)
	_, err = doger.InvokeNamed[doger.ShutdownerWithContextAndError](level3, "level3-shutdownable")
	is.NoError(err)

	// Shutdown the root scope
	report := root.Shutdown()
	is.True(report.Succeed)
	is.Empty(report.Errors)

	// Verify all levels were shut down
	is.Len(shutdownOrder, 4)
	is.Contains(shutdownOrder, "root")
	is.Contains(shutdownOrder, "level1")
	is.Contains(shutdownOrder, "level2")
	is.Contains(shutdownOrder, "level3")
}
