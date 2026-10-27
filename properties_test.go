package doger

import (
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnvPropertySource_Mapping is not parallel: it sets environment
// variables.
func TestEnvPropertySource_Mapping(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		key      string
		variable string
	}{
		{name: "default", prefix: "DOGER_", key: "redis.addr", variable: "DOGER_REDIS_ADDR"},
		{name: "two_segments", prefix: "DOGER_", key: "redis.cache.addr", variable: "DOGER_REDIS_CACHE_ADDR"},
		{name: "dash", prefix: "DOGER_", key: "redis.cache-addr", variable: "DOGER_REDIS_CACHE__ADDR"},
		{name: "custom_prefix", prefix: "MYAPP_", key: "redis.addr", variable: "MYAPP_REDIS_ADDR"},
		{name: "empty_prefix", prefix: "", key: "redis.addr", variable: "REDIS_ADDR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.variable, "v")
			source := EnvPropertySource(tt.prefix)

			value, ok := source.Lookup(tt.key)

			assert.True(t, ok)
			assert.Equal(t, "v", value)
		})
	}
}

// TestEnvPropertySource_Distinct is not parallel: it sets environment
// variables.
func TestEnvPropertySource_Distinct(t *testing.T) {
	t.Setenv("DOGER_REDIS_CACHE_ADDR", "dot")
	t.Setenv("DOGER_REDIS_CACHE__ADDR", "dash")
	source := EnvPropertySource("DOGER_")

	dot, dotOK := source.Lookup("redis.cache.addr")
	dash, dashOK := source.Lookup("redis.cache-addr")

	require.True(t, dotOK)
	require.True(t, dashOK)
	assert.Equal(t, "dot", dot)
	assert.Equal(t, "dash", dash)
}

// TestEnvPropertySource_Unset is not parallel: it changes environment
// variables.
func TestEnvPropertySource_Unset(t *testing.T) {
	t.Setenv("DOGER_REDIS_ADDR", "restored after the test")
	require.NoError(t, os.Unsetenv("DOGER_REDIS_ADDR"))

	value, ok := EnvPropertySource("DOGER_").Lookup("redis.addr")

	assert.False(t, ok)
	assert.Empty(t, value)
}

// mapSource is a PropertySource holding its values in a map.
type mapSource map[string]string

func (m mapSource) Lookup(key string) (string, bool) {
	value, ok := m[key]
	return value, ok
}

// recordingSource is a PropertySource that records the keys it receives
// and holds no value.
type recordingSource struct {
	mu   sync.Mutex
	keys []string
}

func (r *recordingSource) Lookup(key string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys = append(r.keys, key)
	return "", false
}

func (r *recordingSource) received() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.keys...)
}

// lookupExtension is an extension "redis" with instance key instance whose
// Extend reads key from its properties into value and ok.
func lookupExtension(instance, key string, value *string, ok *bool) *testExtension {
	return &testExtension{name: "redis", instance: instance, extend: func(x *ExtensionContext) error {
		*value, *ok = x.Properties().Lookup(key)
		return nil
	}}
}

func TestProperties_Prefixed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		instance string
		key      string
		value    string
		found    bool
	}{
		{name: "no_instance", instance: "", key: "addr", value: "a:1", found: true},
		{name: "instance", instance: "cache", key: "addr", value: "c:3", found: true},
		{name: "missing", instance: "", key: "port", value: "", found: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := NewWithOpts(&InjectorOpts{PropertySource: mapSource{"redis.addr": "a:1", "redis.cache.addr": "c:3"}})
			var value string
			var found bool

			require.NoError(t, Extend(root, lookupExtension(tt.instance, tt.key, &value, &found)))

			assert.Equal(t, tt.value, value)
			assert.Equal(t, tt.found, found)
		})
	}
}

func TestProperties_InvalidKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		key      string
		received []string
	}{
		{name: "upper", key: "Addr", received: []string{}},
		{name: "double_dot", key: "addr..port", received: []string{}},
		{name: "leading_dot", key: ".addr", received: []string{}},
		{name: "empty", key: "", received: []string{}},
		{name: "trailing_dash", key: "addr-", received: []string{}},
		{name: "leading_dash", key: "-addr", received: []string{}},
		{name: "double_dash", key: "pool--size", received: []string{}},
		{name: "dash", key: "pool-size", received: []string{"redis.pool-size"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := &recordingSource{}
			root := NewWithOpts(&InjectorOpts{PropertySource: source})
			var value string
			var found bool

			require.NoError(t, Extend(root, lookupExtension("", tt.key, &value, &found)))

			assert.False(t, found)
			assert.Empty(t, value)
			assert.Equal(t, tt.received, append([]string{}, source.received()...))
		})
	}
}

// appCache is an application service with a Lookup method, which is not a
// property source for the container.
type appCache struct{}

func (appCache) Lookup(string) (string, bool) { return "from-cache", true }

