package bench

import (
	"context"
	"fmt"
	"testing"

	"github.com/kartaladev/doger"
)

// benchEvent is the event of BenchmarkPublish.
type benchEvent struct{ ID int }

// benchListener is a listener service of benchEvent.
type benchListener struct{}

func (*benchListener) OnEvent(context.Context, benchEvent) error { return nil }

// BenchmarkPublish measures a synchronous Publish: the plan lookup and the
// dispatch hot path.
func BenchmarkPublish(b *testing.B) {
	for _, n := range []int{0, 1, 10, 100} {
		b.Run(fmt.Sprintf("%d-listeners", n), func(b *testing.B) {
			root := doger.New()
			for i := range n {
				if i%2 == 0 {
					doger.Listen(root, func(context.Context, benchEvent) error { return nil })
					continue
				}
				doger.ProvideNamed(root, fmt.Sprintf("listener-%d", i), func(doger.Injector) (*benchListener, error) {
					return &benchListener{}, nil
				}, doger.AsListener[benchEvent]())
			}
			// unrelated services the plan must skip
			for i := range 20 {
				doger.ProvideNamedValue(root, fmt.Sprintf("other-%d", i), i)
			}
			ctx := context.Background()

			b.ReportAllocs()
			for b.Loop() {
				if err := doger.Publish(ctx, root, benchEvent{ID: 1}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}

	b.Run("nested-scopes", func(b *testing.B) {
		root := doger.New()
		doger.Listen(root, func(context.Context, benchEvent) error { return nil })
		var scope doger.Injector = root
		for depth := range 5 {
			scope = scope.Scope(fmt.Sprintf("level-%d", depth))
			doger.Listen(scope, func(context.Context, benchEvent) error { return nil })
			doger.ProvideNamedValue(scope, "value", depth)
		}
		ctx := context.Background()

		b.ReportAllocs()
		for b.Loop() {
			if err := doger.Publish(ctx, scope, benchEvent{ID: 1}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkPublish_NotExternalized measures Publish of an event whose type
// is not externalized, in a container with a transport: it must cost what
// BenchmarkPublish costs, with no new allocation.
func BenchmarkPublish_NotExternalized(b *testing.B) {
	for _, registered := range []bool{false, true} {
		b.Run(fmt.Sprintf("registered=%t", registered), func(b *testing.B) {
			root := doger.NewWithOpts(&doger.InjectorOpts{EventTransport: doger.NewMemoryEventTransport()})
			if registered {
				if err := doger.RegisterEventType[benchEvent](root, "bench.event"); err != nil {
					b.Fatal(err)
				}
			}
			doger.Listen(root, func(context.Context, benchEvent) error { return nil })
			ctx := context.Background()

			b.ReportAllocs()
			for b.Loop() {
				if err := doger.Publish(ctx, root, benchEvent{ID: 1}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
