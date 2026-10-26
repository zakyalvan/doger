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
// record no dependency.
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

	return detachedInjector{Injector: vScope.self, holder: holder}
}
