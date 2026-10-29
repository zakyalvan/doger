package doger

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestNewServiceDescription checks the creation of a new ServiceDescription.
func TestNewServiceDescription(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	expected := ServiceDescription{"foo", "bar", "baz"}
	actual := newServiceDescription("foo", "bar", "baz")

	is.Equal(expected, actual)
}

// TestNewDAG checks the initialization of a new DAG.
func TestNewDAG(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	dag := newDAG()
	expectedDependencies := map[ServiceDescription]map[ServiceDescription]DependencySource{}
	expectedDependents := map[ServiceDescription]map[ServiceDescription]DependencySource{}

	is.Equal(expectedDependencies, dag.dependencies)
	is.Equal(expectedDependents, dag.dependents)
}

// TestDAG_addDependency checks the addition of dependencies to the DAG.
func TestDAG_addDependency(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	edge1 := newServiceDescription("scope1", "scope1", "service1")
	edge2 := newServiceDescription("scope2", "scope2", "service2")
	edge3 := newServiceDescription("scope3", "scope3", "service3")

	dag := newDAG()

	dag.addDependency("scope1", "scope1", "service1", "scope2", "scope2", "service2", DependencySourceInvoked)

	expectedDependencies := map[ServiceDescription]map[ServiceDescription]DependencySource{edge1: {edge2: DependencySourceInvoked}}
	expectedDependents := map[ServiceDescription]map[ServiceDescription]DependencySource{edge2: {edge1: DependencySourceInvoked}}

	is.Equal(expectedDependencies, dag.dependencies)
	is.Equal(expectedDependents, dag.dependents)

	dag.addDependency("scope3", "scope3", "service3", "scope2", "scope2", "service2", DependencySourceInvoked)

	expectedDependencies = map[ServiceDescription]map[ServiceDescription]DependencySource{edge1: {edge2: DependencySourceInvoked}, edge3: {edge2: DependencySourceInvoked}}
	expectedDependents = map[ServiceDescription]map[ServiceDescription]DependencySource{edge2: {edge1: DependencySourceInvoked, edge3: DependencySourceInvoked}}

	is.Equal(expectedDependencies, dag.dependencies)
	is.Equal(expectedDependents, dag.dependents)
}

// TestDAG_removeService checks the removal of dependencies to the DAG.
func TestDAG_removeService(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	edge1 := newServiceDescription("scope1", "scope1", "service1")
	// edge2 := newServiceDescription("scope2", "scope2", "service2")
	edge3 := newServiceDescription("scope3", "scope3", "service3")

	dag := newDAG()

	dag.addDependency("scope1", "scope1", "service1", "scope2", "scope2", "service2", DependencySourceInvoked)
	dag.addDependency("scope3", "scope3", "service3", "scope2", "scope2", "service2", DependencySourceInvoked)

	dag.removeService("scope2", "scope2", "service2")

	expectedDependencies := map[ServiceDescription]map[ServiceDescription]DependencySource{edge1: {}, edge3: {}}
	expectedDependents := map[ServiceDescription]map[ServiceDescription]DependencySource{}

	is.Equal(expectedDependencies, dag.dependencies)
	is.Equal(expectedDependents, dag.dependents)
}

// TestDAG_explainService checks the explanation of dependencies for a service in the DAG.
func TestDAG_explainService(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	edge1 := newServiceDescription("scope1", "scope1", "service1")
	edge2 := newServiceDescription("scope2", "scope2", "service2")
	edge3 := newServiceDescription("scope3", "scope3", "service3")

	dag := newDAG()
	dag.addDependency("scope1", "scope1", "service1", "scope2", "scope2", "service2", DependencySourceInvoked)
	dag.addDependency("scope3", "scope3", "service3", "scope2", "scope2", "service2", DependencySourceInvoked)

	tests := []struct {
		name             string
		scopeID          string
		scopeName        string
		service          string
		wantDependencies []ServiceDescription
		wantDependents   []ServiceDescription
	}{
		{
			name:    "edge1",
			scopeID: "scope1", scopeName: "scope1", service: "service1",
			wantDependencies: []ServiceDescription{edge2},
			wantDependents:   []ServiceDescription{},
		},
		{
			name:    "edge2",
			scopeID: "scope2", scopeName: "scope2", service: "service2",
			wantDependencies: []ServiceDescription{},
			wantDependents:   []ServiceDescription{edge1, edge3},
		},
		{
			name:    "edge3",
			scopeID: "scope3", scopeName: "scope3", service: "service3",
			wantDependencies: []ServiceDescription{edge2},
			wantDependents:   []ServiceDescription{},
		},
		{
			name:    "not_found",
			scopeID: "scopeX", scopeName: "scopeX", service: "serviceX",
			wantDependencies: []ServiceDescription{},
			wantDependents:   []ServiceDescription{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			a, b := dag.explainService(tt.scopeID, tt.scopeName, tt.service)
			is.ElementsMatch(tt.wantDependencies, a)
			is.ElementsMatch(tt.wantDependents, b)
		})
	}
}
