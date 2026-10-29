package doger

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestHealthCheck(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()

	Provide(i, func(i Injector) (*lazyTest, error) { return &lazyTest{}, nil })
	Provide(i, func(i Injector) (*lazyTestHeathcheckerOK, error) { return &lazyTestHeathcheckerOK{}, nil })
	Provide(i, func(i Injector) (*lazyTestHeathcheckerKO, error) { return &lazyTestHeathcheckerKO{}, nil })

	is.NoError(HealthCheck[*lazyTest](i))
	is.NoError(HealthCheck[*lazyTestHeathcheckerOK](i))
	is.NoError(HealthCheck[*lazyTestHeathcheckerKO](i))

	_, _ = Invoke[*lazyTest](i)
	_, _ = Invoke[*lazyTestHeathcheckerOK](i)
	_, _ = Invoke[*lazyTestHeathcheckerKO](i)

	is.NoError(HealthCheck[*lazyTest](i))
	is.NoError(HealthCheck[*lazyTestHeathcheckerOK](i))
	is.Equal(assert.AnError, HealthCheck[*lazyTestHeathcheckerKO](i))
}

func TestHealthCheckWithContext(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()
	ctx := context.Background()

	Provide(i, func(i Injector) (*lazyTestHeathcheckerKOCtx, error) {
		return &lazyTestHeathcheckerKOCtx{foobar: "foobar"}, nil
	})
	is.NoError(HealthCheckWithContext[*lazyTestHeathcheckerKOCtx](ctx, i))
	_, _ = Invoke[*lazyTestHeathcheckerKOCtx](i)

	is.Equal(assert.AnError, HealthCheckWithContext[*lazyTestHeathcheckerKOCtx](ctx, i))
}

func TestHealthCheckNamed(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()

	ProvideNamed(i, "foobar", func(i Injector) (*lazyTestHeathcheckerKO, error) { return &lazyTestHeathcheckerKO{}, nil })
	is.NoError(HealthCheckNamed(i, "foobar"))
	_, _ = InvokeNamed[*lazyTestHeathcheckerKO](i, "foobar")

	is.Equal(assert.AnError, HealthCheckNamed(i, "foobar"))
}

func TestHealthCheckNamedWithContext(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()
	ctx := context.Background()

	ProvideNamed(i, "foobar", func(i Injector) (*lazyTestHeathcheckerKOCtx, error) {
		return &lazyTestHeathcheckerKOCtx{foobar: "foobar"}, nil
	})
	is.NoError(HealthCheckNamedWithContext(ctx, i, "foobar"))
	_, _ = InvokeNamed[*lazyTestHeathcheckerKOCtx](i, "foobar")

	is.Equal(assert.AnError, HealthCheckNamedWithContext(ctx, i, "foobar"))
}

func TestShutdown(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	type test struct {
		foobar string
	}

	i := New()

	Provide(i, func(i Injector) (test, error) {
		return test{foobar: "foobar"}, nil
	})

	instance, err := Invoke[test](i)
	is.Equal(test{foobar: "foobar"}, instance)
	is.NoError(err)

	err = Shutdown[test](i)
	is.NoError(err)

	instance, err = Invoke[test](i)
	is.Empty(instance)
	is.Error(err)

	err = Shutdown[test](i)
	is.Error(err)
}

func TestShutdownWithContext(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()
	ctx := context.Background()

	Provide(i, func(i Injector) (*lazyTestShutdownerKOCtx, error) {
		return &lazyTestShutdownerKOCtx{foobar: "foobar"}, nil
	})
	is.NoError(ShutdownWithContext[*lazyTestShutdownerKOCtx](ctx, i))
	_, _ = Invoke[*lazyTestShutdownerKOCtx](i)

	is.EqualError(ShutdownWithContext[*lazyTestShutdownerKOCtx](ctx, i), "DI: could not find service `*github.com/kartaladev/doger.lazyTestShutdownerKOCtx`, no service available")
}

func TestMustShutdown(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	type test struct {
		foobar string
	}

	i := New()

	Provide(i, func(i Injector) (test, error) {
		return test{foobar: "foobar"}, nil
	})

	instance, err := Invoke[test](i)
	is.Equal(test{foobar: "foobar"}, instance)
	is.NoError(err)

	is.NotPanics(func() {
		MustShutdown[test](i)
	})

	instance, err = Invoke[test](i)
	is.Empty(instance)
	is.Error(err)

	is.Panics(func() {
		MustShutdown[test](i)
	})
}

