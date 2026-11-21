package doger

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync/atomic"
	"time"

	typetostring "github.com/samber/go-type-to-string"
)

//
// This file could be replaced with a dependency on a library like samber/lo, but I wanted to keep the dependencies to a minimum.
//

func empty[T any]() (t T) {
	return t
}

// deepEmpty returns the zero value of T with every pointer of a pointer
// chain allocated, or an error matching ErrStructInjection when T is a
// recursive pointer type.
func deepEmpty[T any]() (T, error) {
	v, err := deepEmptyMakeValue(reflect.TypeFor[T](), nil) // reflect.Value with the desired shape
	if err != nil {
		return empty[T](), err
	}
	typed, _ := reflect.TypeAssert[T](v) // a nil interface gives the zero value

	return typed, nil
}

// deepEmptyMakeValue returns the zero value of t, with every pointer of a
// pointer chain allocated. visited holds the pointer types already walked:
// a recursive pointer type returns an error matching ErrStructInjection
// instead of recursing without bound.
func deepEmptyMakeValue(t reflect.Type, visited []reflect.Type) (reflect.Value, error) {
	// Base case: not a pointer -> just zero of this type.
	if t.Kind() != reflect.Pointer {
		return reflect.Zero(t), nil
	}
	if slices.Contains(visited, t) {
		return reflect.Value{}, structInjectionError("recursive pointer type `%s` cannot be injected", typetostring.GetReflectType(t))
	}

	// Recursive case: pointer -> allocate pointer, set its Elem to the
	// recursively-constructed zero value of the element type.
	elem, err := deepEmptyMakeValue(t.Elem(), append(visited, t))
	if err != nil {
		return reflect.Value{}, err
	}
	p := reflect.New(t.Elem())
	p.Elem().Set(elem)
	return p.Convert(t), nil // a named pointer type, such as type P *S, is not *S
}

func must0(err error) {
	if err != nil {
		panic(err)
	}
}

func must1[A any](a A, err error) A {
	if err != nil {
		panic(err)
	}

	return a
}

func keys[K comparable, V any](in map[K]V) []K {
	result := make([]K, 0, len(in))

	for k := range in {
		result = append(result, k)
	}

	return result
}

func flatten[T any](collection [][]T) []T {
	totalLen := 0
	for i := range collection {
		totalLen += len(collection[i])
	}

	result := make([]T, 0, totalLen)
	for i := range collection {
		result = append(result, collection[i]...)
	}

	return result
}

func mAp[T any, R any](collection []T, iteratee func(T, int) R) []R {
	result := make([]R, len(collection))

	for i, item := range collection {
		result[i] = iteratee(item, i)
	}

	return result
}

func typeIsAssignable[T, AssignTo any]() bool {
	_, ok := any((*T)(nil)).(*AssignTo)
	return ok
}

func filter[V any](collection []V, predicate func(item V, index int) bool) []V {
	result := make([]V, 0, len(collection))

	for i, item := range collection {
		if predicate(item, i) {
			result = append(result, item)
		}
	}

	return result
}

func reverseSlice[S ~[]E, E any](s S) {
	slices.Reverse(s)
}

func orderedUniq[V comparable](in []V) []V {
	out := make([]V, 0, len(in))
	present := make(map[V]struct{}, len(in))

	for _, v := range in {
		if _, ok := present[v]; !ok {
			out = append(out, v)
			present[v] = struct{}{}
		}
	}

	return out
}

func contains[T comparable](list []T, elem T) bool {
	return slices.Contains(list, elem)
}

// applyOptions applies opts to target in order, skipping nil ones.
func applyOptions[T any, O ~func(*T)](target *T, opts []O) {
	for _, opt := range opts {
		if opt != nil {
			opt(target)
		}
	}
}

// loadOrInit returns the value of p, storing the one init returns first
// when p holds none. Concurrent callers all get the value stored first.
func loadOrInit[T any](p *atomic.Pointer[T], init func() *T) *T {
	if value := p.Load(); value != nil {
		return value
	}
	p.CompareAndSwap(nil, init())

	return p.Load()
}

func coalesce[T comparable](v ...T) T {
	return cmp.Or(v...)
}

