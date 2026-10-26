package doger

import (
	"cmp"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"unsafe"

	typetostring "github.com/samber/go-type-to-string"
)

// invokeAnyByName retrieves and instantiates a service by name, returning it as any.
// This function handles circular dependency detection, service resolution, and invocation hooks.
//
// Parameters:
//   - i: The injector to search for the service
//   - name: The name of the service to invoke
//
// Returns the service instance as any and any error that occurred during invocation.
//
// This function is used internally by the DI container for service resolution and
// supports virtual scopes for dependency tracking and circular dependency detection.
func invokeAnyByName(i Injector, name string) (any, error) {
	return invokeAnyByNameAs(i, name, DependencySourceInvoked)
}

// invokeAnyByNameAs is invokeAnyByName recording the dependency edge, when
// invoked from a provider, with the given source.
func invokeAnyByNameAs(i Injector, name string, source DependencySource) (any, error) {
	injector := getInjectorOrDefault(i)

	vScope, isVirtualScope := injector.(*virtualScope)
	if isVirtualScope {
		if err := vScope.detectCircularDependency(name); err != nil {
			return nil, err
		}
	}

	serviceAny, serviceScope, found := injector.serviceGetRec(name)
	if !found {
		return nil, serviceNotFound(injector, ErrServiceNotFound, chainTo(vScope, name))
	}

	if isVirtualScope {
		vScope.addDependency(injector, name, serviceScope, source)
	}

	service, ok := serviceAny.(serviceWrapperAny)
	if !ok {
		return nil, serviceNotFound(injector, ErrServiceNotFound, chainTo(vScope, name))
	}

	injector.RootScope().opts.onBeforeInvocation(serviceScope, name)
	child := newChildVirtualScope(vScope, serviceScope, name)
	instance, err := service.getInstanceAny(child)
	injector.RootScope().opts.onAfterInvocation(serviceScope, name, err)
	if err != nil {
		return nil, err
	}

	serviceScope.onServiceInvoke(name)

	injector.RootScope().opts.Logf("DI: service %s invoked", name)

	return instance, nil
}

// invokeByName retrieves and instantiates a service by name with type safety.
// This function handles circular dependency detection, service resolution, type checking,
// and invocation hooks.
//
// Parameters:
//   - i: The injector to search for the service
//   - name: The name of the service to invoke
//
// Returns the service instance with the correct type and any error that occurred during invocation.
//
// This function is used internally by the DI container for type-safe service resolution.
// It ensures that the returned service matches the expected type T.
func invokeByName[T any](i Injector, name string) (T, error) {
	injector := getInjectorOrDefault(i)

	vScope, isVirtualScope := injector.(*virtualScope)
	if isVirtualScope {
		if err := vScope.detectCircularDependency(name); err != nil {
			return empty[T](), err
		}
	}

	serviceAny, serviceScope, found := injector.serviceGetRec(name)
	if !found {
		return empty[T](), serviceNotFound(injector, ErrServiceNotFound, chainTo(vScope, name))
	}

	if isVirtualScope {
		vScope.addDependency(injector, name, serviceScope, DependencySourceInvoked)
	}

	service, ok := serviceAny.(serviceWrapper[T])
	if !ok {
		return empty[T](), serviceTypeMismatch(inferServiceName[T](), serviceAny.(serviceWrapperAny).getTypeName()) //nolint:errcheck,forcetypeassert
	}

	injector.RootScope().opts.onBeforeInvocation(serviceScope, name)
	child := newChildVirtualScope(vScope, serviceScope, name)
	instance, err := service.getInstance(child)
	injector.RootScope().opts.onAfterInvocation(serviceScope, name, err)

	if err != nil {
		return empty[T](), err
	}

	serviceScope.onServiceInvoke(name)

	injector.RootScope().opts.Logf("DI: service %s invoked", name)

	return instance, nil
}

