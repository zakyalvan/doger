package doger

import (
	"fmt"
	"sync"
)

// newServiceDescription creates a new ServiceDescription with the provided scope ID, scope name, and service name.
// This function is used internally to create consistent ServiceDescription instances.
//
// Parameters:
//   - scopeID: The unique identifier of the scope
//   - scopeName: The human-readable name of the scope
//   - serviceName: The name of the service
//
// Returns a new ServiceDescription instance.
func newServiceDescription(scopeID string, scopeName string, serviceName string) ServiceDescription {
	return ServiceDescription{
		ScopeID:   scopeID,
		ScopeName: scopeName,
		Service:   serviceName,
	}
}

// ServiceDescription represents a service in the dependency graph (DAG), identified by scope ID, scope name, and service name.
// This type is used to uniquely identify services across the entire scope hierarchy for dependency tracking.
//
// Fields:
//   - ScopeID: The unique identifier of the scope containing the service
//   - ScopeName: The human-readable name of the scope containing the service
//   - Service: The name of the service within the scope
type ServiceDescription struct {
	ScopeID   string
	ScopeName string
	Service   string
}

// DependencySource says how a dependency edge was recorded. When an edge
// was recorded several ways, the first of invoked, depends-on and declared
// is kept.
type DependencySource int

const (
	// DependencySourceInvoked is an edge recorded when a provider invoked
	// the service. It is the zero value.
	DependencySourceInvoked DependencySource = iota
	// DependencySourceDependsOn is an edge from a DependsOn or
	// DependsOnNamed registration option.
	DependencySourceDependsOn
	// DependencySourceDeclared is an edge recorded by DeclareDependency.
	DependencySourceDeclared
)

// String returns "invoked", "depends-on" or "declared", and
// "DependencySource(n)" for an unknown value.
func (s DependencySource) String() string {
	switch s {
	case DependencySourceInvoked:
		return "invoked"
	case DependencySourceDependsOn:
		return "depends-on"
	case DependencySourceDeclared:
		return "declared"
	default:
		return fmt.Sprintf("DependencySource(%d)", int(s))
	}
}

// MarshalText encodes the source as its String form, so JSON shows it by name.
func (s DependencySource) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

// UnmarshalText decodes "invoked", "depends-on" or "declared", so explained
// output read back from JSON keeps its sources. Any other text is an error.
func (s *DependencySource) UnmarshalText(text []byte) error {
	for _, source := range []DependencySource{DependencySourceInvoked, DependencySourceDependsOn, DependencySourceDeclared} {
		if source.String() == string(text) {
			*s = source
			return nil
		}
	}

	return fmt.Errorf("DI: unknown dependency source %q", text)
}

// newDAG creates a new DAG (Directed Acyclic Graph) with initialized dependencies and dependents maps.
// This function initializes a new dependency graph for tracking service relationships.
//
// Returns a new DAG instance ready for dependency tracking.
func newDAG() *DAG {
	d := &DAG{}
	initDAG(d)

	return d
}

// initDAG initializes the zero DAG d in place, so that a RootScope can hold
// its graph in the same allocation.
func initDAG(d *DAG) {
	d.dependencies = map[ServiceDescription]map[ServiceDescription]DependencySource{}
	d.dependents = map[ServiceDescription]map[ServiceDescription]DependencySource{}
}

// DAG represents a Directed Acyclic Graph of services, tracking dependencies and dependents.
// This type manages the relationships between services to ensure proper initialization order
// and detect circular dependencies.
//
// The DAG maintains two maps:
//   - dependencies: Maps each service to the services it depends on
//   - dependents: Maps each service to the services that depend on it
//
// Fields:
//   - mu: Read-write mutex for thread-safe access to the graph
//   - dependencies: Map of services to their dependencies
//   - dependents: Map of services to their dependents
type DAG struct {
	mu           sync.RWMutex
	dependencies map[ServiceDescription]map[ServiceDescription]DependencySource
	dependents   map[ServiceDescription]map[ServiceDescription]DependencySource
}

// addDependency adds a dependency relationship from one service to another in the DAG.
// This function establishes that the 'from' service depends on the 'to' service,
// which affects the order of service initialization and shutdown.
//
// Parameters:
//   - fromScopeID: The scope ID of the dependent service
//   - fromScopeName: The scope name of the dependent service
//   - fromServiceName: The name of the dependent service
//   - toScopeID: The scope ID of the dependency service
//   - toScopeName: The scope name of the dependency service
//   - toServiceName: The name of the dependency service
//
// This function is thread-safe and updates both the dependencies and dependents maps.
// An edge recorded again keeps its strongest source: invoked, then
// depends-on, then declared.
func (d *DAG) addDependency(fromScopeID, fromScopeName, fromServiceName, toScopeID, toScopeName, toServiceName string, source DependencySource) {
	from := newServiceDescription(fromScopeID, fromScopeName, fromServiceName)
	to := newServiceDescription(toScopeID, toScopeName, toServiceName)

	d.mu.Lock()
	defer d.mu.Unlock()

	// from -> to
	dependencies, ok := d.dependencies[from]
	if !ok {
		dependencies = map[ServiceDescription]DependencySource{}
		d.dependencies[from] = dependencies
	}
	if current, ok := dependencies[to]; ok {
		source = min(source, current)
	}
	dependencies[to] = source

	// from <- to
	dependents, ok := d.dependents[to]
	if !ok {
		dependents = map[ServiceDescription]DependencySource{}
		d.dependents[to] = dependents
	}
	dependents[from] = source
}

