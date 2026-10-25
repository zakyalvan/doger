package doger

import "context"

// Runner is one-shot startup work, run by the built-in [Runnable] kind after
// every Starter has started.
//
// Run must return: long-running work belongs in a Starter that spawns its
// own goroutine, plus a Shutdowner that stops it. Run receives the Start-time
// context, which root Shutdown cancels; a Runner that honours it returns when
// Shutdown cancels Start.
type Runner interface {
	Run(ctx context.Context) error
}

// runnableKind is the kind AsRunnable and the default handler use, so
// reassigning the exported Runnable variable cannot break them.
var runnableKind = NewKind[Runner, struct{}]("runnable")

// Runnable is the built-in kind of one-shot runners. By default, every root
// container has an implicit StageAfterStart handler for it that calls Run once per
// opted-in service, in dependency then registration order, and stops at the
// first error. Replace it with Runnable.Handle.
//
// Do not reassign Runnable: [AsRunnable] and the default handler keep using
// the original kind.
var Runnable = runnableKind

// AsRunnable opts a registration into the [Runnable] kind. It is shorthand
// for Runnable.With(struct{}{}).
func AsRunnable() ProvideOption {
	return runnableKind.With(struct{}{})
}

// defaultRunnableEntry is the default Runnable handler entry. A container
// without its own Runnable entry runs it in the first StageAfterStart slot,
// so New does not allocate it per container.
var defaultRunnableEntry = runnableKind.newEntry(StageAfterStart, runRunner)

// runRunner is the default Runnable handler: it runs the runner inline.
func runRunner(ctx context.Context, c Component[Runner, struct{}]) error {
	return c.Instance.Run(ctx)
}
