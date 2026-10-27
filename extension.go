package doger

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// ErrInvalidExtension is matched by the error [Extend] returns for an
// extension it cannot install: a nil or typed-nil extension, an empty or
// invalid name or instance key, or an empty [ExtendScopeName]. Nothing is
// installed then.
var ErrInvalidExtension = errors.New("DI: invalid extension")

// ErrExtensionDisabled is returned, possibly wrapped, by an extension's
// Extend to decline installing, for example when its configuration turns it
// off. [Extend] then rolls back what it registered, records the identity as
// disabled, logs one line naming it, and returns nil. The library has no
// enable/disable switch of its own: whether to decline is the extension's
// choice.
var ErrExtensionDisabled = errors.New("DI: extension disabled")

// ErrRequiredExtensionDisabled is matched by the error
// [ExtensionContext.Require] returns for a disabled extension. It does not
// match [ErrExtensionDisabled], so an extension returning it fails instead
// of being disabled in turn.
var ErrRequiredExtensionDisabled = errors.New("DI: required extension is disabled")

// ErrExtensionCycle is matched by the error of a cycle of
// [ExtensionContext.Require] calls, such as a requiring b requiring a, on
// one goroutine (the error names the chain, "a -> b -> a") or across
// goroutines installing concurrently. The extensions of the cycle fail and
// are rolled back instead of deadlocking.
var ErrExtensionCycle = errors.New("DI: extension cycle")

// ErrExtensionConflict is matched by the error [Extend] returns, installing
// nothing, when an extension identity is already installed with another
// extension value or other [ExtendOption]s, or when the private scope name
// of the extension is already used by another extension or scope of the
// root. Install a second instance with an instance key
// ([ExtensionInstance]), or rename the private scope with
// [ExtendScopeName].
var ErrExtensionConflict = errors.New("DI: extension conflict")

// ExtensionError is the error [Extend] returns when an extension fails to
// install: its Extend returned an error or panicked, or an export it asked
// for failed, or, once its name is known to be valid, its other inputs are
// invalid. Find it with [errors.As]; Err is the cause, which [errors.Is] and
// errors.As also see. A recovered panic becomes an error that wraps the
// panic value when it is an error.
type ExtensionError struct {
	Name     string // the extension's name
	Instance string // its instance key, empty without one
	Err      error  // the cause
}

// Error names the extension and its instance key, followed by the cause.
func (e *ExtensionError) Error() string {
	return "DI: extension " + describeExtension(e.Name, e.Instance) + ": " + e.Err.Error()
}

// Unwrap returns the cause.
func (e *ExtensionError) Unwrap() error { return e.Err }

// Extension is the contract a module implements once so that a capability,
// usually the integration of a library, installs into any container with one
// [Extend] call. Modules implementing it are conventionally named
// doger<lib> and expose a constructor Extension(opts...).
//
// Only Name and Extend are required. Further extension points come as
// optional interfaces, such as [ExtensionInstance], never as new methods
// here, so implementations never break.
type Extension interface {
	// Name names the extension: lowercase alphanumeric words joined by
	// single dashes, such as "redis" or "redis-cluster". It names the
	// extension's private scope and errors.
	Name() string
	// Extend registers the extension's services through x, in x.Scope(),
	// and publishes the ones the application may use with
	// [ExtensionExport]. A non-nil error fails the install.
	Extend(x *ExtensionContext) error
}

// ExtensionInstance is the optional interface of an extension that can be
// installed more than once in a container, once per instance key, for
// example one Redis client per database. The key follows the grammar of
// [Extension.Name].
type ExtensionInstance interface {
	// Instance returns the instance key; an empty key means none.
	Instance() string
}

// Placement is where an extension registers its services. Its zero value,
// PlacementPrivate, is the default.
type Placement int