// edgeSource returns the source of the edge from -> to, and whether it exists.
func (d *DAG) edgeSource(from, to ServiceDescription) (DependencySource, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	source, ok := d.dependencies[from][to]
	return source, ok
}

// removeService removes a dependency relationship between services in the DAG.
// This function is called when a service is being removed from the container,
// and it cleans up all dependency relationships involving that service.
//
// Parameters:
//   - scopeID: The scope ID of the service to remove
//   - scopeName: The scope name of the service to remove
//   - serviceName: The name of the service to remove
//
// This function removes the service from both dependencies and dependents maps,
// ensuring the graph remains consistent.
func (d *DAG) removeService(scopeID, scopeName, serviceName string) {
	desc := newServiceDescription(scopeID, scopeName, serviceName)

	d.mu.Lock()
	defer d.mu.Unlock()

	dependencies, dependents := d.explainServiceUnsafe(desc)

	for _, dependency := range dependencies {
		delete(d.dependents[dependency], desc)
	}

	// should be empty, because we remove dependencies in the inverse invocation order
	for _, dependent := range dependents {
		delete(d.dependencies[dependent], desc)
	}

	delete(d.dependencies, desc)
	delete(d.dependents, desc)
}

// listServicesHavingNoDependent returns the subset of the provided service names
// (all belonging to the scope identified by scopeID/scopeName) that have no
// dependents registered in the graph. It takes a single read lock for the
// whole batch and does not allocate per-service dependency lists, unlike
// repeated explainService calls.
func (d *DAG) listServicesHavingNoDependent(scopeID, scopeName string, serviceNames []string) []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	out := make([]string, 0, len(serviceNames))
	for _, name := range serviceNames {
		desc := newServiceDescription(scopeID, scopeName, name)
		if len(d.dependents[desc]) == 0 {
			out = append(out, name)
		}
	}

	return out
}

// listDependentsWithin returns the services of the scope identified by
// scopeID/scopeName that are among serviceNames, or have a dependency path,
// through any scope, to one of them. It takes a single read lock.
func (d *DAG) listDependentsWithin(scopeID, scopeName string, serviceNames []string) []string {
	if len(serviceNames) == 0 {
		return []string{}
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	seen := map[ServiceDescription]struct{}{}
	queue := make([]ServiceDescription, 0, len(serviceNames))
	for _, name := range serviceNames {
		desc := newServiceDescription(scopeID, scopeName, name)
		seen[desc] = struct{}{}
		queue = append(queue, desc)
	}

	out := make([]string, 0, len(serviceNames))
	for len(queue) > 0 {
		desc := queue[0]
		queue = queue[1:]
		if desc.ScopeID == scopeID {
			out = append(out, desc.Service)
		}
		for dependent := range d.dependents[desc] {
			if _, ok := seen[dependent]; !ok {
				seen[dependent] = struct{}{}
				queue = append(queue, dependent)
			}
		}
	}

	return out
}

// explainService provides information about a service's dependencies and dependents in the DAG.
// This function returns the list of services that the specified service depends on,
// as well as the list of services that depend on the specified service.
//
// Parameters:
//   - scopeID: The scope ID of the service to explain
//   - scopeName: The scope name of the service to explain
//   - serviceName: The name of the service to explain
//
// Returns two slices:
//   - dependencies: Services that the specified service depends on
//   - dependents: Services that depend on the specified service
//
// This function is thread-safe and provides read-only access to the dependency graph.
func (d *DAG) explainService(scopeID, scopeName, serviceName string) (dependencies, dependents []ServiceDescription) {
	desc := newServiceDescription(scopeID, scopeName, serviceName)

	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.explainServiceUnsafe(desc)
}

// explainServiceUnsafe is the internal implementation of explainService.
// This function performs the actual work of retrieving dependency information
// without acquiring locks (assumes the caller has already acquired appropriate locks).
//
// Parameters:
//   - desc: The ServiceDescription to explain
//
// Returns two slices:
//   - dependencies: Services that the specified service depends on
//   - dependents: Services that depend on the specified service
func (d *DAG) explainServiceUnsafe(desc ServiceDescription) (dependencies, dependents []ServiceDescription) {
	dependencies, dependents = []ServiceDescription{}, []ServiceDescription{}

	if kv, ok := d.dependencies[desc]; ok {
		dependencies = keys(kv)
	}

	if kv, ok := d.dependents[desc]; ok {
		dependents = keys(kv)
	}

	return dependencies, dependents
}
