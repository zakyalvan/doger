package doger

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// ExtensionContext is what an extension installs itself through: [Extend]
// builds one for each [Extension.Extend] call, which is the only time it is
// valid. Once that call returned, [ExtensionExport], [ExtensionExportNamed]
// and Require fail with an error matching [ErrInvalidExtension]. It is a
// struct, so that new capabilities can be added to it without breaking
// extensions.
type ExtensionContext struct {
	root     *RootScope
	scope    *Scope
	instance string
	flat     bool // registering into the root's own scope: exports are no-ops

	session uint64          // the session installing the extension
	chain   []extensionLink // the extensions being installed by Require up to this one, outermost first
	closed  atomic.Bool     // set when Extend returned
	lateLog sync.Once       // logs the first property read after Extend returned
	applied []string        // the published names of the exports applied; written once Extend returned

	mu      sync.Mutex
	exports []pendingExport // applied when Extend succeeds
}

// pendingExport is an export recorded during Extend.
type pendingExport struct {
	published string // the name it is published under in the parent
	apply     func() error
}

// Scope returns the scope the extension registers its services in: its
// private child scope of the root by default.
func (x *ExtensionContext) Scope() *Scope { return x.scope }

// Root returns the container the extension is installed into.
func (x *ExtensionContext) Root() *RootScope { return x.root }

// Instance returns the extension's instance key, empty without one.
func (x *ExtensionContext) Instance() string { return x.instance }

// Require installs dep, an extension this one depends on, as [Extend]
// would without options, and returns nil when dep's identity is already
// installed, whatever its value or options, so several extensions can
// require the same one. It returns an error matching
// [ErrRequiredExtensionDisabled] when dep is disabled, and an error matching
// [ErrExtensionCycle] when dep is being installed by this chain of Require
// calls, or by a concurrent Extend that waits for this one. A failure of dep
// fails this call; extensions that dep or this extension required and that
// installed stay installed. Return Require's error from Extend to fail this
// extension too.
//
// An install made through Require only is replaced by a later explicit
// Extend of the same identity with a different value, as long as no service
// of its private scope (or its descendants) was resolved; once one was, that
// Extend fails with [ErrExtensionConflict].
//
// Like the context, Require is valid only while this extension's Extend
// runs: called later, it returns an error matching [ErrInvalidExtension].
func (x *ExtensionContext) Require(dep Extension) error {
	if x.closed.Load() {
		return x.closedError()
	}

	return installInSession(x.root, dep, extendOptions{}, requester{session: x.session, chain: x.chain, viaRequire: true})
}

// Properties returns the extension's view of the container's property
// source, prefixed with its name and instance key (see [Properties]). Read
// it during Extend; providers, which run later, use [ExtensionProperties].
func (x *ExtensionContext) Properties() Properties {
	return Properties{root: x.root, prefix: propertyPrefix(x.name(), x.instance), x: x}
}

// name returns the extension's name.
func (x *ExtensionContext) name() string { return x.chain[len(x.chain)-1].name }

// closedError is the error of using x after its Extend returned.
func (x *ExtensionContext) closedError() error {
	return fmt.Errorf("%w: the context of extension `%s` is used after its Extend returned", ErrInvalidExtension, x.name())
}

// ExtensionExport publishes the service named after T, registered in
// x.Scope(), to the root, as [Export] does. The export is recorded and
// applied only once the extension's Extend returned nil, so a failing
// install publishes nothing; an export that then fails, for example because
// the service was never registered, fails the install. Options are those of
// Export, such as [ExportAs].
func ExtensionExport[T any](x *ExtensionContext, opts ...ExportOption) error {
	return ExtensionExportNamed[T](x, NameOf[T](), opts...)
}

// ExtensionExportNamed is like [ExtensionExport] for the service registered
// as name in x.Scope(); it is published as name unless [ExportAs] renames
// it.
func ExtensionExportNamed[T any](x *ExtensionContext, name string, opts ...ExportOption) error {
	if x.closed.Load() {
		return x.closedError()
	}
	if x.flat {
		return nil
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	x.exports = append(x.exports, pendingExport{
		published: buildExportOptions(name, opts).published,
		apply:     func() error { return ExportNamed[T](x.scope, name, opts...) },
	})

	return nil
}

// applyExports applies the exports recorded during Extend, in order. It
// stops at the first error and returns the published names of the exports
// it applied.
func (x *ExtensionContext) applyExports() (applied []string, err error) {
	x.mu.Lock()
	exports := x.exports
	x.mu.Unlock()

	for _, export := range exports {
		if err := export.apply(); err != nil {
			return applied, err
		}
		applied = append(applied, export.published)
	}

	return applied, nil
}

// isExportFrom reports whether service is an export of the scope source, or
// a default decorating one.
func isExportFrom(service any, source *Scope) bool {
	switch wrapper := service.(type) {
	case serviceWrapperExport:
		return wrapper.withinSource(source)
	case serviceWrapperUnwrapExport:
		export, _ := wrapper.unwrapExport()
		return export != nil && export.withinSource(source)
	default:
		return false
	}
}