func TestMustShutdownWithContext(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()
	ctx := context.Background()

	Provide(i, func(i Injector) (*lazyTestShutdownerKOCtx, error) {
		return &lazyTestShutdownerKOCtx{foobar: "foobar"}, nil
	})
	is.NotPanics(func() {
		MustShutdownWithContext[*lazyTestShutdownerKOCtx](ctx, i)
	})
	_, _ = Invoke[*lazyTestShutdownerKOCtx](i)

	is.PanicsWithError("DI: could not find service `*github.com/kartaladev/doger.lazyTestShutdownerKOCtx`, no service available", func() {
		MustShutdownWithContext[*lazyTestShutdownerKOCtx](ctx, i)
	})
}

func TestShutdownNamed(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()

	ProvideNamedValue(i, "foobar", 42)

	instance, err := InvokeNamed[int](i, "foobar")
	is.Equal(42, instance)
	is.NoError(err)

	err = ShutdownNamed(i, "foobar")
	is.NoError(err)

	instance, err = InvokeNamed[int](i, "foobar")
	is.Empty(instance)
	is.Error(err)

	err = ShutdownNamed(i, "foobar")
	is.Error(err)
}

func TestShutdownNamedWithContext(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()
	ctx := context.Background()

	ProvideNamed(i, "foobar", func(i Injector) (*lazyTestShutdownerKOCtx, error) {
		return &lazyTestShutdownerKOCtx{foobar: "foobar"}, nil
	})
	is.NoError(ShutdownNamedWithContext(ctx, i, "foobar"))
	_, _ = Invoke[*lazyTestShutdownerKOCtx](i)

	is.EqualError(ShutdownNamedWithContext(ctx, i, "foobar"), "DI: could not find service `foobar`, no service available")
}

func TestMustShutdownNamed(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()

	ProvideNamedValue(i, "foobar", 42)

	instance, err := InvokeNamed[int](i, "foobar")
	is.Equal(42, instance)
	is.NoError(err)

	is.NotPanics(func() {
		MustShutdownNamed(i, "foobar")
	})

	instance, err = InvokeNamed[int](i, "foobar")
	is.Empty(instance)
	is.Error(err)

	is.Panics(func() {
		MustShutdownNamed(i, "foobar")
	})
}

func TestMustShutdownNamedWithContext(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()
	ctx := context.Background()

	ProvideNamed(i, "foobar", func(i Injector) (*lazyTestShutdownerKOCtx, error) {
		return &lazyTestShutdownerKOCtx{foobar: "foobar"}, nil
	})
	is.NotPanics(func() {
		MustShutdownNamedWithContext(ctx, i, "foobar")
	})
	_, _ = InvokeNamed[*lazyTestShutdownerKOCtx](i, "foobar")

	is.PanicsWithError("DI: could not find service `foobar`, no service available", func() {
		MustShutdownNamedWithContext(ctx, i, "foobar")
	})
}

func TestDoubleInjection(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	type test struct{}

	i := New()

	is.NotPanics(func() {
		Provide(i, func(i Injector) (*test, error) {
			return &test{}, nil
		})
	})

	const declared = "DI: service `*github.com/kartaladev/doger.test` has already been declared"

	// the rows run in order on the same injector, each redeclaring *test
	tests := []struct {
		name    string
		provide func()
		message string
	}{
		{
			name: "Provide",
			provide: func() {
				Provide(i, func(i Injector) (*test, error) {
					return &test{}, nil
				})
			},
			message: declared,
		},
		{
			name:    "ProvideValue",
			provide: func() { ProvideValue(i, &test{}) },
			message: declared,
		},
		{
			name: "ProvideNamed",
			provide: func() {
				ProvideNamed(i, "*github.com/kartaladev/doger.test", func(i Injector) (*test, error) {
					return &test{}, nil
				})
			},
			message: declared,
		},
		{
			name:    "ProvideNamedValue",
			provide: func() { ProvideNamedValue(i, "*github.com/kartaladev/doger.test", &test{}) },
			message: declared,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			is.PanicsWithError(tt.message, tt.provide)
		})
	}
}
