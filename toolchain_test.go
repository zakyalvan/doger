package doger

import (
	"bufio"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readGoDirectives returns the go and toolchain directive lines of the
// go.mod or go.work file at file, relative to the repository root.
func readGoDirectives(t *testing.T, file string) (goLine string, toolchainLine string) {
	t.Helper()

	f, err := os.Open(file)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "go "):
			goLine = line
		case strings.HasPrefix(line, "toolchain "):
			toolchainLine = line
		}
	}
	require.NoError(t, scanner.Err())

	return goLine, toolchainLine
}

func TestToolchain_GoDirective(t *testing.T) {
	t.Parallel()

	files := []string{
		"go.work",
		"go.mod",
		"http/chi/go.mod",
		"http/echo/go.mod",
		"http/fiber/go.mod",
		"http/gin/go.mod",
		"http/std/go.mod",
		"examples/dag/go.mod",
		"examples/eager-loading/go.mod",
		"examples/event-driven/go.mod",
		"examples/healthcheckable/go.mod",
		"examples/http/chi/go.mod",
		"examples/http/echo/go.mod",
		"examples/http/fiber/go.mod",
		"examples/http/gin/go.mod",
		"examples/http/std/go.mod",
		"examples/interface/go.mod",
		"examples/nested-scope/go.mod",
		"examples/package-system/go.mod",
		"examples/service-aliases/go.mod",
		"examples/service-explanation/go.mod",
		"examples/shutdownable/go.mod",
		"examples/simple/go.mod",
		"examples/struct/go.mod",
		"examples/transient-services/go.mod",
		"examples/web-application/go.mod",
	}

	for _, dir := range readWorkspaceDirs(t, ".") {
		assert.Contains(t, files, path.Join(dir, "go.mod"), "workspace module %q has no row", dir)
	}

	for _, file := range files {
		file := file
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			goLine, toolchainLine := readGoDirectives(t, file)
			is.Equal("go 1.27", goLine)
			is.Empty(toolchainLine, "toolchain directive")
		})
	}
}
