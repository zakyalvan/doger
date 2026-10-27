package doger

import (
	"os"
	"regexp"
	"strings"
)

// PropertySource is where extensions read their configuration, through
// [Properties]. Implement it to adapt a configuration library (viper, koanf,
// a file...) without the container depending on one.
//
// A container decides its source at its first property read, and keeps it:
// [InjectorOpts.PropertySource] when set; otherwise the service the
// application registered in the root under NameOf[PropertySource]() (a
// service of any other name is never selected, even with a Lookup method);
// otherwise EnvPropertySource("DOGER_"), registered there as a default. A
// PropertySource service registered after that first read is not used, and
// registering one under NameOf[PropertySource]() then panics as a duplicate:
// set InjectorOpts.PropertySource to avoid any ordering constraint.
//
// The provider of a PropertySource service must not read properties itself,
// directly or through the services it invokes: it would wait for its own
// decision. Give it its configuration through its own arguments instead.
//
// The container logs the type of the source it chose, never a value, and no
// error it builds holds a property value.
type PropertySource interface {
	// Lookup returns the value of key and whether it is set. Keys are
	// lowercase alphanumeric words joined by single dashes, in segments
	// separated by dots, such as "redis.cache.pool-size".
	Lookup(key string) (string, bool)
}

// EnvPropertySource returns a PropertySource reading environment variables:
// the variable of a key is prefix followed by the key upper cased, each
// dash replaced by two underscores and each dot by one, so
// EnvPropertySource("DOGER_") reads "redis.cache-addr" from
// DOGER_REDIS_CACHE__ADDR. With the key grammar of [PropertySource],
// distinct keys never share a variable. An unset variable is a miss; a set,
// empty one is the empty value. It is the container's default source, with
// the prefix "DOGER_".
func EnvPropertySource(prefix string) PropertySource {
	return envPropertySource{prefix: prefix}
}

// envPropertySource reads environment variables.
type envPropertySource struct{ prefix string }

func (s envPropertySource) Lookup(key string) (string, bool) {
	return os.LookupEnv(envName(s.prefix, key))
}

// envKeyReplacer maps the separators of a property key to the separators of
// an environment variable name: a dash to two underscores, a dot to one.
var envKeyReplacer = strings.NewReplacer("-", "__", ".", "_")

// envName returns the name of the environment variable of key: key upper
// cased, with its separators replaced, after prefix.
func envName(prefix, key string) string {
	return prefix + envKeyReplacer.Replace(strings.ToUpper(key))
}

// Properties is an extension's view of the container's property source:
// Lookup(key) reads "<name>.<key>", or "<name>.<instance>.<key>" for an
// extension with an instance key. Extensions get it from
// [ExtensionContext.Properties] while their Extend runs, and their providers
// from [ExtensionProperties]. Its zero value misses every key.
//
// By convention, an extension gives precedence to the options of its
// constructor, then to properties, then to its own defaults.
type Properties struct {
	root   *RootScope
	prefix string
	x      *ExtensionContext // the context the view comes from; nil from ExtensionProperties
}

// propertyKeyPattern is the grammar of full property keys: segments of
// lowercase alphanumeric words joined by single dashes, separated by dots.
var propertyKeyPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*(\.[a-z0-9]+(-[a-z0-9]+)*)*$`)

// Lookup returns the value of key under the extension's prefix and whether
// it is set. A key that would make an invalid full key (see
// [PropertySource]), such as "Addr" or "pool--size", misses without
// consulting the source: configuration then falls back to defaults. A view
// taken from an [ExtensionContext] misses once its Extend returned, logging
// one line.
func (p Properties) Lookup(key string) (string, bool) {
	if p.root == nil {
		return "", false
	}
	if p.x != nil && p.x.closed.Load() {
		p.x.lateLog.Do(func() {
			p.root.opts.Logf("DI: properties of extension %s read after its Extend returned", p.x.name())
		})
		return "", false
	}

	fullKey := p.prefix + "." + key
	if !propertyKeyPattern.MatchString(fullKey) {
		return "", false
	}

	registry := p.root.extensionRegistry()
	registry.sourceOnce.Do(func() { registry.source = decidePropertySource(p.root) })

	return registry.source.Lookup(fullKey)
}

// decidePropertySource returns the property source of root:
// InjectorOpts.PropertySource when set, else the root's service named
// NameOf[PropertySource](), registering EnvPropertySource("DOGER_") there
// as a default first. It logs the source's type, never its values.
func decidePropertySource(root *RootScope) PropertySource {
	source := root.opts.PropertySource
	if source == nil {
		name := NameOf[PropertySource]()
		if !root.self.serviceExist(name) {
			ProvideNamedValue(root, name, EnvPropertySource("DOGER_"), AsDefault())
		}
		var err error
		source, err = InvokeNamed[PropertySource](root, name)
		if err != nil {
			root.opts.Logf("DI: property source unavailable, every property is unset: %v", err)
			source = noProperties{}
		}
	}
	root.opts.Logf("DI: property source %T", source)

	return source
}

// noProperties is a property source where no property is set.
type noProperties struct{}

func (noProperties) Lookup(string) (string, bool) { return "", false }

// propertyPrefix returns the prefix of the properties of the extension name
// with the instance key instance: name, or name.instance.
func propertyPrefix(name, instance string) string {
	if instance == "" {
		return name
	}

	return name + "." + instance
}

// ExtensionProperties returns the properties view of the extension whose
// private scope (or a descendant of it) i belongs to, and true. Providers of
// an extension call it with the injector they receive to read their
// configuration: the view is bound to i's container, so a clone created with
// its own InjectorOpts.PropertySource reads from that source. Outside any
// private extension scope, including the root of a flattened extension, it
// returns a view where every key misses, and false.
func ExtensionProperties(i Injector) (Properties, bool) {
	scope := injectorScope(i)
	if scope == nil {
		return Properties{}, false
	}
	root := scope.rootScope
	registry := root.ext.Load()
	if registry == nil {
		return Properties{}, false
	}

	for ; scope != nil && scope.parentScope != nil; scope = scope.parentScope {
		if scope.parentScope != root.self {
			continue
		}
		registry.mu.Lock()
		id, ok := registry.scopes[scope.name]
		entry := registry.entries[id]
		registry.mu.Unlock()
		if ok && entry != nil {
			return Properties{root: root, prefix: propertyPrefix(entry.name, id.instance)}, true
		}
	}

	return Properties{}, false
}
