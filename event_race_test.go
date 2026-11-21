package doger

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublish_RacingShutdown(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	root := New()
	var delivered atomic.Int32
	Listen(root, func(context.Context, OrderPlaced) error {
		delivered.Add(1)
		return nil
	}, ListenerAsync())

	stop := make(chan struct{})
	unexpected := make(chan error, 100)
	var publishers sync.WaitGroup
	for range 100 {
		publishers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := Publish(context.Background(), root, OrderPlaced{ID: "1"}); err != nil && !assert.ErrorIs(t, err, ErrPublishAfterShutdown) {
					unexpected <- err
					return
				}
			}
		})
	}

	time.Sleep(10 * time.Millisecond)
	report := root.Shutdown()
	close(stop)
	publishers.Wait()
	close(unexpected)

	assert.True(t, report.Succeed, report.Error())
	for err := range unexpected {
		t.Errorf("unexpected Publish error: %v", err)
	}
}

func TestPublish_ConcurrentRegistration(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	root := New()
	defer root.Shutdown()
	builds := &atomic.Int32{}
	mailer := &Mailer{}
	ProvideNamed(root, "*Mailer", func(Injector) (*Mailer, error) {
		builds.Add(1)
		return mailer, nil
	}, AsListener[OrderPlaced]())

	var wg sync.WaitGroup
	done := make(chan struct{})
	for range 10 {
		wg.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
				}
				stop := Listen(root, func(context.Context, OrderPlaced) error { return nil })
				stop()
			}
		})
	}
	for worker := range 5 {
		wg.Go(func() {
			for n := 0; ; n++ {
				select {
				case <-done:
					return
				default:
				}
				child := root.Scope(fmt.Sprintf("child-%d-%d", worker, n))
				Listen(child, func(context.Context, OrderPlaced) error { return nil })
				ProvideNamed(child, "*Mailer", func(Injector) (*Mailer, error) { return &Mailer{}, nil }, AsListener[OrderPlaced]())
				_ = Publish(context.Background(), child, OrderPlaced{ID: "child"}, PublishLocal())
				child.Shutdown()
			}
		})
	}

	var publishers sync.WaitGroup
	for range 100 {
		publishers.Go(func() {
			assert.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
		})
	}
	publishers.Wait()
	close(done)
	wg.Wait()

	assert.Equal(t, int32(1), builds.Load())
	require.Len(t, mailer.Received(), 100)
}