const (
	// PlacementPrivate registers the extension's services into its private
	// child scope of the root, from which only its exports are visible. It
	// is the zero value, on purpose: private is the safe default.
	PlacementPrivate Placement = iota
	// PlacementFlat registers the extension's services into the root's own
	// scope, where every service sees them; exports are then no-ops.
	PlacementFlat
)

// ExtensionPlacement is the optional interface of an extension choosing its
// default placement. An extension without it is installed privately. The
// installer has the last word: [Flatten] installs into the root whatever the
// extension chose.
type ExtensionPlacement interface {
	// Placement returns where the extension registers its services.
	Placement() Placement
}

// ExtendOption configures how [Extend] installs an extension.
type ExtendOption func(*extendOptions)

// extendOptions is the configuration of an Extend call.
type extendOptions struct {
	flatten      bool
	scopeName    string
	scopeNameSet bool
}

// Flatten installs the extension into the root's own scope instead of a
// private scope, whatever placement the extension declares, and makes its
// exports no-ops. A flattened install that fails is not rolled back: the
// services it registered before failing stay registered in the root.
func Flatten() ExtendOption {
	return func(options *extendOptions) { options.flatten = true }
}

// ExtendScopeName names the extension's private scope name instead of
// "ext:<name>[/<instance>]", for example to avoid a clash with another scope
// or extension of the same name. It has no effect when the extension is
// installed flat (see [Flatten] and [PlacementFlat]). An empty name makes
// [Extend] fail with an error matching [ErrInvalidExtension].
func ExtendScopeName(name string) ExtendOption {
	return func(options *extendOptions) {
		options.scopeName = name
		options.scopeNameSet = true
	}
}

// extensionNamePattern is the grammar of extension names and instance keys:
// lowercase alphanumeric words joined by single dashes.
var extensionNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Extend installs ext into root, a nil root meaning [DefaultRootScope]. It
// calls ext.Extend once, with an [ExtensionContext] whose Scope is a new
// private child scope of root, named "ext:<name>" or, for an extension with
// an instance key, "ext:<name>/<instance>". [ExtendScopeName] renames it.
// The services the extension registers there stay invisible from root,
// except those it exports with [ExtensionExport] or [ExtensionExportNamed]:
// those exports are applied once ext.Extend returned nil, and an export that
// fails fails the install.
//
// Extend validates ext first and returns an error matching
// [ErrInvalidExtension], installing nothing, for a nil or typed-nil
// extension, a name or instance key outside the grammar of
// [Extension.Name], or an empty ExtendScopeName. Validation never panics,
// even when Name or Instance does.
//
// A failing install leaves nothing behind: when ext.Extend returns an error
// or panics, or an export fails, Extend returns an [*ExtensionError] and
// rolls the private scope back. It shuts the scope down, with a context
// that never ends, so every service the attempt built is shut down and the
// scope's shutdown and HookScopeClosing/HookScopeClosed hooks fire, as its
// registration hooks did; it then detaches the scope, removes the exports
// already applied, and forgets the identity, so a later Extend can retry. A
// failed shutdown report is joined to the error. Extensions the failing one
// required stay installed. A flattened install (see [Flatten]) is not rolled
// back: its registrations stay in the root.
//
// Phase: Extend is allowed while the container is idle, starting (from a
// HookContainerStarting hook, its services are then started by that Start),
// started, ready or failed. After a successful Start, call
// [RootScope.Refresh] to start the services an extension added; after a
// failed Start, Refresh returns [ErrNotStarted] as usual. Once root Shutdown
// began, Extend returns an error matching [ErrStartAfterShutdown] and
// installs nothing. Like the hook adders, Extend must not be called
// concurrently with Start, and a Shutdown beginning while Extend runs is not
// prevented.
//
// Identity: an extension is identified by its Go type plus its instance key
// ([ExtensionInstance]). Installing an identity again does nothing when the
// extension value is deeply equal to the installed one ([reflect.DeepEqual])
// and the ExtendOptions have the same effect; otherwise Extend returns an
// error matching [ErrExtensionConflict]. Extension authors therefore keep
// their options as plain data (a non-nil func field never compares equal)
// and always return a pointer from their constructor: T and *T are two
// identities, which collide on their private scope name. Concurrent Extend
// calls install an identity once: a call for an identity being installed
// waits for that installation, then follows these rules.
//
// From inside an extension's Extend, install the extensions it depends on
// with [ExtensionContext.Require], never with Extend: Require cycles are
// detected, but a nested Extend starts an installation the outer one cannot
// see, so if it leads back to an extension being installed by the outer
// call, through Require or another nested Extend, it deadlocks, as a
// [sync.Once.Do] called from its own function does.
func Extend(root *RootScope, ext Extension, opts ...ExtendOption) error {
	if root == nil {
		root = DefaultRootScope
	}

	options := extendOptions{}
	for _, opt := range opts {
		opt(&options)
	}

	return installInSession(root, ext, options, requester{})
}

