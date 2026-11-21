package postgres

import "testing"

func TestQuote(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"doger_events":     `"doger_events"`,
		"app.doger_events": `"app"."doger_events"`,
	} {
		if got := quote(in); got != want {
			t.Errorf("quote(%q) = %s, want %s", in, got, want)
		}
	}
}
