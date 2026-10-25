package doger

import (
	"fmt"
	"slices"
)

// checkCycles rejects dependency cycles among the candidates, including
// cycles that only exist once providers have run.
func (s *RootScope) checkCycles(candidates []*startCandidate) error {
	const (
		unvisited = iota
		inProgress
		done
	)
	state := map[ServiceDescription]int{}
	var stack []ServiceDescription

	var visit func(node ServiceDescription) []ServiceDescription
	visit = func(node ServiceDescription) []ServiceDescription {
		state[node] = inProgress
		stack = append(stack, node)
		for _, neighbor := range s.dependencyNeighbors(node) {
			switch state[neighbor] {
			case inProgress:
				start := slices.Index(stack, neighbor)
				return append(slices.Clone(stack[start:]), neighbor)
			case unvisited:
				if cycle := visit(neighbor); cycle != nil {
					return cycle
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[node] = done
		return nil
	}

	for _, candidate := range candidates {
		if state[candidate.desc()] != unvisited {
			continue
		}
		if cycle := visit(candidate.desc()); cycle != nil {
			names := make([]string, 0, len(cycle))
			for _, desc := range cycle {
				names = append(names, desc.Service)
			}
			return fmt.Errorf("DI: cannot start: %w: %s", ErrCircularDependency, humanReadableInvokerChain(names))
		}
	}

	return nil
}

// orderByDependencies returns candidates in dependency order: a candidate
// comes after every other candidate it reaches through dependency edges,
// directly or transitively. Ties are broken by the input order (scope depth,
// then registration order).
func (s *RootScope) orderByDependencies(candidates []*startCandidate) []*startCandidate {
	inSet := map[ServiceDescription]bool{}
	for _, candidate := range candidates {
		inSet[candidate.desc()] = true
	}

	// the candidates each candidate reaches, directly or transitively
	requires := map[ServiceDescription][]ServiceDescription{}
	for _, starter := range candidates {
		from := starter.desc()
		visited := map[ServiceDescription]bool{from: true}
		queue := []ServiceDescription{from}
		for len(queue) > 0 {
			node := queue[0]
			queue = queue[1:]
			for _, neighbor := range s.dependencyNeighbors(node) {
				if visited[neighbor] {
					continue
				}
				visited[neighbor] = true
				queue = append(queue, neighbor)
				if inSet[neighbor] {
					requires[from] = append(requires[from], neighbor)
				}
			}
		}
	}

	ordered := make([]*startCandidate, 0, len(candidates))
	emitted := map[ServiceDescription]bool{}
	remaining := candidates
	for len(remaining) > 0 {
		ready := slices.IndexFunc(remaining, func(c *startCandidate) bool {
			return !slices.ContainsFunc(requires[c.desc()], func(d ServiceDescription) bool { return !emitted[d] })
		})
		if ready < 0 {
			// defensive: cycles are rejected before ordering
			return append(ordered, remaining...)
		}

		ordered = append(ordered, remaining[ready])
		emitted[remaining[ready].desc()] = true
		remaining = slices.Delete(slices.Clone(remaining), ready, ready+1)
	}

	return ordered
}
