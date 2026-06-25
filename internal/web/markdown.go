package web

import (
	"bytes"
	"html/template"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
)

// goldmark with default options does NOT pass raw HTML through (it escapes it),
// so the only HTML in the output is what goldmark itself emits from markdown.
// bluemonday's UGC policy is then applied as defense in depth — it strips any
// dangerous attributes/schemes (e.g. javascript: links) that slip through.
var (
	mdRenderer = goldmark.New()
	mdPolicy   = bluemonday.UGCPolicy()
)

// Markdown renders untrusted markdown (e.g. user-authored repeater docs) into
// sanitized HTML safe to embed in a template. Returns "" on render error.
func Markdown(src string) template.HTML {
	if src == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := mdRenderer.Convert([]byte(src), &buf); err != nil {
		return ""
	}
	return template.HTML(mdPolicy.SanitizeBytes(buf.Bytes()))
}
