package web

import (
	"encoding/json"
	"html/template"

	"github.com/jleight/meshtender/internal/store"
)

// linkPlatformClient is the per-platform config the shared link-editor JS needs
// (static/link-editor.js), keyed by platform key in the emitted object.
type linkPlatformClient struct {
	Kind        string `json:"kind"`
	Placeholder string `json:"placeholder"`
	Label       bool   `json:"label"` // whether the optional label field applies
}

// LinkPlatformsJS marshals the link platforms into a JSON object (keyed by
// platform key) for the link editor's client JS, delivered CSP-safely via a
// nonce'd inline script that assigns a window global. The data is entirely
// server-controlled (platform descriptors, no user input), so emitting it as
// template.JS — bypassing HTML/JS-string escaping — is safe here.
func LinkPlatformsJS(ps []store.LinkPlatform) template.JS {
	m := make(map[string]linkPlatformClient, len(ps))
	for _, p := range ps {
		m[p.Key] = linkPlatformClient{
			Kind:        string(p.Kind),
			Placeholder: p.Placeholder,
			Label:       p.Kind == store.KindURL,
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return template.JS(b) //nolint:gosec // G203: b is json.Marshal output of server-controlled platform descriptors (Go escapes <>& in JS context)
}
