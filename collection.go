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
// exclude that provider's service and record its dependency on each member,
// as InvokeAll does.
func InvokeCollection[T any](i Injector) Collection[T] {
	return Collection[T]{injector: getInjectorOrDefault(i)}
}

// All resolves the members now, with the membership, order, error and hook
// rules of [InvokeAll].
func (c Collection[T]) All() ([]T, error) {
	return invokeAllByGenericType[T](c.injector)
}

// Map resolves the members now, keyed by service name, with the rules of
// [InvokeAllMap].
func (c Collection[T]) Map() (map[string]T, error) {
	return invokeAllMapByGenericType[T](c.injector)
}
