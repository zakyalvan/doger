package doger

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClock_DefaultRealClock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		build func() *RootScope
		sleep time.Duration
		want  string // "" for nil
	}{
		{name: "timeout_10ms_check_20ms", build: func() *RootScope { return NewWithOpts(&InjectorOpts{HealthCheckTimeout: 10 * time.Millisecond}) }, sleep: 20 * time.Millisecond, want: healthCheckTestDeadlineMessage},
		{name: "timeout_10ms_check_5ms", build: func() *RootScope { return NewWithOpts(&InjectorOpts{HealthCheckTimeout: 10 * time.Millisecond}) }, sleep: 5 * time.Millisecond},
		{name: "timeout_10ms_nil_clock_check_20ms", build: func() *RootScope {
			return NewWithOpts(&InjectorOpts{HealthCheckTimeout: 10 * time.Millisecond, Clock: nil})
		}, sleep: 20 * time.Millisecond, want: healthCheckTestDeadlineMessage},
		{name: "new_no_timeout_check_20ms", build: func() *RootScope { return New() }, sleep: 20 * time.Millisecond},
		{name: "nil_opts_no_timeout_check_20ms", build: func() *RootScope { return NewWithOpts(nil) }, sleep: 20 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				i := tt.build()
				ProvideNamedValue(i, "svc", &healthCheckTestOnClock{clock: clockwork.NewRealClock(), duration: tt.sleep})

				err := i.HealthCheckWithContext(context.Background())["svc"]
				if tt.want == "" {
					assert.NoError(t, err)
				} else {
					assert.EqualError(t, err, tt.want)
				}

				time.Sleep(tt.sleep) // let an abandoned check return
			})
		})
	}
}

// clockTestAdvancer advances a fake clock when it is built and when it is
// shut down.
type clockTestAdvancer struct {
	clock    *clockwork.FakeClock
	shutdown time.Duration
}

func (c *clockTestAdvancer) Shutdown() { c.clock.Advance(c.shutdown) }

func TestClock_ReportedDurations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		buildAdvance  time.Duration
		shutdownAdvnc time.Duration
		measure       func(t *testing.T, i *RootScope) time.Duration
		want          time.Duration
	}{
		{
			name: "build_time_3s", buildAdvance: 3 * time.Second, want: 3 * time.Second,
			measure: func(t *testing.T, i *RootScope) time.Duration {
				t.Helper()
				out, ok := ExplainNamedService(i, "svc")
				require.True(t, ok)
				return out.ServiceBuildTime
			},
		},
		{
			name: "build_time_0s_omitted", want: 0,
			measure: func(t *testing.T, i *RootScope) time.Duration {
				t.Helper()
				out, ok := ExplainNamedService(i, "svc")
				require.True(t, ok)
				assert.NotContains(t, out.String(), "Service build time")
				return out.ServiceBuildTime
			},
		},
		{
			name: "service_shutdown_time_2s", shutdownAdvnc: 2 * time.Second, want: 2 * time.Second,
			measure: func(t *testing.T, i *RootScope) time.Duration {
				t.Helper()
				report := i.Shutdown()
				return report.ServiceShutdownTime[newServiceDescription(i.ID(), i.Name(), "svc")]
			},
		},
		{
			name: "shutdown_time_2s", shutdownAdvnc: 2 * time.Second, want: 2 * time.Second,
			measure: func(t *testing.T, i *RootScope) time.Duration {
				t.Helper()
				return i.Shutdown().ShutdownTime
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fc := clockwork.NewFakeClock()
			i := NewWithOpts(&InjectorOpts{Clock: fc})
			ProvideNamed(i, "svc", func(Injector) (*clockTestAdvancer, error) {
				fc.Advance(tt.buildAdvance)
				return &clockTestAdvancer{clock: fc, shutdown: tt.shutdownAdvnc}, nil
			})
			_, err := InvokeNamed[*clockTestAdvancer](i, "svc")
			require.NoError(t, err)

			assert.Equal(t, tt.want, tt.measure(t, i))
		})
	}
}