// requester is who asks for an install: a top-level Extend, a zero
// requester that starts a new session, or a Require of the extension at the
// end of chain, in its session.
type requester struct {
	session    uint64
	chain      []extensionLink
	viaRequire bool
}

// installInSession validates and plans ext, installed with options, and
// installs it for from.
func installInSession(root *RootScope, ext Extension, options extendOptions, from requester) error {
	id, name, err := validateExtension(ext, options)
	if err != nil && name != "" {
		return &ExtensionError{Name: name, Err: err}
	}
	if err != nil {
		return err
	}

	plan, err := planInstall(ext, name, id.instance, options)
	if err != nil {
		return &ExtensionError{Name: name, Instance: id.instance, Err: err}
	}

	if phase := root.Phase(); phase == PhaseClosing || phase == PhaseClosed {
		return &ExtensionError{Name: name, Instance: id.instance, Err: fmt.Errorf("%w: cannot install in phase %s", ErrStartAfterShutdown, phase)}
	}

	registry := root.extensionRegistry()
	if from.session == 0 {
		from.session = registry.sessions.Add(1)
	}
	req := installRequest{ext: ext, id: id, name: name, plan: plan, session: from.session, chain: from.chain, viaRequire: from.viaRequire}

	return installExtension(root, registry, req)
}

// installRequest is an extension to install, validated and planned, and
// who asks for it.
type installRequest struct {
	ext  Extension
	id   extensionID
	name string
	plan installPlan

	session    uint64          // the session installing it: one top-level Extend and its Require calls
	chain      []extensionLink // the extensions whose Require led to this request, outermost first
	viaRequire bool            // asked by Require
}

// extensionLink is an extension in a chain of Require calls.
type extensionLink struct {
	id   extensionID
	name string
}

// extensionState is the state of an extension identity in a container.
type extensionState int

const (
	extensionInstalling extensionState = iota
	extensionInstalled
	extensionDisabled
)

// extensionEntry is an extension identity known to a container.
type extensionEntry struct {
	state   extensionState
	done    chan struct{} // closed when the installation ends
	session uint64        // the session installing it
	name    string        // the extension's name

	// installed and disabled only
	value      Extension
	plan       installPlan
	viaRequire bool     // installed by Require only
	scope      *Scope   // the private scope; nil when flat
	exports    []string // the published names of its exports
}

// extensionRegistry holds the extensions of a container.
type extensionRegistry struct {
	mu       sync.Mutex
	entries  map[extensionID]*extensionEntry
	scopes   map[string]extensionID // private scope names, installing and installed
	waits    map[uint64]uint64      // waits-for graph: a waiting session to the session it waits for
	sessions atomic.Uint64          // the last session ID

	sourceOnce sync.Once      // decides source at the first property read
	source     PropertySource // the container's property source, once decided
}

