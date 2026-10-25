package doger

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// provideBuildCountedServer registers lazy Starter "server" in root and
// returns the counter of its provider calls.
func provideBuildCountedServer(root *RootScope) *atomic.Int32 {
	builds := &atomic.Int32{}
	ProvideNamed(root, "server", func(Injector) (*startWithErr, error) {
		builds.Add(1)
		return &startWithErr{}, nil
	})
	return builds
}

func TestContainerStartingHook_Veto(t *testing.T) {
	t.Parallel()

	errMissing := errors.New("missing DB_URL")
	errLicense := errors.New("no license")

	tests := []struct {
		name    string
		results []error
		wantErr error
		builds  int32
		ran     int32
	}{
		{name: "nil", results: []error{nil}, wantErr: nil, builds: 1, ran: 1},
		{name: "nil, nil", results: []error{nil, nil}, wantErr: nil, builds: 1, ran: 2},
		{name: "missing DB_URL", results: []error{errMissing}, wantErr: errMissing, builds: 0, ran: 1},
		{name: "nil, no license, nil", results: []error{nil, errLicense, nil}, wantErr: errLicense, builds: 0, ran: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			var ran atomic.Int32
			opts := &InjectorOpts{}
			for _, result := range tt.results {
				opts.HookContainerStarting = append(opts.HookContainerStarting, func(context.Context, *RootScope) error {
					ran.Add(1)
					return result
				})
			}
			root := NewWithOpts(opts)
			builds := provideBuildCountedServer(root)

			err := root.Start()

			if tt.wantErr == nil {
				is.NoError(err)
			} else {
				is.ErrorIs(err, tt.wantErr)
			}
			is.Equal(tt.builds, builds.Load())
			is.Equal(tt.ran, ran.Load())
		})
	}
}

func TestContainerStartingHook_BeforeContextCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result func(ctx context.Context) error
	}{
		{name: "returns ctx.Err()", result: func(ctx context.Context) error { return ctx.Err() }},
		{name: "returns nil", result: func(context.Context) error { return nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			builds := provideBuildCountedServer(root)
			var ran atomic.Int32
			root.AddContainerStartingHook(func(ctx context.Context, _ *RootScope) error {
				ran.Add(1)
				return tt.result(ctx)
			})

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := root.StartWithContext(ctx)

			is.ErrorIs(err, context.Canceled)
			is.Equal(int32(1), ran.Load())
			is.Equal(int32(0), builds.Load())
		})
	}
}

func TestContainerStartingHook_ReceivesContext(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	var gotValue any
	var gotRoot *RootScope
	root.AddContainerStartingHook(func(ctx context.Context, r *RootScope) error {
		gotValue = ctx.Value(ctxTestKey)
		gotRoot = r
		return nil
	})

	ctx := context.WithValue(context.Background(), ctxTestKey, "abc")
	require.NoError(t, root.StartWithContext(ctx))

	is.Equal("abc", gotValue)
	is.Same(root, gotRoot)
}