func TestClock_TimeoutErrorSameOnAnyClock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fake bool
	}{
		{name: "nil_real_clock", fake: false},
		{name: "fake_clock", fake: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				is := assert.New(t)

				opts := &InjectorOpts{HealthCheckTimeout: 10 * time.Millisecond}
				fc := clockwork.NewFakeClock()
				if tt.fake {
					opts.Clock = fc
				}
				i := NewWithOpts(opts)
				check := newHealthCheckTestBlocking()
				ProvideNamedValue(i, "svc", check)

				result := healthCheckTestAsync(context.Background(), i)
				synctest.Wait()
				if tt.fake {
					fc.Advance(10 * time.Millisecond)
				}
				err := (<-result)["svc"]

				is.EqualError(err, healthCheckTestDeadlineMessage)
				is.ErrorIs(err, ErrHealthCheckTimeout)
				is.ErrorIs(err, context.DeadlineExceeded)

				close(check.release)
				synctest.Wait()
			})
		})
	}
}

func TestClock_TimeoutWaitsForClock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts func(clockwork.Clock) *InjectorOpts
	}{
		{name: "health_check_timeout", opts: func(c clockwork.Clock) *InjectorOpts {
			return &InjectorOpts{Clock: c, HealthCheckTimeout: 10 * time.Millisecond}
		}},
		{name: "health_check_global_timeout", opts: func(c clockwork.Clock) *InjectorOpts {
			return &InjectorOpts{Clock: c, HealthCheckGlobalTimeout: 10 * time.Millisecond}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)
			is := assert.New(t)
			must := require.New(t)

			fc := clockwork.NewFakeClock()
			i := NewWithOpts(tt.opts(fc))
			check := newHealthCheckTestBlocking()
			defer close(check.release)
			ProvideNamedValue(i, "svc", check)

			result := healthCheckTestAsync(context.Background(), i)
			waitCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			must.NoError(fc.BlockUntilContext(waitCtx, 1)) // the timeout's timer

			time.Sleep(50 * time.Millisecond) // wall time, fake clock not advanced
			is.False(healthCheckTestReturned(result), "returned on wall time")

			fc.Advance(10 * time.Millisecond)
			is.EqualError((<-result)["svc"], healthCheckTestDeadlineMessage)
		})
	}
}

func TestClock_DerivedInjectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// target registers "svc" (blocking until released) and returns the
		// injector to check, plus a clock to advance to finish the check
		// when F does not.
		target       func(root *RootScope, check Healthchecker) (Injector, *clockwork.FakeClock)
		stillRunning bool
	}{
		{
			name: "service_in_child_scope",
			target: func(root *RootScope, check Healthchecker) (Injector, *clockwork.FakeClock) {
				child := root.Scope("c")
				ProvideNamedValue(child, "svc", check)
				return child, nil
			},
		},
		{
			name: "inherited_service_from_child_scope",
			target: func(root *RootScope, check Healthchecker) (Injector, *clockwork.FakeClock) {
				ProvideNamedValue(root, "svc", check)
				return root.Scope("c"), nil
			},
		},
		{
			name: "clone",
			target: func(root *RootScope, check Healthchecker) (Injector, *clockwork.FakeClock) {
				ProvideNamedValue(root, "svc", check)
				return root.Clone(), nil
			},
		},
		{
			name: "clone_with_opts_own_fake_clock",
			target: func(root *RootScope, check Healthchecker) (Injector, *clockwork.FakeClock) {
				ProvideNamedValue(root, "svc", check)
				g := clockwork.NewFakeClock()
				return root.CloneWithOpts(&InjectorOpts{Clock: g, HealthCheckTimeout: 10 * time.Millisecond}), g
			},
			stillRunning: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				is := assert.New(t)

				f := clockwork.NewFakeClock()
				root := NewWithOpts(&InjectorOpts{Clock: f, HealthCheckTimeout: 10 * time.Millisecond})
				check := newHealthCheckTestBlocking()
				target, g := tt.target(root, check)

				result := healthCheckTestAsync(context.Background(), target)
				synctest.Wait()
				f.Advance(10 * time.Millisecond)
				synctest.Wait()

				if tt.stillRunning {
					is.False(healthCheckTestReturned(result), "timed out on the root's clock")
					g.Advance(10 * time.Millisecond)
				}
				err := (<-result)["svc"]
				is.EqualError(err, healthCheckTestDeadlineMessage)
				is.ErrorIs(err, ErrHealthCheckTimeout)

				close(check.release)
				synctest.Wait()
			})
		})
	}
}
