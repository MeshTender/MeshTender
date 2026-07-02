package web

import (
	"testing"
	"time"
)

type testCursor struct {
	Name string    `json:"n"`
	ID   int64     `json:"i"`
	At   time.Time `json:"t"`
}

func TestCursorRoundTrip(t *testing.T) {
	want := testCursor{Name: "repeater-alpha", ID: 42, At: time.Unix(1_700_000_000, 0).UTC()}
	got, ok := DecodeCursor[testCursor](EncodeCursor(want))
	if !ok {
		t.Fatal("DecodeCursor: ok=false for a token we just encoded")
	}
	if got.Name != want.Name || got.ID != want.ID || !got.At.Equal(want.At) {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, want)
	}
}

func TestCursorTokenIsURLSafe(t *testing.T) {
	// RawURLEncoding must not emit '+', '/', or '=' — the token rides in a query
	// parameter unescaped.
	tok := EncodeCursor(testCursor{Name: "a/b+c==", ID: 1})
	for _, r := range tok {
		switch r {
		case '+', '/', '=':
			t.Fatalf("token %q contains non-URL-safe rune %q", tok, r)
		}
	}
}

func TestDecodeCursorRejectsBadInput(t *testing.T) {
	// A missing, malformed-base64, or malformed-JSON token must reset to the zero
	// value with ok=false so a tampered URL just pages from the start.
	cases := map[string]string{
		"empty":           "",
		"not base64":      "!!!not base64!!!",
		"base64 non-json": EncodeCursor([]byte("plain string, not this struct's JSON")),
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := DecodeCursor[testCursor](tok)
			if ok {
				t.Fatalf("ok=true for %s input %q", name, tok)
			}
			if got != (testCursor{}) {
				t.Fatalf("non-zero cursor %+v for %s input", got, name)
			}
		})
	}
}