// invokeByGenericType looks for a service by its type and invokes it.
// When multiple services match the provided type or interface, it selects
// the one in the nearest scope, then the earliest registration, then the
// smallest name (see selectByType). This function is useful for
// interface-based dependency injection.
//
// Parameters:
//   - i: The injector to search for the service
//
// Returns the service instance with the correct type and any error that occurred during invocation.
func invokeByGenericType[T any](i Injector) (T, error) {
	injector := getInjectorOrDefault(i)
	serviceAliasName := inferServiceName[T]()

	vScope, _ := injector.(*virtualScope)

	selected, ok := selectByType(injector, reflect.TypeFor[T]())
	if !ok {
		return empty[T](), serviceNotFound(injector, ErrServiceNotMatch, chainTo(vScope, serviceAliasName))
	}

	instance, err := invokeServiceInstance(injector, vScope, serviceAliasName, selected.name, selected.scope, selected.service)
	if err != nil {
		return empty[T](), err
	}

	return instance.(T), nil //nolint:errcheck,forcetypeassert
}

// invokeServiceInstance invokes a service already selected by type. Hooks
// and the log line report hookName; cycle detection, the invoker chain, the
// dependency edge and invocation tracking use realName, because using a
// type name there would break cycle detection. vScope is nil outside a
// provider. The edge is recorded only after a successful build.
func invokeServiceInstance(injector Injector, vScope *virtualScope, hookName, realName string, scope *Scope, service any) (any, error) {
	if vScope != nil {
		if err := vScope.detectCircularDependency(realName); err != nil {
			return nil, err
		}
	}

	injector.RootScope().opts.onBeforeInvocation(scope, hookName)
	child := newChildVirtualScope(vScope, scope, realName)
	instance, err := service.(serviceWrapperGetInstanceAny).getInstanceAny(child) //nolint:errcheck,forcetypeassert
	injector.RootScope().opts.onAfterInvocation(scope, hookName, err)
	if err != nil {
		return nil, err
	}

	if vScope != nil {
		vScope.addDependency(injector, realName, scope, DependencySourceInvoked)
	}

	scope.onServiceInvoke(realName)

	injector.RootScope().opts.Logf("DI: service %s invoked", hookName)

	return instance, nil
}

// collectionSource is where a collection read finds and builds its members.
type collectionSource struct {
	from   Injector      // the members are the services visible from it
	reader *virtualScope // the reading provider's scope, or nil: its build chain, self-exclusion and edges apply
	holder string        // excluded too, when not empty: the service holding a live collection
}

// collectionFrom returns the source of a collection invoked from injector:
// visible from it, and built through it.
func collectionFrom(injector Injector) collectionSource {
	reader, _ := injector.(*virtualScope)
	return collectionSource{from: injector, reader: reader}
}

// invokeAllByGenericType invokes every collection member for T, in
// collection order.
func invokeAllByGenericType[T any](source collectionSource) ([]T, error) {
	members, err := invokeCollection(source, reflect.TypeFor[T](), true)
	if err != nil {
		return nil, err
	}

	result := make([]T, 0, len(members))
	for _, m := range members {
		typed, _ := m.instance.(T)
		result = append(result, typed)
	}

	return result, nil
}

// collectionMember is a built member of a collection.
type collectionMember struct {
	candidate collectionCandidate
	instance  any
	order     int // set by the ordering pass of an ordered collection
}

// invokeCollection builds the members of the collection for target from
// source, in default order. When ordered is true, a separate stable pass
// then sorts them by WithOrder or Order(), once every member is built.
func invokeCollection(source collectionSource, target reflect.Type, ordered bool) ([]collectionMember, error) {
	candidates := collectionCandidatesFor(source, target)
	members := make([]collectionMember, 0, len(candidates))
	err := buildMembers(source, candidates, func(candidate collectionCandidate, instance any) {
		members = append(members, collectionMember{candidate: candidate, instance: instance})
	})
	if err != nil {
		return nil, err
	}

	if !ordered {
		return members, nil
	}

	for index := range members {
		order, err := orderValue(members[index].candidate.name, members[index].instance, members[index].candidate.options())
		if err != nil {
			return nil, err
		}
		members[index].order = order
	}
	slices.SortStableFunc(members, func(a, b collectionMember) int { return cmp.Compare(a.order, b.order) })

	return members, nil
}

