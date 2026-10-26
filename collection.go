package doger

// Collection is a live collection: unlike the snapshot returned by
// [InvokeAll], it resolves its members again on every read, so services
// registered after it was created are included. Create it with
// [InvokeCollection]. A Collection is safe to copy and to read concurrently.
type Collection[T any] struct {
	injector Injector
}

// InvokeCollection returns a live collection of every visible service
// assignable to T, resolved from i (nil means [DefaultRootScope]) on each
// read. It builds nothing itself. When i is a provider's injector, reads
// exclude that provider's service. Reads made while that provider runs
// record its dependency on each member, as InvokeAll does; once it has
// returned, reads resolve from its scope without its invoker chain and
// record no dependency. Providers of that chain still running keep their
// cycle detection.
//
// A read cannot tell which goroutine or build chain it runs in. So a
// goroutine outside the build chain that reads while an outer provider of
// the holder still runs gets ErrCircularDependency for that provider's
// service, and a read from inside a member's provider, by code not on the
// holder's build chain, waits for that member's own build and never returns:
// inside a provider, use [InvokeAll] on its own injector instead.
func InvokeCollection[T any](i Injector) Collection[T] {
	return Collection[T]{injector: getInjectorOrDefault(i)}
}

// All resolves the members now, with the membership, order, error and hook
// rules of [InvokeAll].
func (c Collection[T]) All() ([]T, error) {
	return invokeAllByGenericType[T](c.reader())
}

// Map resolves the members now, keyed by service name, with the rules of
// [InvokeAllMap].
func (c Collection[T]) Map() (map[string]T, error) {
	return invokeAllMapByGenericType[T](c.reader())
}

// reader returns the injector a read resolves from. Once the provider that
// created the collection has returned, its build-time invoker chain is
// stale, so reads use the provider's scope, excluding only the holder.
func (c Collection[T]) reader() Injector {
	vScope, ok := c.injector.(*virtualScope)
	if !ok || !vScope.returned.Load() {
		return c.injector
	}

	holder, ok := vScope.getLastInvokerName()
	if !ok {
		return vScope.self
	}

	// providers above the holder that still run keep their cycle detection
	running := runningChain(vScope)
	if len(running) == 0 {
		return detachedInjector{Injector: vScope.self, holder: holder}
	}

	read := newVirtualScope(vScope.self, running)
	read.name = holder
	read.parent = vScope.parent
	read.detached = true
	return detachedInjector{Injector: read, holder: holder}
}
