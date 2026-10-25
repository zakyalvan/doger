package dogerhttp

import (
	"regexp"
	"strings"
	"testing"

	"github.com/kartaladev/doger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var htmlTitleRe = regexp.MustCompile(`<title>(.*?)</title>`)

// htmlTitle returns the text of the first <title> element of page.
func htmlTitle(t *testing.T, page string) string {
	t.Helper()

	m := htmlTitleRe.FindStringSubmatch(page)
	require.Len(t, m, 2, "page has no <title>")

	return m[1]
}

// renderPage renders one of the debug UI pages for a root scope with the
// value "db-value" registered as "db-service".
func renderPage(t *testing.T, page string) string {
	t.Helper()

	const basePath = "/debug/do"
	root := doger.New()
	doger.ProvideNamedValue(root, "db-service", "db-value")

	var (
		html string
		err  error
	)
	switch page {
	case "index":
		html, err = IndexHTML(basePath)
	case "scope_tree":
		html, err = ScopeTreeHTML(basePath, root, "")
	case "service_list":
		html, err = ServiceListHTML(basePath, root)
	case "service_detail":
		html, err = ServiceHTML(basePath, root, root.ID(), "db-service")
	default:
		require.FailNowf(t, "unknown page", "%q", page)
	}
	require.NoError(t, err)

	return html
}

func TestIndexHTML(t *testing.T) {
	t.Parallel()
	is := assert.New(t)

	basePath := "/debug/di"
	html, err := IndexHTML(basePath)
	is.NoError(err)
	is.NotEmpty(html)
	is.Contains(html, "Welcome to doger UI")
	is.Equal("Dependency injection UI - doger", htmlTitle(t, html))
	is.Contains(html, basePath+"/scope")
	is.Contains(html, basePath+"/service")
}

func TestIndexHTML_Branding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		label string
		href  string
	}{
		{label: "Repository", href: "https://github.com/kartaladev/doger"},
		{label: "New issue", href: "https://github.com/kartaladev/doger/issues"},
		{label: "Documentation", href: "https://kartaladev.github.io/doger/"},
		{label: "Godoc", href: "https://pkg.go.dev/github.com/kartaladev/doger"},
		{label: "Changelog", href: "https://github.com/kartaladev/doger/releases"},
		{label: "License", href: "https://github.com/kartaladev/doger/blob/main/LICENSE"},
		{label: "Upstream project", href: "https://github.com/samber/do"},
	}

	html, err := IndexHTML("/debug/do")
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			t.Parallel()

			link := `<a href="` + regexp.QuoteMeta(tt.href) + `"[^>]*>` + regexp.QuoteMeta(tt.label) + `</a>`
			assert.Regexp(t, link, html)
		})
	}
}

func TestIndexHTML_BasePathLinks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		base string
	}{
		{name: "debug_do", base: "/debug/do"},
		{name: "admin_di", base: "/admin/di"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			html, err := IndexHTML(tt.base)
			is.NoError(err)
			is.Contains(html, `href="`+tt.base+`/scope"`)
			is.Contains(html, `href="`+tt.base+`/service"`)
		})
	}
}

func TestScopeTreeHTML_Basic(t *testing.T) {
	t.Parallel()
	is := assert.New(t)

	basePath := "/debug/di"
	root := doger.New()
	child := root.Scope("child")

	doger.ProvideNamedValue(root, "db-service", "db-value")
	doger.ProvideNamedTransient(root, "request-id", func(i doger.Injector) (string, error) { return "rid", nil })
	doger.ProvideNamedValue(child, "cache-service", 123)

	html, err := ScopeTreeHTML(basePath, root, "")
	is.NoError(err)
	is.NotEmpty(html)
	is.Contains(html, "Scope description")
	// Links to scopes and services should be present
	is.Contains(html, basePath+"/scope")
	is.True(strings.Contains(html, "db-service") || strings.Contains(html, "cache-service"))
}

func TestServiceListHTML_Basic(t *testing.T) {
	t.Parallel()
	is := assert.New(t)

	basePath := "/debug/di"
	root := doger.New()
	_ = root // ensure non-nil
	doger.ProvideNamedValue(root, "cfg", "x")

	html, err := ServiceListHTML(basePath, root)
	is.NoError(err)
	is.NotEmpty(html)
	is.Contains(html, "Service description")
	is.Contains(html, basePath+"/service")
}

func TestServiceHTML_Basic(t *testing.T) {
	t.Parallel()
	is := assert.New(t)

	basePath := "/debug/di"
	root := doger.New()
	doger.ProvideNamedValue(root, "cfg", "x")

	html, err := ServiceHTML(basePath, root, root.ID(), "cfg")
	is.NoError(err)
	is.NotEmpty(html)
	is.Contains(html, "Scope id:")
	is.Contains(html, "Service name: cfg")
	// eager value should report eager type
	is.Contains(html, "Service type: eager")
}

func TestPagesHTML_Title(t *testing.T) {
	t.Parallel()

	tests := []struct {
		page  string
		title string
	}{
		{page: "scope_tree", title: "Inspect scope tree - doger"},
		{page: "service_list", title: "Inspect service - doger"},
		{page: "service_detail", title: "Inspect service - doger"},
	}

	for _, tt := range tests {
		t.Run(tt.page, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.title, htmlTitle(t, renderPage(t, tt.page)))
		})
	}
}

func TestPagesHTML_UpstreamAttributionOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		page  string
		count int
	}{
		{page: "index", count: 1},
		{page: "scope_tree", count: 0},
		{page: "service_list", count: 0},
		{page: "service_detail", count: 0},
	}

	for _, tt := range tests {
		t.Run(tt.page, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.count, strings.Count(renderPage(t, tt.page), "samber/do"))
		})
	}
}