// orderValue returns the collection order of a built member: its WithOrder
// value when set (Order() is then not called), otherwise its Order() when it
// implements Ordered, otherwise 0.
// A panicking Order() is reported as an error naming the member, wrapping
// the panic value when it is an error.
func orderValue(name string, instance any, opts *serviceOptions) (order int, err error) {
	if opts != nil && opts.order != nil {
		return *opts.order, nil
	}

	ordered, ok := instance.(Ordered)
	if !ok {
		return 0, nil
	}

	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok {
				err = fmt.Errorf("DI: Order() of collection member `%s` panicked: %w", name, e)
			} else {
				err = fmt.Errorf("DI: Order() of collection member `%s` panicked: %v", name, r)
			}
		}
	}()

	return ordered.Order(), nil
}

// invokeAllMapByGenericType invokes every collection member for T, keyed
// by name. Order() is never called.
func invokeAllMapByGenericType[T any](source collectionSource) (map[string]T, error) {
	members, err := invokeCollection(source, reflect.TypeFor[T](), false)
	if err != nil {
		return nil, err
	}

	result := make(map[string]T, len(members))
	for _, m := range members {
		typed, _ := m.instance.(T)
		result[m.candidate.name] = typed
	}

	return result, nil
}

// buildMembers builds the candidates in order, through the reader when
// there is one, and passes each member to add. It stops at the first
// failure, naming the member.
func buildMembers(source collectionSource, candidates []collectionCandidate, add func(candidate collectionCandidate, instance any)) error {
	injector := source.from
	if source.reader != nil {
		injector = source.reader
	}

	for _, candidate := range candidates {
		instance, err := invokeServiceInstance(injector, source.reader, candidate.name, candidate.name, candidate.scope, candidate.service)
		if err != nil {
			return wrapMemberError(candidate.name, err)
		}

		add(candidate, instance)
	}

	return nil
}

// options returns the registration options of the candidate, or nil.
func (c collectionCandidate) options() *serviceOptions {
	if service, ok := c.service.(serviceWrapperAny); ok {
		return service.getOptions()
	}

	return nil
}

// wrapMemberError names the collection member whose build failed.
func wrapMemberError(name string, err error) error {
	return fmt.Errorf("DI: could not invoke collection member `%s`: %w", name, err)
}

// collectionCandidate is a member of a collection, before it is built.
type collectionCandidate struct {
	placement
	scope   *Scope
	service any
}

// placement is where a service sits in the default order of collections
// and Start: ancestors first, then registration order.
type placement struct {
	name     string
	depth    int    // scope depth from the root scope (root = 0)
	regIndex uint64 // registration index, unique per root container
}

// compare orders p before o by scope depth, then registration index; the
// name keeps the order total even if indexes ever collide.
func (p placement) compare(o placement) int {
	return cmp.Or(cmp.Compare(p.depth, o.depth), cmp.Compare(p.regIndex, o.regIndex), cmp.Compare(p.name, o.name))
}

// collectionCandidatesFor returns the members of a collection for target,
// visible from source: the nearest service of each name (even when it is
// not assignable to target, it shadows its ancestors), without aliases,
// without the service whose provider reads the collection and without the
// holder, whose registered type is assignable to target.
func collectionCandidatesFor(source collectionSource, target reflect.Type) []collectionCandidate {
	candidates := []collectionCandidate{}
	seen := map[string]struct{}{}

	// the service whose provider reads the collection is not a member
	if source.reader != nil {
		if invoker, ok := source.reader.getLastInvokerName(); ok {
			seen[invoker] = struct{}{}
		}
	}
	if source.holder != "" {
		seen[source.holder] = struct{}{}
	}

	source.from.serviceForEachRec(func(name string, scope *Scope, s any) bool {
		if _, ok := seen[name]; ok {
			return true
		}
		seen[name] = struct{}{}

		service, ok := s.(serviceWrapperAny)
		if !ok || service.getServiceType() == ServiceTypeAlias || !serviceCanCastToType(s, target) {
			return true
		}

		candidates = append(candidates, collectionCandidate{
			name:     name,
			depth:    scopeDepth(scope),
			regIndex: service.getRegistrationIndex(),
			scope:    scope,
			service:  s,
		})
		return true
	})

	slices.SortFunc(candidates, func(a, b collectionCandidate) int { return a.compare(b.placement) })

	return candidates
}

