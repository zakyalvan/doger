package doger

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShutdownReport_ErrorFormatting(t *testing.T) {
	t.Parallel()
	is := assert.New(t)

	desc := ServiceDescription{ScopeID: "sid", ScopeName: "sname", Service: "svc"}
	rep := ShutdownReport{
		Succeed:             false,
		Services:            []ServiceDescription{desc},
		Errors:              map[ServiceDescription]error{desc: assert.AnError},
		ShutdownTime:        0,
		ServiceShutdownTime: map[ServiceDescription]time.Duration{desc: time.Millisecond},
	}

	is.Len(rep.Services, 1)
	is.Len(rep.Errors, 1)
	is.Len(rep.ServiceShutdownTime, 1)
	msg := rep.Error()
	is.Contains(msg, "DI: shutdown errors:")
	is.Contains(msg, "sname > svc")
}

func TestScope_Shutdown_ReportFields(t *testing.T) {
	t.Parallel()
	is := assert.New(t)

	i := New()

	// OK shutdowner
	ProvideNamedValue(i, "ok-svc", &lazyTestShutdownerOK{})
	// Failing shutdowner
	ProvideNamedValue(i, "ko-svc", &lazyTestShutdownerKO{})

	// Invoke both
	_, _ = InvokeNamed[*lazyTestShutdownerOK](i, "ok-svc")
	_, _ = InvokeNamed[*lazyTestShutdownerKO](i, "ko-svc")

	rep := i.Shutdown()

	is.NotNil(rep)
	is.False(rep.Succeed)
	is.Len(rep.Errors, 1)
	// Two services were shut down
	is.Len(rep.Services, 2)
	is.Len(rep.ServiceShutdownTime, 2)

	// Error should be attached to the failing service desc
	failing := ServiceDescription{ScopeID: i.self.id, ScopeName: i.self.name, Service: "ko-svc"}
	_, ok := rep.Errors[failing]
	is.True(ok)
}

func TestScope_ShutdownWithContext_ReportTimings(t *testing.T) {
	t.Parallel()
	is := assert.New(t)

	i := New()
	// Create a slow shutdowner (100ms)
	slow := newScopeTestSlowShutdowner(50 * time.Millisecond)
	ProvideNamedValue(i, "slow", slow)
	_, _ = InvokeNamed[*scopeTestSlowShutdowner](i, "slow")

	// Use a generous timeout to allow shutdown to complete
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	rep := i.ShutdownWithContext(ctx)

	is.NotNil(rep)
	is.True(rep.Succeed)
	// One service
	is.Len(rep.Services, 1)
	// Per-service timing should be recorded and > 0
	desc := ServiceDescription{ScopeID: i.self.id, ScopeName: i.self.name, Service: "slow"}
	dt, ok := rep.ServiceShutdownTime[desc]
	is.True(ok)
	is.Greater(dt, time.Duration(0))
}

func TestMergeShutdownReports_Succeed(t *testing.T) {
	t.Parallel()

	desc := ServiceDescription{ScopeID: "sid", ScopeName: "sname", Service: "svc"}
	ok := func() *ShutdownReport {
		return &ShutdownReport{
			Succeed:             true,
			Services:            []ServiceDescription{desc},
			Errors:              map[ServiceDescription]error{},
			ServiceShutdownTime: map[ServiceDescription]time.Duration{},
		}
	}
	failing := &ShutdownReport{
		Succeed:             false,
		Services:            []ServiceDescription{desc},
		Errors:              map[ServiceDescription]error{desc: assert.AnError},
		ServiceShutdownTime: map[ServiceDescription]time.Duration{},
	}

	tests := []struct {
		name    string
		reports []*ShutdownReport
		succeed bool
	}{
		{name: "no reports", reports: nil, succeed: true},
		{name: "two successful", reports: []*ShutdownReport{ok(), ok()}, succeed: true},
		{name: "one failing", reports: []*ShutdownReport{ok(), failing}, succeed: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.succeed, mergeShutdownReports(tt.reports...).Succeed)
		})
	}
}

func TestStartError_ConsumerBuilt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		err     *StartError
		message string
	}{
		{
			name:    "no Scope",
			err:     &StartError{Service: "db", Op: StartOpStart, Cause: errors.New("boom")},
			message: "DI: failed to start service `db` in scope ``: boom",
		},
		{
			name:    "no Cause",
			err:     &StartError{Service: "db", Op: StartOpBuild, Scope: New().self},
			message: "DI: failed to build service `db` in scope `[root]`: <nil>",
		},
		{
			name:    "handle without Scope and Cause",
			err:     &StartError{Service: "j", Kind: "k", Op: StartOpHandle},
			message: "DI: kind `k` failed for service `j` in scope ``: <nil>",
		},
		{
			name:    "zero value",
			err:     &StartError{},
			message: "DI: failed to build service `` in scope ``: <nil>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var message string
			require.NotPanics(t, func() { message = tt.err.Error() })
			assert.Equal(t, tt.message, message)
		})
	}
}
