package marketing

import "testing"

func TestOrgCursorRoundTrip(t *testing.T) {
	cases := []orgCursor{
		{Sort: "name", Name: "Acme Mesh", ID: 1},
		{Sort: "members", Count: 0, ID: 0},
		{Sort: "members", Query: "café", Count: 17, ID: 9876543210},
		{Sort: "repeaters", Query: "weird/name?with=specials&  spaces", Count: 3, ID: 42},
		{Sort: "newest", Time: "2026-06-20T12:00:00.123456789Z", ID: 7},
	}
	for _, c := range cases {
		tok := encodeOrgCursor(c)
		got, ok := decodeOrgCursor(tok)
		if !ok || got != c {
			t.Errorf("round-trip(%+v) via %q = (%+v, ok=%v)", c, tok, got, ok)
		}
	}
}

func TestDecodeOrgCursorMalformed(t *testing.T) {
	// A missing or garbage cursor must decode to ok=false, never an error, so a
	// tampered URL just resets paging rather than 500ing.
	for _, tok := range []string{"", "not-base64!!", "Zm9v" /* "foo", not JSON */} {
		if c, ok := decodeOrgCursor(tok); ok {
			t.Errorf("decodeOrgCursor(%q) = (%+v, ok=true), want ok=false", tok, c)
		}
	}
}