// invokingServiceName returns the name of the service whose provider
// received injector, when injector is a provider's virtual scope.
func invokingServiceName(injector Injector) (string, bool) {
	if vScope, ok := injector.(*virtualScope); ok {
		return vScope.getLastInvokerName()
	}

	return "", false
}

// selectByType returns the visible service assignable to target, aliases
// included, in the nearest scope (the invoking scope before its ancestors),
// then with the earliest registration, then with the smallest name. A
// service hidden by a same-name service in a nearer scope is skipped, as for
// collections, and so is the service whose provider invokes it, so that a
// decorator resolves the service it wraps. This is nearest-first, the
// opposite of collection order, on purpose: it keeps the "current scope
// first" behavior of InvokeAs.
func selectByType(injector Injector, target reflect.Type) (collectionCandidate, bool) {
	from := injectorScope(injector)
	invoker, fromProvider := invokingServiceName(injector)

	// nearest scope first: once a scope has a match, a farther scope cannot
	// win, so the walk stops there
	for scope := from; scope != nil; scope = scope.parentScope {
		var selected collectionCandidate
		var found bool

		for _, match := range scope.matchesFor(target) {
			if fromProvider && match.name == invoker {
				continue
			}

			// an alias of the invoking service would resolve to it
			if fromProvider && aliasResolvesTo(match.service, invoker) {
				continue
			}

			// a nearer service of the same name hides this one, as for collections
			if shadowedBetween(from, scope, match.name) {
				continue
			}

			// one scope at a time, so the depth never decides
			candidate := placement{name: match.name, regIndex: match.regIndex}
			if !found || candidate.compare(selected.placement) < 0 {
				selected = collectionCandidate{placement: candidate, scope: scope, service: match.service}
				found = true
			}
		}

		if found {
			return selected, true
		}
	}

	return collectionCandidate{}, false
}

// maxAliasHops bounds the alias chain aliasResolvesTo follows: a longer
// chain is a loop.
const maxAliasHops = 64

// aliasResolvesTo reports whether service is an alias whose target chain
// reaches a service named name, following each target from its alias's
// injector, as invoking the alias does.
func aliasResolvesTo(service any, name string) bool {
	for range maxAliasHops {
		alias, ok := service.(serviceWrapperAliasTarget)
		if !ok {
			return false
		}

		injector, target := alias.aliasTarget()
		if target == name {
			return true
		}

		next, _, found := injector.serviceGetRec(target)
		if !found {
			return false
		}
		service = next
	}

	return false
}

// shadowedBetween reports whether a scope from from up to, but not
// including, its ancestor to registers name, hiding to's service.
func shadowedBetween(from, to *Scope, name string) bool {
	for scope := from; scope != nil && scope != to; scope = scope.parentScope {
		if scope.serviceExist(name) {
			return true
		}
	}

	return false
}

// scopeDepth returns the number of ancestors of scope, 0 for nil.
func scopeDepth(scope *Scope) int {
	if scope == nil {
		return 0
	}

	depth := 0
	for p := scope.parentScope; p != nil; p = p.parentScope {
		depth++
	}
	return depth
}