// https://gist.github.com/rkravchik/d9733e1d2d626188eb91df751471d739
func newUUID() (string, error) {
	var uuid [16]byte
	n, err := rand.Read(uuid[:])
	if n != len(uuid) || err != nil {
		return "", err
	}
	// variant bits; see section 4.1.1
	uuid[8] = uuid[8]&^0xc0 | 0x80
	// version 4 (pseudo-random); see section 4.1.3
	uuid[6] = uuid[6]&^0xf0 | 0x40

	return formatUUID(uuid), nil
}

// formatUUID returns the canonical text of uuid: lowercase hex grouped
// 8-4-4-4-12.
func formatUUID(uuid [16]byte) string {
	var buf [36]byte
	buf[8], buf[13], buf[18], buf[23] = '-', '-', '-', '-'
	hex.Encode(buf[0:8], uuid[0:4])
	hex.Encode(buf[9:13], uuid[4:6])
	hex.Encode(buf[14:18], uuid[6:8])
	hex.Encode(buf[19:23], uuid[8:10])
	hex.Encode(buf[24:36], uuid[10:16])
	return string(buf[:])
}

// healthCheckResult decides an observed health-check result: once ctx is
// done, or once clock has reached the deadline the container set for the
// check, the check counts as timed out, whatever it returned; before that,
// the check keeps its own result.
func healthCheckResult(ctx context.Context, clock Clock, err error) error {
	if cause := contextDone(ctx); cause != nil {
		return timeoutError(cause)
	}

	// The context may not be marked done yet when the check returned at its
	// deadline: its cancellation runs in another goroutine.
	if deadline, ok := healthCheckDeadline(ctx); ok && !clock.Now().Before(deadline) {
		return timeoutError(context.DeadlineExceeded)
	}

	return err
}

// healthCheckDeadlineKey is the context key of the earliest health-check
// deadline the container set, on its clock.
type healthCheckDeadlineKey struct{}

// withHealthCheckDeadline records a health-check deadline set by the
// container, keeping an earlier one already recorded in ctx.
func withHealthCheckDeadline(ctx context.Context, deadline time.Time) context.Context {
	if current, ok := healthCheckDeadline(ctx); ok && !deadline.Before(current) {
		return ctx
	}

	return context.WithValue(ctx, healthCheckDeadlineKey{}, deadline)
}

// healthCheckDeadline returns the health-check deadline the container
// recorded in ctx. A deadline set by the consumer's context is not one.
func healthCheckDeadline(ctx context.Context) (time.Time, bool) {
	deadline, ok := ctx.Value(healthCheckDeadlineKey{}).(time.Time)
	return deadline, ok
}

// timeoutError returns an error matching both ErrHealthCheckTimeout and
// cause. A deadline cause is normalized to context.DeadlineExceeded, so the
// message reads the same whichever clock set the deadline.
func timeoutError(cause error) error {
	if errors.Is(cause, context.DeadlineExceeded) {
		cause = context.DeadlineExceeded
	}

	return fmt.Errorf("%w: %w", ErrHealthCheckTimeout, cause)
}

// contextDone returns ctx.Err() once ctx is done, and nil while it is live.
// It never calls Err() on a live context, which a consumer's context may
// block on.
func contextDone(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// Previously, we used to perform check like this:
// _, ok := any(empty[Initial]()).(Alias)
// But it was not working when Initial was an interface, so we now
// use reflection to check if Initial implements Alias.
func genericCanCastToGeneric[From any, To any]() bool {
	var from From
	anyFrom := any(from)

	if anyFrom == nil { // check for interface by reflect
		typeFrom := reflect.TypeOf(&from).Elem()
		typeTo := reflect.TypeFor[To]()

		toInterface := typeTo.Kind() == reflect.Interface

		return toInterface && typeFrom.Implements(typeTo)
	}

	_, ok := anyFrom.(To)
	return ok
}

func typeCanCastToGeneric[To any](fromType reflect.Type) bool {
	toType := reflect.TypeFor[To]()
	return typeCanCastToType(fromType, toType)
}

func typeCanCastToType(fromType reflect.Type, toType reflect.Type) bool {
	if fromType == nil {
		return false
	}

	if fromType == toType {
		return true
	}

	// Check assignable
	if fromType.AssignableTo(toType) {
		return true
	}

	return false
}

// sameInstance reports whether a and b are the same value; values of a
// type that cannot be compared are never the same.
func sameInstance(a, b any) bool {
	if a == nil || b == nil || !reflect.TypeOf(a).Comparable() || !reflect.TypeOf(b).Comparable() {
		return false
	}

	return a == b
}
