package doger

import (
	"context"
	"sync"
	"sync/atomic"
)

// EventExecutor runs async event deliveries (see [ListenerAsync]).
//
// Submit runs task, now or later, with a context the task honours; it
// returns an error when it refuses the task, which Publish then returns as a
// [*ListenerError]. Every task Submit accepts (returns nil for) must run,
// even once Shutdown began: root and scope shutdown wait for each accepted
// delivery, bounded only by their context, so an executor that discards an
// accepted task makes Shutdown (context.Background()) wait forever. Shutdown stops accepting tasks and waits for the
// running ones, bounded by ctx; its signature matches
// [net/http.Server.Shutdown], so an executor can also be a doger
// ShutdownerWithContextAndError service.
type EventExecutor interface {
	Submit(ctx context.Context, task func(context.Context)) error
	Shutdown(ctx context.Context) error
}

// BoundedEventExecutor is the default [EventExecutor]: it runs at most n
// deliveries at once, each on its own goroutine. A Submit finding every
// slot taken waits for one, or for its context's end, which applies
// backpressure to publishers.
type BoundedEventExecutor struct {
	slots   chan struct{} // one value per running task
	closing chan struct{} // closed by Shutdown: no task is accepted from then on

	mu      sync.Mutex      // guards closing being closed and running's increments, so Shutdown never misses a task it accepted
	running inflightCounter // the tasks running on goroutines of their own

	abortCtx context.Context //nolint:containedctx // the parent of every task context, cancelled when a Shutdown deadline passes
	abort    context.CancelCauseFunc
}

var _ EventExecutor = (*BoundedEventExecutor)(nil)

// NewBoundedEventExecutor returns an executor running at most n tasks at
// once. It panics with "doger: NewBoundedEventExecutor requires n > 0" for
// n == 0.
//
// A Submit fails at once with its context's error when that context is
// done, even when a slot is free, and with an error matching
// [ErrPublishAfterShutdown] once Shutdown began, which also wakes the
// Submit calls waiting for a slot. A Submit made from a task of this
// executor (a nested publish) never waits: when no slot is free, it runs
// the task inline on the calling goroutine, so nested async publishing
// cannot deadlock; that nested delivery is then synchronous.
//
// Each task runs with a context that keeps the values of the Submit
// context but not its cancellation, and that is cancelled with cause
// ErrPublishAfterShutdown when a Shutdown deadline passes.
func NewBoundedEventExecutor(n uint) *BoundedEventExecutor {
	if n == 0 {
		panic(libErrorf("NewBoundedEventExecutor requires n > 0"))
	}

	abortCtx, abort := context.WithCancelCause(context.Background())

	return &BoundedEventExecutor{
		slots:    make(chan struct{}, n),
		closing:  make(chan struct{}),
		abortCtx: abortCtx,
		abort:    abort,
	}
}

// boundedExecutorKey marks the context of a task with its *boundedTask.
type boundedExecutorKey struct{}

// boundedTask is a task of a BoundedEventExecutor, as its context knows it.
type boundedTask struct {
	executor *BoundedEventExecutor
	parent   *boundedTask // the task whose context the task was submitted with, if any
	counted  bool         // runs on a goroutine of its own, counted in running; false when inline
	ended    atomic.Bool
}

// taskOf returns the task whose context ctx is, or nil.
func taskOf(ctx context.Context) *boundedTask {
	task, _ := ctx.Value(boundedExecutorKey{}).(*boundedTask)

	return task
}

