package doger

import "context"

// The container lifecycle events: the root publishes each one right after
// the container hooks of the same point, on the goroutine running them (see
// RootScope.StartWithContext and RootScope.ShutdownWithContext). Their
// listeners only observe: every one runs, whatever EventErrorMode, and their
// errors, panics and build failures go to InjectorOpts.HookEventError,
// never changing the result of Start or Shutdown. Listening to them costs
// nothing to containers that do not.

// ContainerStarting is published by the first Start once every Starting hook
// passed, before discovery: a listener service built for it is the first
// instantiation of the start, and a failed build is reported, not a veto.
// Listen to ContainerStarted to avoid building before discovery.
type ContainerStarting struct {
	Root *RootScope
}

// ContainerStarted is published by the first Start after the Started hooks,
// once its pipeline succeeded.
type ContainerStarted struct {
	Root *RootScope
}

// ContainerReady is published by the first Start after the Ready hooks.
type ContainerReady struct {
	Root *RootScope
}

// ContainerFailed is published by the first Start after the Failed hooks,
// with the error Start returns; for a panic Start re-raises, before the
// re-panic.
type ContainerFailed struct {
	Root *RootScope
	Err  error
}

// ContainerClosing is published by the first root Shutdown after the
// Closing hooks, before any service shuts down.
type ContainerClosing struct {
	Root *RootScope
}

// ContainerClosed is published by the first root Shutdown after the Closed
// hooks, with the report Shutdown returns. Only function listeners receive
// it: service listeners are shut down by then. Async deliveries are no
// longer accepted at that point, so its async listeners run inline, on the
// goroutine running Shutdown, before Shutdown returns.
type ContainerClosed struct {
	Root   *RootScope
	Report *ShutdownReport
}

// noteListeners allocates the event state of the container when opts
// declare listeners, so that lifecycle events reach service listeners in a
// container without function listeners.
func (s *RootScope) noteListeners(opts *serviceOptions) {
	if opts != nil && len(opts.listeners) > 0 {
		s.eventState()
	}
}

// publishLifecycle publishes a container lifecycle event through the root:
// every listener runs, whatever EventErrorMode, and their failures go to
// HookEventError, never to the caller. ContainerClosed reaches function
// listeners only. It costs one check when the container has no event state: no
// function listener nor listener registration ever made one.
func (s *RootScope) publishLifecycle(ctx context.Context, event any) {
	if s.events.Load() == nil {
		return
	}

	_, funcOnly := event.(ContainerClosed)
	_ = s.dispatch(context.WithoutCancel(ctx), s.self, event, dispatchConfig{report: true, funcOnly: funcOnly})
}
