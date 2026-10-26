package doger

// Collection is a live collection: unlike the snapshot returned by
// [InvokeAll], it resolves its members again on every read, so services
// registered after it was created are included. Create it with
// [InvokeCollection]. A Collection is safe to copy and to read concurrently.
type Collection[T any] struct {
	scope  *Scope // members resolve from it; nil means DefaultRootScope
	holder string // the service whose provider created the collection, or ""
}

// InvokeCollection returns a live collection of every visible service
// assignable to T, resolved from the scope of i (nil means
// [DefaultRootScope]) on each read. It builds nothing itself. When i is a
// provider's injector, the service that provider builds (the holder) is
// excluded from every read. The collection keeps the holder's scope and
// name, not its provider's build chain.
func InvokeCollection[T any](i Injector) Collection[T] {
	injector := getInjectorOrDefault(i)
	holder, _ := invokingServiceName(injector)

	return Collection[T]{scope: injectorScope(injector), holder: holder}
}

// All resolves the members now, with the membership, order, error and hook
// rules of [InvokeAll]. r is the reader's injector: a provider passes its
// own, so that its service is excluded, its dependency on each member is
// recorded and cycles through its build chain are reported, as [InvokeAll]
// called from r does. Code outside a provider passes nil: the read then uses
// no build chain and records no dependency. The reader is excluded by name,
// as [InvokeAs] skips its invoker: a reader in a child scope that shadows a
// member of the holder's scope with its own name excludes that member too.
func (c Collection[T]) All(r Injector) ([]T, error) {
	return invokeAllByGenericType[T](c.source(r))
}

// Map resolves the members now, keyed by service name, with the rules of
// [InvokeAllMap] and the reader's injector r of [Collection.All].
func (c Collection[T]) Map(r Injector) (map[string]T, error) {
	return invokeAllMapByGenericType[T](c.source(r))
}

// source returns where a read with reader injector r finds and builds the
// members: from the holder's scope, excluding the holder, through r when r
// is a provider's injector.
func (c Collection[T]) source(r Injector) collectionSource {
	var from Injector = DefaultRootScope
	if c.scope != nil {
		from = c.scope
	}
	reader, _ := r.(*virtualScope)

	return collectionSource{from: from, reader: reader, holder: c.holder}
}
