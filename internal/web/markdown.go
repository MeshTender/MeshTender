package web

import (
	"bytes"
	"html/template"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"golang.org/x/net/html"
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
	return template.HTML(mdPolicy.SanitizeBytes(buf.Bytes())) //nolint:gosec // G203: output is bluemonday-sanitized by mdPolicy
}

// MarkdownText flattens markdown to a single line of plain text — the formatting
// (emphasis, headings, list bullets, link syntax) stripped, leaving just the
// words. Used for compact teasers (e.g. org directory cards) where rendered
// markdown would be out of place. Returns the source unchanged on parse error.
func MarkdownText(src string) string {
	if src == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := mdRenderer.Convert([]byte(src), &buf); err != nil {
		return src
	}
	doc, err := html.Parse(&buf)
	if err != nil {
		return src
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			// Separate every text run with a space; block boundaries otherwise glue
			// adjacent words together ("one"+"two"). Runs collapse below.
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return strings.Join(strings.Fields(b.String()), " ")
}