// extensionRegistry returns the registry of the container, created on first
// use, so a container without extensions holds none.
func (s *RootScope) extensionRegistry() *extensionRegistry {
	if registry := s.ext.Load(); registry != nil {
		return registry
	}
	s.ext.CompareAndSwap(nil, &extensionRegistry{
		entries: map[extensionID]*extensionEntry{},
		scopes:  map[string]extensionID{},
		waits:   map[uint64]uint64{},
	})

	return s.ext.Load()
}

// extensionScopes returns the private scopes of the extensions installed in
// the container, ordered by name; none without extensions.
func (s *RootScope) extensionScopes() []*Scope {
	registry := s.ext.Load()
	if registry == nil {
		return nil
	}

	registry.mu.Lock()
	scopes := []*Scope{}
	for _, entry := range registry.entries {
		if entry.state == extensionInstalled && entry.scope != nil {
			scopes = append(scopes, entry.scope)
		}
	}
	registry.mu.Unlock()

	slices.SortFunc(scopes, func(a, b *Scope) int { return strings.Compare(a.name, b.name) })

	return scopes
}

// claim is what a request does with its extension identity.
type claim struct {
	entry    *extensionEntry // the entry the request installs, when owner
	owner    bool
	replaced *extensionEntry // the required install the request replaces, when owner
	wait     <-chan struct{} // the end of a running installation to wait for, then claim again
	edge     bool            // the wait is recorded in the waits-for graph
}

// installExtension installs the extension of req: it installs it when its
// identity is free, applies the repeat rules when it is installed or
// disabled, and waits for a running installation of it to end.
func installExtension(root *RootScope, registry *extensionRegistry, req installRequest) error {
	for {
		c, err := registry.claim(root, req)
		if err != nil {
			return err
		}
		if c.owner {
			disabled, err := runInstall(root, registry, c, req)
			if disabled && req.viaRequire {
				return requiredDisabled(req)
			}
			return err
		}
		if c.wait == nil {
			return nil
		}
		<-c.wait
		if c.edge {
			registry.mu.Lock()
			delete(registry.waits, req.session)
			registry.mu.Unlock()
		}
	}
}

// requiredDisabled is the error of a Require of the disabled extension of req.
func requiredDisabled(req installRequest) error {
	return fmt.Errorf("%w: extension %s", ErrRequiredExtensionDisabled, describeExtension(req.name, req.id.instance))
}

// claim decides what req does with its identity: install it as the owner of
// a new entry (replacing an unused required install), wait for a running
// installation, or nothing, for a repeat. err is a conflict, a cycle, or a
// Require of a disabled extension.
func (r *extensionRegistry) claim(root *RootScope, req installRequest) (claim, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.entries[req.id]
	switch {
	case !ok:
		if err := r.reserveScope(root, req); err != nil {
			return claim{}, err
		}
		return claim{entry: r.startEntry(req), owner: true}, nil
	case entry.state == extensionInstalling:
		return r.waitFor(entry, req)
	case entry.state == extensionDisabled && req.viaRequire:
		return claim{}, requiredDisabled(req)
	case entry.state == extensionInstalled && req.viaRequire:
		return claim{}, nil
	case req.plan == entry.plan && reflect.DeepEqual(entry.value, req.ext):
		entry.viaRequire = false
		return claim{}, nil
	case replaceable(entry):
		return r.replaceRequired(root, entry, req)
	default:
		return claim{}, fmt.Errorf("%w: extension %s is already installed with other options", ErrExtensionConflict, describeExtension(req.name, req.id.instance))
	}
}

// replaceable reports whether entry is a private install made by Require
// only, none of whose services was resolved, which an explicit Extend
// therefore replaces.
func replaceable(entry *extensionEntry) bool {
	return entry.state == extensionInstalled && entry.viaRequire && entry.scope != nil && !scopeResolved(entry.scope)
}

// replaceRequired claims the identity of entry, a replaceable install, for
// req, moving the private scope name reservation when req's differs. r.mu
// must be held.
func (r *extensionRegistry) replaceRequired(root *RootScope, entry *extensionEntry, req installRequest) (claim, error) {
	if req.plan != entry.plan { // the same private scope name stays reserved
		if err := r.reserveScope(root, req); err != nil {
			return claim{}, err
		}
		delete(r.scopes, entry.plan.scopeName)
	}

	return claim{entry: r.startEntry(req), owner: true, replaced: entry}, nil
}

