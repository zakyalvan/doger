package doger_test

import (
	"strings"
	"testing"

	"github.com/kartaladev/doger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type explainFrameDB struct{}

func (*explainFrameDB) HealthCheck() error { return nil }

func newExplainFrameDB(doger.Injector) (*explainFrameDB, error) {
	return &explainFrameDB{}, nil
}

func TestExplainService_ProviderFrame(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		register func(t *testing.T, i doger.Injector)
		explain  func(i doger.Injector) (doger.ExplainServiceOutput, bool)
		file     string
		function string
	}{
		{
			name: "eager",
			register: func(t *testing.T, i doger.Injector) {
				doger.ProvideValue(i, &explainFrameDB{})
			},
			explain:  doger.ExplainService[*explainFrameDB],
			file:     "/service_eager.go",
			function: "newServiceEager",
		},
		{
			name: "alias",
			register: func(t *testing.T, i doger.Injector) {
				doger.Provide(i, newExplainFrameDB)
				require.NoError(t, doger.As[*explainFrameDB, doger.Healthchecker](i))
			},
			explain:  doger.ExplainService[doger.Healthchecker],
			file:     "/service_alias.go",
			function: "newServiceAlias",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)
			must := require.New(t)

			i := doger.New()
			tt.register(t, i)

			output, ok := tt.explain(i)
			must.True(ok)
			must.NotNil(output.Invoked)
			is.True(strings.HasSuffix(output.Invoked.File, tt.file), "Invoked.File = %q", output.Invoked.File)
			is.True(strings.HasPrefix(output.Invoked.Function, tt.function), "Invoked.Function = %q", output.Invoked.Function)
			line := invokedLine(output.String())
			is.NotEmpty(line)
			is.NotContains(line, "stacktrace.go:NewFrameFromCaller")
		})
	}
}

// invokedLine returns the "Invoked:" line of an ExplainServiceOutput string.
func invokedLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "Invoked:") {
			return line
		}
	}
	return ""
}
