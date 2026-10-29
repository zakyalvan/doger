package stacktrace

///
/// Stolen from palantir/stacktrace repo
/// -> https://github.com/palantir/stacktrace/blob/master/cleanpath/gopath_test.go
/// -> Apache 2.0 LICENSE
///

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRemoveGoPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		gopath   []string
		path     string
		expected string
	}{
		{
			name:     "empty_gopath",
			gopath:   []string{},
			path:     "/some/dir/src/pkg/prog.go",
			expected: "/some/dir/src/pkg/prog.go",
		},
		{
			name:     "single_matching_dir",
			gopath:   []string{"/some/dir"},
			path:     "/some/dir/src/pkg/prog.go",
			expected: "pkg/prog.go",
		},
		{
			name:     "nonmatching_dir",
			gopath:   []string{"/other/dir"},
			path:     "/some/dir/src/pkg/prog.go",
			expected: "/some/dir/src/pkg/prog.go",
		},
		{
			name:     "multiple_matching_dirs_shorter_first",
			gopath:   []string{"/some", "/some/src/dir"},
			path:     "/some/src/dir/src/pkg/prog.go",
			expected: "pkg/prog.go",
		},
		{
			name:     "multiple_matching_dirs_longer_first",
			gopath:   []string{"/some/src/dir", "/some"},
			path:     "/some/src/dir/src/pkg/prog.go",
			expected: "pkg/prog.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dirs := make([]string, len(tt.gopath))
			copy(dirs, tt.gopath)
			sort.Stable(longestFirst(dirs))

			cleaned := removeGoPathDirs(tt.path, dirs)
			assert.Equal(t, tt.expected, cleaned, "testcase: %+v", tt)
		})
	}
}
