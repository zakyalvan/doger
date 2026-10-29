package stacktrace

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func example1() (Frame, bool) {
	return NewFrameFromCaller()
}

func provider(i any) (int, error) {
	return 42, nil
}

func example2() (Frame, bool) {
	return NewFrameFromPC(reflect.ValueOf(provider).Pointer())
}

func TestStacktrace(t *testing.T) {
	t.Parallel()
	is := assert.New(t)

	frame, ok := example1()
	is.True(ok)
	is.NotNil(frame)
	is.NotEmpty(frame)
	is.True(strings.HasSuffix(frame.File, "stacktrace/stacktrace_test.go"))
	is.Equal("example1", frame.Function)
	is.Equal(12, frame.Line)
}

func TestNewFrameFromPtr(t *testing.T) {
	t.Parallel()
	is := assert.New(t)

	frame, ok := example2()
	is.True(ok)
	is.NotNil(frame)
	is.NotEmpty(frame)
	is.True(strings.HasSuffix(frame.File, "stacktrace/stacktrace_test.go"))
	is.Equal("provider", frame.Function)
	is.Equal(16, frame.Line)
}

func TestFrame_String(t *testing.T) {
	t.Parallel()
	is := assert.New(t)

	frame, ok := example1()
	is.True(ok)
	is.NotNil(frame)
	is.NotEmpty(frame)
	is.Contains(frame.String(), "stacktrace/stacktrace_test.go:example1:12")
}

func TestShortFuncName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "func", input: "github.com/palantir/shield/package.FuncName", expected: "FuncName"},
		{name: "value_receiver_method", input: "github.com/palantir/shield/package.Receiver.MethodName", expected: "Receiver.MethodName"},
		{name: "pointer_receiver_method", input: "github.com/palantir/shield/package.(*PtrReceiver).MethodName", expected: "PtrReceiver.MethodName"},
		// Go may collapse generic type arguments to "[...]" in runtime function names.
		// Regardless of the exact formatting, shortFuncName should only strip the
		// pointer-receiver prefix, not "*" characters inside type arguments.
		{name: "generic_collapsed_type_args", input: "github.com/samber/do/v2.(*ServiceLazy[...]).getInstance", expected: "ServiceLazy[...].getInstance"},
		{name: "generic_pointer_type_arg", input: "github.com/samber/do/v2.(*ServiceLazy[*int]).getInstance", expected: "ServiceLazy[*int].getInstance"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			is.Equal(tt.expected, shortFuncName(tt.input))
		})
	}
}

func TestNewFrameFromPC_ZeroPC(t *testing.T) {
	t.Parallel()
	is := assert.New(t)

	var (
		frame Frame
		ok    bool
	)
	is.NotPanics(func() {
		frame, ok = NewFrameFromPC(0)
	})
	is.False(ok)
	is.Equal(Frame{}, frame)
}