// invokeByTag injects services into struct fields based on struct tags.
// This function supports automatic dependency injection into struct fields
// using the `do` tag or a custom tag key specified in the injector options.
// If `implicitAliasing` is true and a service is not found by tag, the injector
// will fall back to searching for a service by the field's generic type, like `doger.InvokeAs[T]`.
//
// Parameters:
//   - i: The injector to search for services
//   - structName: The name of the struct for error reporting
//   - structValue: A reflect.Value pointing to the struct to inject into
//   - implicitAliasing: Whether to fall back to generic type if service is not found by name
//
// Returns an error if injection fails for any reason.
//
// The function does not manipulate virtual scope because it is done by invokeAnyByName or invokeByGenericType.
//
// The implicit-aliasing fallback selects among matching services like invokeByGenericType.
func invokeByTags(i Injector, structName string, structValue reflect.Value, implicitAliasing bool) error { //nolint:gocyclo
	injector := getInjectorOrDefault(i)

	// Ensure that servicePtr is a pointer to a struct
	if structValue.Kind() != reflect.Pointer || structValue.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("DI: must be a pointer to a struct")
	}

	structValue = structValue.Elem()

	// Iterate through the fields of the struct
	for i := 0; i < structValue.NumField(); i++ {
		field := structValue.Type().Field(i)
		fieldValue := structValue.Field(i)

		serviceName, ok := field.Tag.Lookup(coalesce(injector.RootScope().opts.StructTagKey, DefaultStructTagKey))
		if !ok {
			continue
		}

		// Keep track if tag was provided without an explicit name (eg: `do:""`)
		wasTagNameEmpty := serviceName == ""

		if !fieldValue.CanAddr() {
			return fmt.Errorf("DI: field is not addressable `%s.%s`", structName, field.Name)
		}

		if !fieldValue.CanSet() {
			// When a field is not exported, we override it.
			// See https://stackoverflow.com/questions/42664837/how-to-access-unexported-struct-fields/43918797#43918797
			// bearer:disable go_gosec_unsafe_unsafe
			fieldValue = reflect.NewAt(fieldValue.Type(), unsafe.Pointer(fieldValue.UnsafeAddr())).Elem()
		}

		if serviceName == "*" {
			if err := injectCollectionField(injector, structName, field, fieldValue); err != nil {
				return err
			}
			continue
		}

		if serviceName == "" {
			serviceName = typetostring.GetReflectValueType(fieldValue)
		}

		name := serviceName
		if implicitAliasing && wasTagNameEmpty && !namedCandidate(injector, serviceName) {
			// Fallback: resolve by the field's type when no service other than
			// the invoking one has the type's name; with no match, the named
			// lookup reports why
			if selected, found := selectByType(injector, fieldValue.Type()); found {
				name = selected.name
			}
		}
		dependency, err := invokeAnyByName(injector, name)
		if err != nil {
			return err
		}

		dependencyValue := reflect.ValueOf(dependency)

		// Should be checked before invocation, because we just built something that is not assignable to the field.
		if !dependencyValue.Type().AssignableTo(fieldValue.Type()) {
			return fmt.Errorf("DI: `%s` is not assignable to field `%s.%s`", serviceName, structName, field.Name)
		}

		// Should not panic, since we checked CanAddr() and CanSet() earlier.
		fieldValue.Set(dependencyValue)
	}

	return nil
}

// namedCandidate reports whether a visible service named name, other than
// the one whose provider invokes from injector, can fill a field by name.
func namedCandidate(injector Injector, name string) bool {
	if invoker, ok := invokingServiceName(injector); ok && invoker == name {
		return false
	}

	return injector.serviceExistRec(name)
}