// reserveScope reserves the private scope name of req, or returns an error
// matching ErrExtensionConflict when another extension or scope uses it.
// r.mu must be held.
func (r *extensionRegistry) reserveScope(root *RootScope, req installRequest) error {
	if req.plan.flat {
		return nil
	}
	if _, taken := r.scopes[req.plan.scopeName]; taken || root.self.hasChild(req.plan.scopeName) {
		return fmt.Errorf("%w: scope `%s` of extension %s is already used", ErrExtensionConflict, req.plan.scopeName, describeExtension(req.name, req.id.instance))
	}
	r.scopes[req.plan.scopeName] = req.id

	return nil
}

// startEntry records the identity of req as being installed by its session.
// r.mu must be held.
func (r *extensionRegistry) startEntry(req installRequest) *extensionEntry {
	entry := &extensionEntry{state: extensionInstalling, done: make(chan struct{}), session: req.session, name: req.name}
	r.entries[req.id] = entry

	return entry
}

// waitFor returns the wait of req for entry, being installed, or an error
// matching ErrExtensionCycle when the wait could never end: req's own chain
// of Require calls is installing it, or its installer waits, through other
// sessions, for req's session. r.mu must be held.
func (r *extensionRegistry) waitFor(entry *extensionEntry, req installRequest) (claim, error) {
	if entry.session == req.session {
		for index, link := range req.chain {
			if link.id == req.id {
				return claim{}, cycleError(req.chain[index:], req.name)
			}
		}
		return claim{wait: entry.done}, nil
	}

	if waitsForCycle(r.waits, req.session, entry.session) {
		return claim{}, fmt.Errorf("%w: extension %s is being installed by a call that waits for this one", ErrExtensionCycle, describeExtension(req.name, req.id.instance))
	}
	r.waits[req.session] = entry.session

	return claim{wait: entry.done, edge: true}, nil
}

// waitsForCycle reports whether waiter waiting for owner would close a cycle
// in graph, the waits-for graph: owner waits, through other sessions, for
// waiter.
func waitsForCycle(graph map[uint64]uint64, waiter, owner uint64) bool {
	for session, ok := owner, true; ok; session, ok = graph[session] {
		if session == waiter {
			return true
		}
	}

	return false
}

// cycleError is the error of a Require cycle: chain, then name again.
func cycleError(chain []extensionLink, name string) error {
	names := make([]string, 0, len(chain)+1)
	for _, link := range chain {
		names = append(names, link.name)
	}
	names = append(names, name)

	return fmt.Errorf("%w: %s", ErrExtensionCycle, strings.Join(names, " -> "))
}

// scopeResolved reports whether a service of scope or of its descendants was
// resolved there; resolving an export resolves its source in its scope.
func scopeResolved(scope *Scope) bool {
	scope.mu.RLock()
	resolved := len(scope.orderedInvocation) > 0
	scope.mu.RUnlock()
	if resolved {
		return true
	}

	return slices.ContainsFunc(scope.Children(), scopeResolved)
}

