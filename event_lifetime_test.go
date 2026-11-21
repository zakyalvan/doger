package doger

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// namedListener is a listener service recording its name in a recorder.
type namedListener struct {
	name string
	rec  *recorder
}

func (l *namedListener) OnEvent(context.Context, OrderPlaced) error {
	l.rec.names = append(l.rec.names, l.name)
	return nil
}

func TestListenerLifetime(t *testing.T) {
	t.Parallel()

	// listening and silent provide a *namedListener named "*Mailer" whose
	// instance records label.
	listening := func(rec *recorder, label string) Provider[*namedListener] {
		return func(Injector) (*namedListener, error) { return &namedListener{name: label, rec: rec}, nil }
	}

	tests := []struct {
		name  string
		setup func(root *RootScope, rec *recorder) Injector // returns the injector to publish through
		ran   []string
	}{
		{
			name: "listener overridden by a service without it",
			setup: func(root *RootScope, rec *recorder) Injector {
				ProvideNamed(root, "*Mailer", listening(rec, "*Mailer"), AsListener[OrderPlaced]())
				OverrideNamed(root, "*Mailer", listening(rec, "*Mailer"))
				return root
			},
			ran: []string{},
		},
		{
			name: "service overridden by a listener",
			setup: func(root *RootScope, rec *recorder) Injector {
				ProvideNamed(root, "*Mailer", listening(rec, "*Mailer"))
				OverrideNamed(root, "*Mailer", listening(rec, "*Mailer"), AsListener[OrderPlaced]())
				return root
			},
			ran: []string{"*Mailer"},
		},
		{
			name: "AsDefault listener replaced by an app service without it",
			setup: func(root *RootScope, rec *recorder) Injector {
				ProvideNamed(root, "*Mailer", listening(rec, "default"), AsDefault(), AsListener[OrderPlaced]())
				ProvideNamed(root, "*Mailer", listening(rec, "app"))
				return root
			},
			ran: []string{},
		},
		{
			name: "AsDefault listener yields to an app listener registered first",
			setup: func(root *RootScope, rec *recorder) Injector {
				ProvideNamed(root, "*Mailer", listening(rec, "app"), AsListener[OrderPlaced]())
				ProvideNamed(root, "*Mailer", listening(rec, "default"), AsDefault(), AsListener[OrderPlaced]())
				return root
			},
			ran: []string{"app"},
		},
		{
			name: "AsDefault listener in a child yields to an app service of the root",
			setup: func(root *RootScope, rec *recorder) Injector {
				ProvideNamed(root, "*Mailer", listening(rec, "app"))
				req := root.Scope("req")
				ProvideNamed(req, "*Mailer", listening(rec, "default"), AsDefault(), AsListener[OrderPlaced]())
				return req
			},
			ran: []string{},
		},
		{
			name: "child scope shut down",
			setup: func(root *RootScope, rec *recorder) Injector {
				req := root.Scope("req")
				Listen(req, recordAs[OrderPlaced](rec, "f", nil))
				ProvideNamed(req, "*Mailer", listening(rec, "*Mailer"), AsListener[OrderPlaced]())
				require.True(t, req.Shutdown().Succeed)
				return root
			},
			ran: []string{},
		},
		{
			name: "child scope shut down and recreated",
			setup: func(root *RootScope, rec *recorder) Injector {
				req := root.Scope("req")
				Listen(req, recordAs[OrderPlaced](rec, "f", nil))
				require.True(t, req.Shutdown().Succeed)
				req = root.Scope("req")
				Listen(req, recordAs[OrderPlaced](rec, "g", nil))
				return req
			},
			ran: []string{"g"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			rec := &recorder{names: []string{}}
			target := tt.setup(root, rec)

			require.NoError(t, Publish(context.Background(), target, OrderPlaced{ID: "1"}))
			assert.Equal(t, tt.ran, rec.names)
		})
	}
}

func TestPublish_ShutDownScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		shutdown func(root *RootScope, req *Scope)
	}{
		{name: "root shut down", shutdown: func(root *RootScope, _ *Scope) { root.Shutdown() }},
		{name: "req shut down", shutdown: func(_ *RootScope, req *Scope) { req.Shutdown() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			req := root.Scope("req")
			calls := countListener[OrderPlaced](t, req)
			tt.shutdown(root, req)

			err := Publish(context.Background(), req, OrderPlaced{ID: "1"})

			require.ErrorIs(t, err, ErrPublishAfterShutdown)
			assert.Zero(t, *calls)
		})
	}
}
