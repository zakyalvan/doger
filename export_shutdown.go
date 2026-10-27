package doger

import (
	"context"
	"slices"
	"sync"
	"time"
)

// exportPass shuts down a scope holding exports: the services of the scope
// and of its exporting descendants (children exporting into their parent,
// recursively) in one dependency-ordered pass, whatever scope each belongs to.
type exportPass struct {
	ctx    context.Context
	root   *passScope   // the scope shut down
	scopes []*passScope // root first, each scope before its exporting children
}

// passScope is a scope taking part in an export pass.
type passScope struct {
	scope     *Scope
	parent    *passScope          // nil for the root of the pass
	children  []*passScope        // its exporting children shut down by the pass
	exporting map[*Scope]struct{} // its exporting children, including those shut down elsewhere
	run       *scopeCloseRun      // the shutdown of the scope the pass runs; nil for the root
	start     time.Time           // when the pass joined the scope
	waitErr   error               // waiting for a starting run failed
	reports   []*ShutdownReport   // its part of the report so far
	report    *ShutdownReport     // its full report, once finished
	opened    bool                // its HookScopeClosing ran
	finished  bool                // its HookScopeClosed ran and its shutdown was released
}

// shutdownWithExports shuts down a scope holding exports: the children that
// export nothing (of the scope and of every exporting descendant) first, then
// one pass where a service shuts down once no service still running in the
// pass depends on it. The scope's own hooks bracket the pass; an exporting
// descendant closes inside it, its HookScopeClosing just before its first
// service shuts down and its HookScopeClosed once its services and its
// exporting descendants are done.
func (s *Scope) shutdownWithExports(ctx context.Context, first *hookPanic, isChild bool) *ShutdownReport {
	// first is passed to the methods running hooks, never stored in the
	// pass: the caller's panic record then stays off the heap
	pass := &exportPass{ctx: ctx}
	pass.root = pass.add(s, nil, nil)
	defer pass.release()

	pass.join(pass.root)
	pass.shutdownOtherChildren(first)

	if isChild {
		s.runScopeObserverHooks(ctx, "scope-closing", s.rootScope.opts.HookScopeClosing, first)
	}

	pass.shutdownServices(first)
	s.logf("shut down services")

	if isChild {
		s.runScopeObserverHooks(ctx, "scope-closed", s.rootScope.opts.HookScopeClosed, first)
	}

	return pass.root.mergedReport()
}

// add records scope in the pass, as an exporting child of parent whose
// shutdown run the pass runs, or as the root when parent is nil.
func (p *exportPass) add(scope *Scope, parent *passScope, run *scopeCloseRun) *passScope {
	member := &passScope{
		scope:     scope,
		parent:    parent,
		exporting: map[*Scope]struct{}{},
		run:       run,
		start:     scope.clock().Now(),
	}
	if parent != nil {
		parent.children = append(parent.children, member)
	}
	p.scopes = append(p.scopes, member)

	return member
}

// join adds the exporting children of member to the pass, recursively. Each
// takes part in the child-scope shutdown protocol: when its shutdown is
// already running elsewhere, the pass waits for it, bounded by its context,
// and leaves the child and its descendants out.
func (p *exportPass) join(member *passScope) {
	for _, child := range member.scope.exportingChildren() {
		member.exporting[child] = struct{}{}

		run, winner := child.joinClose()
		if !winner {
			member.reports = append(member.reports, child.waitForScopeClose(p.ctx, run))
			child.endClosing()
			member.scope.removeChild(child)
			continue
		}

		joined := p.add(child, member, run)
		joined.waitErr = child.waitForStartingRun(p.ctx)
		p.join(joined)
	}
}

// release ends the shutdowns of the joined scopes a panic left unfinished.
func (p *exportPass) release() {
	for _, member := range p.scopes {
		if member.run != nil && !member.finished {
			member.finished = true
			member.scope.finishScopeClose(member.run, nil)
			member.scope.endClosing()
		}
	}
}

// shutdownOtherChildren shuts down, in parallel, the children of every scope
// of the pass that export nothing into it.
func (p *exportPass) shutdownOtherChildren(first *hookPanic) {
	reports := make([]*ShutdownReport, len(p.scopes))
	panics := make([]hookPanic, len(p.scopes))

	ctx := p.ctx // captured instead of p, so the pass stays off the heap
	var wg sync.WaitGroup
	for index, member := range p.scopes {
		wg.Go(func() {
			reports[index] = member.scope.shutdownChildrenInParallel(ctx, &panics[index], func(child *Scope) bool {
				_, isExporting := member.exporting[child]
				return !isExporting
			})
		})
	}
	wg.Wait()

	for index, member := range p.scopes {
		member.reports = append(member.reports, reports[index])
		member.scope.rootScope.keepFirstPanic(first, panics[index])
	}
}

// shutdownServices shuts down the services of the scopes of the pass, in
// rounds: a service shuts down once no service still running in the pass
// depends on it, according to the dependency edges among them before the
// pass. A true cycle shuts the remaining services down in parallel.
func (p *exportPass) shutdownServices(first *hookPanic) {
	alive := p.aliveServices()
	dependents := p.root.scope.rootScope.dag.snapshotDependents(serviceSet(alive))

	for ; len(alive) > 0; alive = p.aliveServices() {
		ready := readyServices(alive, dependents)
		p.open(ready, first)
		p.shutdownRound(ready)
		p.settle(first)
	}

	p.settle(first)
}