// runInstall runs the installation of req that c claimed, rolling back the
// required install c replaces first, and records its outcome: installed on
// success, disabled when the extension declined, forgotten on failure.
func runInstall(root *RootScope, registry *extensionRegistry, c claim, req installRequest) (disabled bool, err error) {
	ext, id, name, plan := req.ext, req.id, req.name, req.plan
	outcome := extensionInstalling // failed, until decided
	x := &ExtensionContext{root: root, scope: root.self, instance: id.instance, flat: plan.flat, session: req.session}
	defer func() { registry.release(c.entry, req, outcome, x) }()

	if c.replaced != nil {
		_ = rollbackInstall(root, c.replaced.plan, c.replaced.scope, c.replaced.exports)
	}

	x.chain = append(append([]extensionLink{}, req.chain...), extensionLink{id: id, name: name})
	if !plan.flat {
		x.scope, err = newPrivateScope(root, plan.scopeName, name, id.instance)
		if err != nil {
			return false, err
		}
	}

	applied, err := extendAndExport(ext, x)
	x.applied = applied
	if err != nil {
		if !plan.flat {
			if rollbackErr := rollbackInstall(root, plan, x.scope, applied); rollbackErr != nil {
				err = errors.Join(err, rollbackErr)
			}
		}
		if errors.Is(err, ErrExtensionDisabled) {
			outcome = extensionDisabled
			root.opts.Logf("DI: extension %s disabled", plainExtensionName(name, id.instance))
			return true, nil
		}
		return false, &ExtensionError{Name: name, Instance: id.instance, Err: err}
	}
	outcome = extensionInstalled

	return false, nil
}

// plainExtensionName names an extension in log lines: name, or
// name/instance.
func plainExtensionName(name, instance string) string {
	if instance == "" {
		return name
	}

	return name + "/" + instance
}

// rollbackInstall undoes a private installation planned as plan into scope:
// it shuts scope down, with a context that never ends so that every service
// built there is shut down, detaches it from root so that its name is free
// again, and removes the exports named appliedExports it published in root.
// It returns the failed shutdown report, if any.
func rollbackInstall(root *RootScope, plan installPlan, scope *Scope, appliedExports []string) error {
	var err error
	if report := scope.ShutdownWithContext(context.Background()); !report.Succeed {
		err = report
	}
	root.self.detachChild(plan.scopeName)
	for _, published := range appliedExports {
		root.self.removeService(published, func(service any) bool { return isExportFrom(service, scope) })
	}

	return err
}

// extendAndExport calls ext.Extend with x, turning a panic into an error,
// closes x, then applies the exports it recorded. It returns the published
// names of the exports it applied.
func extendAndExport(ext Extension, x *ExtensionContext) (applied []string, err error) {
	func() {
		defer x.closed.Store(true)
		defer func() {
			if recovered := recover(); recovered != nil {
				if recoveredErr, ok := recovered.(error); ok {
					err = fmt.Errorf("panic: %w", recoveredErr)
				} else {
					err = fmt.Errorf("panic: %v", recovered)
				}
			}
		}()
		err = ext.Extend(x)
	}()
	if err != nil {
		return nil, err
	}

	return x.applyExports()
}

// release ends the installation of req that entry records with outcome:
// installed or disabled, keeping what x installed, or failed
// (extensionInstalling), forgetting the identity and its scope name so that
// a later call can retry. It wakes the calls waiting for it.
func (r *extensionRegistry) release(entry *extensionEntry, req installRequest, outcome extensionState, x *ExtensionContext) {
	r.mu.Lock()
	switch outcome {
	case extensionInstalled:
		entry.state, entry.value, entry.plan, entry.viaRequire = outcome, req.ext, req.plan, req.viaRequire
		entry.exports = x.applied
		if !req.plan.flat {
			entry.scope = x.scope
		}
	case extensionDisabled:
		entry.state, entry.value, entry.plan = outcome, req.ext, req.plan
		if !req.plan.flat {
			delete(r.scopes, req.plan.scopeName)
		}
	case extensionInstalling:
		delete(r.entries, req.id)
		if !req.plan.flat {
			delete(r.scopes, req.plan.scopeName)
		}
	}
	r.mu.Unlock()

	close(entry.done)
}

// newPrivateScope creates the private scope scopeName of an extension in
// root, or returns an error matching ErrExtensionConflict when a scope of
// that name was created meanwhile.
func newPrivateScope(root *RootScope, scopeName, name, instance string) (scope *Scope, err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("%w: scope `%s` of extension %s is already used", ErrExtensionConflict, scopeName, describeExtension(name, instance))
		}
	}()

	return root.self.Scope(scopeName), nil
}