// Submit runs task on a new goroutine once a slot is free; see
// [NewBoundedEventExecutor].
func (e *BoundedEventExecutor) Submit(ctx context.Context, task func(context.Context)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.isClosing() {
		return e.closedError()
	}

	if !e.acquire(ctx) {
		if e.isTaskContext(ctx) && ctx.Err() == nil && !e.isClosing() {
			e.runInline(ctx, task)
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return e.closedError()
	}

	e.mu.Lock()
	if e.isClosing() {
		e.mu.Unlock()
		<-e.slots
		return e.closedError()
	}
	e.running.add(1)
	e.mu.Unlock()

	taskCtx, release := e.taskContext(ctx, true)
	go func() {
		defer e.running.add(-1)
		defer func() { <-e.slots }()
		defer release()
		task(taskCtx)
	}()

	return nil
}

// acquire takes a slot, waiting for one unless ctx is a task context of e;
// it reports false when it took none: ctx ended, Shutdown began, or a
// nested submission found no free slot.
func (e *BoundedEventExecutor) acquire(ctx context.Context) bool {
	select {
	case e.slots <- struct{}{}:
		return true
	default:
	}

	if e.isTaskContext(ctx) {
		return false
	}

	select {
	case e.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	case <-e.closing:
		return false
	}
}

// isTaskContext reports whether ctx is the context of a task of e: a
// submission made with it is nested.
func (e *BoundedEventExecutor) isTaskContext(ctx context.Context) bool {
	task := taskOf(ctx)

	return task != nil && task.executor == e
}

// callerTasks returns how many running tasks of e the goroutine calling
// with ctx is: 1 when ctx is the context of a running task of e, or of a
// task run inline by one, 0 otherwise.
func (e *BoundedEventExecutor) callerTasks(ctx context.Context) int {
	for task := taskOf(ctx); task != nil && !task.ended.Load(); task = task.parent {
		if task.counted {
			if task.executor == e {
				return 1
			}
			return 0 // another goroutine's task: the chain leaves this goroutine
		}
	}

	return 0
}

// runInline runs task on the calling goroutine, for a nested submission
// finding no free slot.
func (e *BoundedEventExecutor) runInline(ctx context.Context, task func(context.Context)) {
	taskCtx, release := e.taskContext(ctx, false)
	defer release()
	task(taskCtx)
}

// taskContext returns the context of a task submitted with ctx: its values,
// marked with the task, cancelled when a Shutdown deadline passes. counted
// tells whether the task runs on a goroutine of its own. The task calls
// release once it ends.
func (e *BoundedEventExecutor) taskContext(ctx context.Context, counted bool) (taskCtx context.Context, release func()) {
	task := &boundedTask{executor: e, parent: taskOf(ctx), counted: counted}
	taskCtx, stop := followAbort(context.WithValue(context.WithoutCancel(ctx), boundedExecutorKey{}, task), e.abortCtx)

	return taskCtx, func() {
		task.ended.Store(true)
		stop()
	}
}

// followAbort returns a context derived from parent that is also cancelled,
// with the same cause, when abortCtx is; release, called once the context is
// no longer used, stops following abortCtx and cancels it.
func followAbort(parent, abortCtx context.Context) (ctx context.Context, release func()) {
	ctx, cancel := context.WithCancelCause(parent)
	stop := context.AfterFunc(abortCtx, func() { cancel(context.Cause(abortCtx)) })

	return ctx, func() {
		stop()
		cancel(nil)
	}
}

// isClosing reports whether Shutdown began.
func (e *BoundedEventExecutor) isClosing() bool {
	select {
	case <-e.closing:
		return true
	default:
		return false
	}
}

// closedError is the error of a Submit refused because Shutdown began.
func (e *BoundedEventExecutor) closedError() error {
	return libErrorMatching(ErrPublishAfterShutdown, "event executor is shut down")
}

// Shutdown stops accepting tasks, wakes the Submit calls waiting for a slot,
// and waits for the running tasks, bounded by ctx. When ctx ends first, it
// cancels the tasks' contexts with cause ErrPublishAfterShutdown and returns
// ctx's error without waiting further. Calling it again waits again.
//
// Called with the context of one of its running tasks (a listener shutting
// its container down), it does not wait for that task, which could not end
// before Shutdown returns.
func (e *BoundedEventExecutor) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	e.mu.Lock()
	if !e.isClosing() {
		close(e.closing)
	}
	e.mu.Unlock()

	select {
	case <-e.running.atMost(e.callerTasks(ctx)):
		return nil
	case <-ctx.Done():
		e.abort(ErrPublishAfterShutdown)
		return ctx.Err()
	}
}
