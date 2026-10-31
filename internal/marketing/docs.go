package marketing

import "net/http"

// pageDocs renders the public help / how-it-works page. It injects the setperm
// command so the example shows MeshTender's real key rather than a placeholder.
func (s *Handlers) pageDocs(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{}
	if s.Identity != nil {
		data["SetPermCommand"] = s.Identity.SetPermCommand()
	}
	s.Render(w, r, "docs.html", data)
}

// pageDocsRegions renders the public explainer on MeshCore regions and scoping,
// written to be linked on its own (e.g. shared with a neighboring mesh).
func (s *Handlers) pageDocsRegions(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, "docs_regions.html", nil)
}
