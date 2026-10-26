package web

import (
	"strings"
	"testing"
)

// execPart runs one shared partial with data.
func execPart(t *testing.T, name string, data any) string {
	t.Helper()
	var b strings.Builder
	if err := testEnv(t).Renderer.pages["error.html"].ExecuteTemplate(&b, name, data); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return b.String()
}

func TestPubkeyPartial(t *testing.T) {
	t.Parallel()
	key := strings.Repeat("ab", 32)
	got := execPart(t, "pubkey", key)
	for _, want := range []string{`>abababab…<`, `title="` + key + `"`, `data-copy="` + key + `"`, `aria-label="Copy public key"`} {
		if !strings.Contains(got, want) {
			t.Errorf("pubkey lacks %s: %s", want, got)
		}
	}
	if short := execPart(t, "pubkey", "abc"); !strings.Contains(short, ">abc<") {
		t.Errorf("a short key isn't shown whole: %s", short)
	}
}

func TestEmptyStatePartial(t *testing.T) {
	t.Parallel()
	dict := func(pairs ...any) map[string]any {
		m, err := Dict(pairs...)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	full := execPart(t, "empty-state", dict("Title", "No companions yet", "Line", "Register the radio you carry.",
		"ActionHref", "/companions/new", "ActionLabel", "Register companion"))
	for _, want := range []string{`class="empty"`, "No companions yet", "Register the radio you carry.", `href="/companions/new"`, "Register companion", "btn-primary"} {
		if !strings.Contains(full, want) {
			t.Errorf("full empty state lacks %s", want)
		}
	}
	compact := execPart(t, "empty-state", dict("Title", "No pending links", "Compact", true))
	if !strings.Contains(compact, "card-body") || strings.Contains(compact, `class="empty"`) || strings.Contains(compact, "<a ") {
		t.Errorf("compact empty state, no action: %s", compact)
	}
}
