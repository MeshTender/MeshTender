package core

import (
	"net/http/httptest"
	"testing"

	"github.com/jleight/meshtender/internal/config"
	"github.com/jleight/meshtender/internal/web"
)

// TestAbsoluteURLIgnoresForwardedProto: the share-link base URL takes its scheme
// from the trusted config, not the client-controlled X-Forwarded-Proto header, so
// a spoofed header can't forge an https link on a plain-http deployment. It also
// preserves the request host and port.
func TestAbsoluteURLIgnoresForwardedProto(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		secure bool
		host   string
		want   string
	}{
		{"insecure config, spoofed proto", false, "app.example.com", "http://app.example.com/invite/tok"},
		{"secure config", true, "app.example.com", "https://app.example.com/invite/tok"},
		{"host with port preserved", true, "app.example.com:8443", "https://app.example.com:8443/invite/tok"},
	}
	for _, c := range cases {
		h := &Handlers{Env: &web.Env{Cfg: &config.Config{Secure: c.secure}}}
		r := httptest.NewRequest("GET", "http://"+c.host+"/repeaters/x/share", nil)
		r.Host = c.host
		r.Header.Set("X-Forwarded-Proto", "https") // spoofed; must be ignored
		if got := h.absoluteURL(r, "/invite/tok"); got != c.want {
			t.Errorf("%s: absoluteURL = %q, want %q", c.name, got, c.want)
		}
	}
}