// TestPropertySource_Selection is not parallel: it sets environment
// variables.
func TestPropertySource_Selection(t *testing.T) {
	tests := []struct {
		name     string
		opts     PropertySource
		register func(root *RootScope)
		value    string
	}{
		{name: "env", register: func(*RootScope) {}, value: "from-env"},
		{
			name: "service",
			register: func(root *RootScope) {
				Provide(root, func(Injector) (PropertySource, error) { return mapSource{"redis.addr": "from-service"}, nil })
			},
			value: "from-service",
		},
		{
			name: "opts",
			opts: mapSource{"redis.addr": "from-opts"},
			register: func(root *RootScope) {
				Provide(root, func(Injector) (PropertySource, error) { return mapSource{"redis.addr": "from-service"}, nil })
			},
			value: "from-opts",
		},
		{
			name:     "unrelated_lookup_ignored",
			register: func(root *RootScope) { Provide(root, func(Injector) (*appCache, error) { return &appCache{}, nil }) },
			value:    "from-env",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DOGER_REDIS_ADDR", "from-env")
			root := NewWithOpts(&InjectorOpts{PropertySource: tt.opts})
			tt.register(root)
			var value string
			var found bool

			require.NoError(t, Extend(root, lookupExtension("", "addr", &value, &found)))

			assert.True(t, found)
			assert.Equal(t, tt.value, value)
		})
	}
}

// TestPropertySource_LateServiceIgnored is not parallel: it sets
// environment variables.
func TestPropertySource_LateServiceIgnored(t *testing.T) {
	t.Setenv("DOGER_REDIS_ADDR", "env-a")
	t.Setenv("DOGER_REDIS_B_ADDR", "env-b")
	root := New()
	var first, second string
	var found bool
	require.NoError(t, Extend(root, lookupExtension("", "addr", &first, &found)))
	Provide(root, func(Injector) (*mapSource, error) { return &mapSource{"redis.b.addr": "late"}, nil })

	require.NoError(t, Extend(root, lookupExtension("b", "addr", &second, &found)))

	assert.Equal(t, "env-a", first)
	assert.Equal(t, "env-b", second)
}

func TestPropertySource_LateSameNamePanics(t *testing.T) {
	t.Parallel()

	root := New()
	var value string
	var found bool
	require.NoError(t, Extend(root, lookupExtension("", "addr", &value, &found)))

	assert.PanicsWithError(t, "DI: service `github.com/kartaladev/doger.PropertySource` has already been declared", func() {
		Provide(root, func(Injector) (PropertySource, error) { return mapSource{}, nil })
	})
}

func TestPropertySource_ConcurrentFirstRead(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	root := New()
	var provided atomic.Int32
	Provide(root, func(Injector) (PropertySource, error) {
		provided.Add(1)
		return mapSource{"redis.addr": "a:1"}, nil
	})

	const readers = 50
	values := make([]string, readers)
	founds := make([]bool, readers)
	require.NoError(t, Extend(root, &testExtension{name: "redis", extend: func(x *ExtensionContext) error {
		start := make(chan struct{})
		var wg sync.WaitGroup
		for index := range readers {
			wg.Go(func() {
				<-start
				values[index], founds[index] = x.Properties().Lookup("addr")
			})
		}
		close(start)
		wg.Wait()
		return nil
	}}))

	for index := range readers {
		assert.True(t, founds[index], "reader %d", index)
		assert.Equal(t, "a:1", values[index], "reader %d", index)
	}
	assert.Equal(t, int32(1), provided.Load())
}

func TestProperties_Secrecy(t *testing.T) {
	t.Parallel()

	var logs logLines
	root := NewWithOpts(&InjectorOpts{Logf: logs.logf, PropertySource: mapSource{"redis.password": "s3cret"}})

	err := Extend(root, &testExtension{name: "redis", extend: func(x *ExtensionContext) error {
		if _, ok := x.Properties().Lookup("password"); !ok {
			return errors.New("no password")
		}
		return errors.New("auth failed")
	}})

	require.ErrorContains(t, err, "auth failed")
	assert.NotContains(t, err.Error(), "s3cret")
	assert.Empty(t, logs.containing("s3cret"))
	assert.Len(t, logs.containing("property source"), 1)
}

func TestExtensionProperties_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		clone bool
		value string
	}{
		{name: "root", clone: false, value: "orig"},
		{name: "clone", clone: true, value: "cloned"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := NewWithOpts(&InjectorOpts{PropertySource: mapSource{"redis.addr": "orig"}})
			var recorded atomic.Pointer[string]
			require.NoError(t, Extend(root, &testExtension{name: "redis", extend: func(x *ExtensionContext) error {
				Provide(x.Scope(), func(i Injector) (*extClient, error) {
					properties, _ := ExtensionProperties(i)
					addr, _ := properties.Lookup("addr")
					recorded.Store(&addr)
					return &extClient{addr: addr}, nil
				})
				return ExtensionExport[*extClient](x)
			}}))
			container := root
			if tt.clone {
				container = root.CloneWithOpts(&InjectorOpts{PropertySource: mapSource{"redis.addr": "cloned"}})
			}

			_, err := Invoke[*extClient](container)

			require.NoError(t, err)
			require.NotNil(t, recorded.Load())
			assert.Equal(t, tt.value, *recorded.Load())
		})
	}
}

func TestExtensionProperties_OutsideExtension(t *testing.T) {
	t.Parallel()

	root := NewWithOpts(&InjectorOpts{PropertySource: mapSource{"redis.addr": "a:1", "addr": "x"}})
	var (
		properties Properties
		ok         bool
	)
	Provide(root, func(i Injector) (*appCache, error) {
		properties, ok = ExtensionProperties(i)
		return &appCache{}, nil
	})

	_, err := Invoke[*appCache](root)

	require.NoError(t, err)
	assert.False(t, ok)
	for _, key := range []string{"addr", "redis.addr"} {
		value, found := properties.Lookup(key)
		assert.False(t, found, key)
		assert.Empty(t, value, key)
	}
}