// describeExtension names an extension in errors and logs.
func describeExtension(name, instance string) string {
	if instance == "" {
		return "`" + name + "`"
	}

	return "`" + name + "` (instance `" + instance + "`)"
}

// extensionID is the identity of an installed extension: its Go type and
// its instance key.
type extensionID struct {
	typ      reflect.Type
	instance string
}

// validateExtension returns the identity and the name of ext, or an error
// matching ErrInvalidExtension when ext or options cannot be installed,
// with the name of ext once it is known to be valid.
func validateExtension(ext Extension, options extendOptions) (id extensionID, name string, err error) {
	if isNilExtension(ext) {
		return extensionID{}, "", fmt.Errorf("%w: extension is nil", ErrInvalidExtension)
	}
	name, err = callExtension("Name", ext.Name)
	if err != nil {
		return extensionID{}, "", err
	}
	if name == "" {
		return extensionID{}, "", fmt.Errorf("%w: extension name is empty", ErrInvalidExtension)
	}
	if !extensionNamePattern.MatchString(name) {
		return extensionID{}, "", fmt.Errorf("%w: invalid extension name `%s`", ErrInvalidExtension, name)
	}
	instance := ""
	if withInstance, ok := ext.(ExtensionInstance); ok {
		instance, err = callExtension("Instance", withInstance.Instance)
		if err != nil {
			return extensionID{}, name, err
		}
	}
	if instance != "" && !extensionNamePattern.MatchString(instance) {
		return extensionID{}, name, fmt.Errorf("%w: invalid instance key `%s`", ErrInvalidExtension, instance)
	}
	if options.scopeNameSet && options.scopeName == "" {
		return extensionID{}, name, fmt.Errorf("%w: scope name is empty", ErrInvalidExtension)
	}

	return extensionID{typ: reflect.TypeOf(ext), instance: instance}, name, nil
}

// installPlan is where an Extend call installs an extension.
type installPlan struct {
	flat      bool   // into the root's own scope
	scopeName string // the private scope otherwise
}

// planInstall returns where ext, the extension name with the instance key
// instance, is installed: the root's own scope with Flatten or when the
// author asks for PlacementFlat, its private scope otherwise.
func planInstall(ext Extension, name, instance string, options extendOptions) (installPlan, error) {
	flat := options.flatten
	if placed, ok := ext.(ExtensionPlacement); ok && !flat {
		placement, err := callExtension("Placement", placed.Placement)
		if err != nil {
			return installPlan{}, err
		}
		flat = placement == PlacementFlat
	}
	if flat {
		return installPlan{flat: true}, nil
	}

	return installPlan{scopeName: privateScopeName(name, instance, options)}, nil
}

// privateScopeName returns the name of the private scope of the extension
// name with the instance key instance: the installer's ExtendScopeName,
// otherwise "ext:<name>[/<instance>]".
func privateScopeName(name, instance string, options extendOptions) string {
	if options.scopeNameSet {
		return options.scopeName
	}
	if instance != "" {
		return "ext:" + name + "/" + instance
	}

	return "ext:" + name
}

// isNilExtension reports whether ext is nil or holds a nil value, which its
// methods could not be called on.
func isNilExtension(ext Extension) bool {
	if ext == nil {
		return true
	}

	value := reflect.ValueOf(ext)
	kind := value.Kind()
	nilable := kind == reflect.Pointer || kind == reflect.Map || kind == reflect.Slice ||
		kind == reflect.Func || kind == reflect.Chan || kind == reflect.Interface

	return nilable && value.IsNil()
}

// callExtension returns what method, the extension method named methodName,
// returns, or an error matching ErrInvalidExtension when it panics.
func callExtension[T any](methodName string, method func() T) (result T, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%w: %s panicked: %v", ErrInvalidExtension, methodName, recovered)
		}
	}()

	return method(), nil
}
