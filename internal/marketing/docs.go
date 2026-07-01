package marketing

import "net/http"

// pageDocs renders the public help / how-it-works page. It injects MeshTender's
// server public key so the setperm example shows the exact command, with the
// real key filled in, rather than a placeholder.
func (s *Handlers) pageDocs(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{}
	if s.Identity != nil {
		data["ServerKey"] = s.Identity.PublicKeyHex()
		data["SetPermCommand"] = s.Identity.SetPermCommand()
	}
	s.Render(w, r, "docs.html", data)
}
