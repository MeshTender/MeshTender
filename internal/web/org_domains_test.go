package web

import "testing"

func TestNormalizeHostname(t *testing.T) {
	cases := map[string]string{
		"mesh.example.org":         "mesh.example.org",
		"  Mesh.Example.ORG  ":     "mesh.example.org",
		"https://mesh.example.org": "mesh.example.org",
		"http://mesh.example.org/": "mesh.example.org",
		"mesh.example.org:8443":    "mesh.example.org",
		"mesh.example.org/path":    "mesh.example.org",
		"example.org.":             "example.org",
		"localhost":                "", // no dot
		"no_underscores.org":       "", // illegal char
		"has space.org":            "",
		"":                         "",
	}
	for in, want := range cases {
		if got := normalizeHostname(in); got != want {
			t.Errorf("normalizeHostname(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTxtRecordsHaveToken(t *testing.T) {
	tok := "secret-token"
	if !txtRecordsHaveToken([]string{"other", "  secret-token  "}, tok) {
		t.Error("should match token with surrounding whitespace among other records")
	}
	if txtRecordsHaveToken([]string{"nope", "secret-token-extra"}, tok) {
		t.Error("should not match a near-but-different token")
	}
	if txtRecordsHaveToken(nil, tok) {
		t.Error("empty records should not match")
	}
}

func TestHostWithoutPort(t *testing.T) {
	cases := map[string]string{
		"example.org:8080": "example.org",
		"example.org":      "example.org",
		"localhost:8090":   "localhost",
	}
	for in, want := range cases {
		if got := hostWithoutPort(in); got != want {
			t.Errorf("hostWithoutPort(%q) = %q, want %q", in, got, want)
		}
	}
}
