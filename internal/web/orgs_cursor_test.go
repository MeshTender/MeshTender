package web

import "testing"

func TestOrgCursorRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		id   int64
	}{
		{"Acme Mesh", 1},
		{"", 0},
		{"weird/name?with=specials&  spaces", 9876543210},
		{"unicode · café ☂", 42},
	}
	for _, c := range cases {
		tok := encodeOrgCursor(c.name, c.id)
		gotName, gotID := decodeOrgCursor(tok)
		if gotName != c.name || gotID != c.id {
			t.Errorf("round-trip(%q,%d) via %q = (%q,%d)", c.name, c.id, tok, gotName, gotID)
		}
	}
}

func TestDecodeOrgCursorMalformed(t *testing.T) {
	// A missing or garbage cursor must decode to the first-page position, never
	// an error, so a tampered URL just resets paging rather than 500ing.
	for _, tok := range []string{"", "not-base64!!", "Zm9v" /* "foo", not JSON */} {
		if name, id := decodeOrgCursor(tok); name != "" || id != 0 {
			t.Errorf("decodeOrgCursor(%q) = (%q,%d), want empty position", tok, name, id)
		}
	}
}
