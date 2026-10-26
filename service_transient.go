package doger

import (
	"context"
	"reflect"

	"github.com/kartaladev/doger/stacktrace"
)

var (
	_ serviceWrapper[int]       = (*serviceTransient[int])(nil)
	_ serviceWrapperHealthcheck = (*serviceTransient[int])(nil)
	_ serviceWrapperShutdown    = (*serviceTransient[int])(nil)
	_ serviceWrapperClone       = (*serviceTransient[int])(nil)
)

type serviceTransient[T any] struct {
	opts     *serviceOptions // nil when registered without options
	regIndex uint64          // registration order, unique per root container
	name     string
	typeName string

	// lazy loading
	provider Provider[T]
}

func newServiceTransient[T any](name string, provider Provider[T]) *serviceTransient[T] {
	return &serviceTransient[T]{
		name:     name,
		typeName: inferServiceName[T](),

		provider: provider,
	}
}

func (s *serviceTransient[T]) getName() string {
	return s.name
}

func (s *serviceTransient[T]) getTypeName() string {
	return s.typeName
}

func (s *serviceTransient[T]) getServiceType() ServiceType {
	return ServiceTypeTransient
}

func (s *serviceTransient[T]) getReflectType() reflect.Type {
	return reflect.TypeFor[T]() // if T is a pointer or interface, it will return a typed nil
}

func (s *serviceTransient[T]) getInstanceAny(i Injector) (any, error) {
	return s.getInstance(i)
}

func (s *serviceTransient[T]) getInstance(i Injector) (T, error) {
	if err := resolveDeclaredDependencies(i, s.opts); err != nil {
		return empty[T](), err
	}

	instance, err := handleProviderPanic(s.provider, i)
	if err != nil {
		return empty[T](), err
	}

	onAfterBuild(i, s.name, instance)

	return instance, nil
}

func (s *serviceTransient[T]) isHealthchecker() bool {
	return false
}

func (s *serviceTransient[T]) healthcheck(ctx context.Context) error {
	// @TODO: implement healthcheck ?
	// It requires to store each instance of service, which is not good because of memory leaks.
	return nil
}

func (s *serviceTransient[T]) isShutdowner() bool {
	return false
}

func (s *serviceTransient[T]) shutdown(ctx context.Context) error {
	// @TODO: implement shutdown ?
	// It requires to store each instance of service, which is not good because of memory leaks.
	return nil
}

func (s *serviceTransient[T]) clone(newScope Injector) any {
	return &serviceTransient[T]{
		name:     s.name,
		regIndex: s.regIndex,
		opts:     s.opts,
		typeName: s.typeName,

		provider: s.provider,
	}
}

func (s *serviceTransient[T]) source() (stacktrace.Frame, []stacktrace.Frame) {
	// @TODO: implement stacktrace ?
	// It requires to store each instance of service, which is not good because of memory leaks.
	return stacktrace.Frame{}, []stacktrace.Frame{}
}

func (s *serviceTransient[T]) getRegistrationIndex() uint64 {
	return s.regIndex
}

func (s *serviceTransient[T]) setRegistrationIndex(index uint64) {
	s.regIndex = index
}

func (s *serviceTransient[T]) getOptions() *serviceOptions {
	return s.opts
}

func (s *serviceTransient[T]) setOptions(opts *serviceOptions) {
	s.opts = opts
}