// injectCollectionField sets a field tagged `*`: a []E field to the members
// of InvokeAll[E], a map[string]E field to those of InvokeAllMap[E].
func injectCollectionField(injector Injector, structName string, field reflect.StructField, fieldValue reflect.Value) error {
	fieldType := fieldValue.Type()

	switch {
	case fieldType.Kind() == reflect.Slice:
		members, err := invokeCollection(collectionFrom(injector), fieldType.Elem(), true)
		if err != nil {
			return err
		}

		slice := reflect.MakeSlice(fieldType, 0, len(members))
		for _, m := range members {
			slice = reflect.Append(slice, memberValue(m.instance, fieldType.Elem()))
		}
		fieldValue.Set(slice)
	case fieldType.Kind() == reflect.Map && fieldType.Key() == reflect.TypeFor[string]():
		members, err := invokeCollection(collectionFrom(injector), fieldType.Elem(), false)
		if err != nil {
			return err
		}

		collection := reflect.MakeMapWithSize(fieldType, len(members))
		for _, m := range members {
			collection.SetMapIndex(reflect.ValueOf(m.candidate.name), memberValue(m.instance, fieldType.Elem()))
		}
		fieldValue.Set(collection)
	default:
		return fmt.Errorf("DI: tag `*` requires a slice or map[string] field, got `%s.%s`", structName, field.Name)
	}

	return nil
}

// memberValue returns instance as a value of type elem: the zero value when
// instance is a nil interface.
func memberValue(instance any, elem reflect.Type) reflect.Value {
	if instance == nil {
		return reflect.Zero(elem)
	}

	return reflect.ValueOf(instance)
}

// serviceNotFound returns a detailed error indicating that the specified service was not found.
// This function provides helpful error messages that include available services and
// the invocation chain for debugging purposes.
func serviceNotFound(injector Injector, err error, chain []string) error {
	name := chain[len(chain)-1]
	services := injector.ListProvidedServices()

	if len(services) == 0 {
		if len(chain) > 1 {
			return fmt.Errorf("%w `%s`, no service available, path: %s", err, name, humanReadableInvokerChain(chain))
		}
		return fmt.Errorf("%w `%s`, no service available", err, name)
	}

	serviceNames := getServiceNames(services)
	sortedServiceNames := sortServiceNames(serviceNames)

	if len(chain) > 1 {
		return fmt.Errorf(
			"%w `%s`, available services: %s, path: %s",
			err,
			name,
			strings.Join(sortedServiceNames, ", "),
			humanReadableInvokerChain(chain),
		)
	}
	return fmt.Errorf("%w `%s`, available services: %s", err, name, strings.Join(sortedServiceNames, ", "))
}

// serviceTypeMismatch returns an error indicating that the specified service was found,
// but its type does not match the expected type. This typically occurs when a service
// is registered with one type but invoked with a different type.
func serviceTypeMismatch(invoking string, registered string) error {
	return fmt.Errorf("DI: service found, but type mismatch: invoking `%s` but registered `%s`", invoking, registered)
}

// getServiceNames formats a list of ServiceDescription names for error reporting.
// This function converts ServiceDescription objects to formatted string names
// that can be displayed in error messages.
func getServiceNames(services []ServiceDescription) []string {
	return mAp(services, func(desc ServiceDescription, _ int) string {
		return fmt.Sprintf("`%s`", desc.Service)
	})
}

// sortServiceNames sorts a list of service names alphabetically.
// This function ensures consistent ordering of service names in error messages
// and other output, making them easier to read and compare.
func sortServiceNames(names []string) []string {
	sort.Strings(names)
	return names
}

// humanReadableInvokerChain formats an invocation chain into a human-readable string.
// This function converts a slice of service names into a formatted string
// that shows the dependency chain for debugging purposes.
//
// This is useful for debugging circular dependencies and understanding
// the service resolution path that led to an error.
func humanReadableInvokerChain(invokerChain []string) string {
	invokerChain = mAp(invokerChain, func(item string, _ int) string {
		return fmt.Sprintf("`%s`", item)
	})
	return strings.Join(invokerChain, " -> ")
}

func handleProviderPanic[T any](provider Provider[T], i Injector) (svc T, err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("DI: %v", r)
			}
		}
	}()

	_svc, _err := provider(i)

	// do not return svc when err != nil
	if _err != nil {
		err = _err
	} else {
		svc = _svc
	}

	return svc, err
}