// readyServices returns the services of alive that no service of alive
// depends on, per scope of the pass, or all of them when there is none: a
// cycle shuts everything left down, without taking care of order.
func readyServices(alive map[*passScope][]ServiceDescription, dependents map[ServiceDescription][]ServiceDescription) map[*passScope][]string {
	running := serviceSet(alive)

	ready := map[*passScope][]string{}
	for member, descs := range alive {
		for _, desc := range descs {
			if !hasRunningDependent(dependents[desc], running) {
				ready[member] = append(ready[member], desc.Service)
			}
		}
	}
	if len(ready) == 0 {
		for member, descs := range alive {
			for _, desc := range descs {
				ready[member] = append(ready[member], desc.Service)
			}
		}
	}

	return ready
}

// serviceSet returns the services of alive as a set.
func serviceSet(alive map[*passScope][]ServiceDescription) map[ServiceDescription]struct{} {
	set := map[ServiceDescription]struct{}{}
	for _, descs := range alive {
		for _, desc := range descs {
			set[desc] = struct{}{}
		}
	}

	return set
}

// hasRunningDependent reports whether one of dependents is running.
func hasRunningDependent(dependents []ServiceDescription, running map[ServiceDescription]struct{}) bool {
	for _, dependent := range dependents {
		if _, ok := running[dependent]; ok {
			return true
		}
	}

	return false
}

// aliveServices returns the services left in each scope of the pass.
func (p *exportPass) aliveServices() map[*passScope][]ServiceDescription {
	alive := map[*passScope][]ServiceDescription{}
	for _, member := range p.scopes {
		scope := member.scope
		scope.mu.RLock()
		for name := range scope.services {
			alive[member] = append(alive[member], newServiceDescription(scope.id, scope.name, name))
		}
		scope.mu.RUnlock()
	}

	return alive
}

// open runs the HookScopeClosing hooks of the exporting descendants with a
// service in ready that did not run them yet, parents first.
func (p *exportPass) open(ready map[*passScope][]string, first *hookPanic) {
	for _, member := range p.scopes {
		if member.run != nil && !member.opened && len(ready[member]) > 0 {
			member.opened = true
			member.scope.runScopeObserverHooks(p.ctx, "scope-closing", member.scope.rootScope.opts.HookScopeClosing, first)
		}
	}
}

// shutdownRound shuts down the ready services, in parallel across scopes.
func (p *exportPass) shutdownRound(ready map[*passScope][]string) {
	members := make([]*passScope, 0, len(ready))
	for member := range ready {
		members = append(members, member)
	}
	reports := make([]*ShutdownReport, len(members))

	ctx := p.ctx // captured instead of p, as in shutdownOtherChildren
	var wg sync.WaitGroup
	for index, member := range members {
		wg.Go(func() {
			reports[index] = member.scope.shutdownServicesWithoutDependenciesInParallel(ctx, ready[member])
		})
	}
	wg.Wait()

	for index, member := range members {
		member.reports = append(member.reports, reports[index])
	}
}

// settle closes, children first, each exporting descendant with no service
// left whose exporting descendants are closed: its HookScopeClosing runs if
// it did not yet, then its HookScopeClosed, and its shutdown is released.
func (p *exportPass) settle(first *hookPanic) {
	for _, member := range slices.Backward(p.scopes) {
		if member.run == nil || member.finished || !member.closable() {
			continue
		}

		scope := member.scope
		hooks := scope.rootScope.opts
		if !member.opened {
			member.opened = true
			scope.runScopeObserverHooks(p.ctx, "scope-closing", hooks.HookScopeClosing, first)
		}
		scope.runScopeObserverHooks(p.ctx, "scope-closed", hooks.HookScopeClosed, first)

		member.report = member.mergedReport()
		if member.waitErr != nil {
			member.report.Errors[newServiceDescription(scope.id, scope.name, "")] = member.waitErr
		}
		member.report.ShutdownTime = scope.clock().Since(member.start)
		member.report.Succeed = len(member.report.Errors) == 0

		member.finished = true
		scope.finishScopeClose(member.run, member.report)
		scope.endClosing()
		member.parent.scope.removeChild(scope)
	}
}

// closable reports whether the scope has no service left and its exporting
// children in the pass are closed.
func (m *passScope) closable() bool {
	m.scope.mu.RLock()
	empty := len(m.scope.services) == 0
	m.scope.mu.RUnlock()
	if !empty {
		return false
	}

	for _, child := range m.children {
		if !child.finished {
			return false
		}
	}

	return true
}

// mergedReport merges the report of the scope so far with the full reports
// of its exporting children in the pass.
func (m *passScope) mergedReport() *ShutdownReport {
	reports := append([]*ShutdownReport{}, m.reports...)
	for _, child := range m.children {
		if child.report != nil {
			reports = append(reports, child.report)
		}
	}

	return mergeShutdownReports(reports...)
}

// exportingChildren returns the child scopes the exports of s resolve from.
func (s *Scope) exportingChildren() []*Scope {
	s.mu.RLock()
	exports := []serviceWrapperExport{}
	for _, service := range s.services {
		if export, ok := asExport(service); ok {
			exports = append(exports, export)
		}
	}
	s.mu.RUnlock()

	// exportSource locks s: it is called once s is unlocked
	seen := map[*Scope]struct{}{}
	children := []*Scope{}
	for _, export := range exports {
		child, _, ok := export.exportSource()
		if _, dup := seen[child]; ok && !dup {
			seen[child] = struct{}{}
			children = append(children, child)
		}
	}

	return children
}

// removeChild removes child from the child scopes of s.
func (s *Scope) removeChild(child *Scope) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.childScopes[child.name] == child {
		delete(s.childScopes, child.name)
	}
}
