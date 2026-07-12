package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/andybalholm/brotli"
)

// staticAsset is one embedded static file, fingerprinted by content hash and
// pre-compressed. The assets are immutable (their URL changes when their bytes
// do), so we compress them once at startup with the best gzip/brotli levels and
// serve the pre-built bytes — no per-request compression CPU.
type staticAsset struct {
	logical     string // "ui.js"
	hashedName  string // "ui.<hash>.js"
	contentType string
	raw         []byte
	gzip        []byte // nil when compression didn't shrink the file
	brotli      []byte // nil when compression didn't shrink the file
}

// negotiable reports whether the asset has any pre-compressed variant, i.e.
// whether its response varies on Accept-Encoding.
func (a *staticAsset) negotiable() bool { return a.gzip != nil || a.brotli != nil }

// assetManifest fingerprints the embedded static assets by content hash so their
// URLs change whenever their bytes do. That lets us serve a fingerprinted URL
// with a one-year immutable Cache-Control: the browser never revalidates, and a
// deploy that changes a file automatically busts the cache via a new URL. The
// embedded FS carries a zero ModTime, so without this the assets have no
// validators at all (no ETag/Last-Modified) and fall back to heuristic caching.
type assetManifest struct {
	byLogical map[string]*staticAsset // "ui.js" -> asset (template lookups, old links)
	byHashed  map[string]*staticAsset // "ui.<hash>.js" -> asset (fingerprinted requests)
}

// assets is the process-wide manifest, built from the embedded static FS at
// package init. The FS is embedded and deterministic, so any failure here is a
// build/programming error — panic to fail fast at startup (and in tests).
var assets = buildAssetManifest(staticFS, "static")

func buildAssetManifest(fsys fs.FS, dir string) *assetManifest {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("web: static sub FS: " + err.Error())
	}
	m := &assetManifest{
		byLogical: map[string]*staticAsset{},
		byHashed:  map[string]*staticAsset{},
	}
	err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		a := newStaticAsset(p, data, hex.EncodeToString(sum[:])[:8])
		m.byLogical[a.logical] = a
		m.byHashed[a.hashedName] = a
		return nil
	})
	if err != nil {
		panic("web: build asset manifest: " + err.Error())
	}
	return m
}

func newStaticAsset(logical string, raw []byte, hash string) *staticAsset {
	a := &staticAsset{
		logical:     logical,
		hashedName:  fingerprintName(logical, hash),
		contentType: assetContentType(logical, raw),
		raw:         raw,
	}
	// Only compress text-ish payloads, and only keep a variant when it actually
	// shrinks the file (a tiny or already-dense asset can grow).
	if compressibleType(a.contentType) {
		if g := gzipBytes(raw); len(g) < len(raw) {
			a.gzip = g
		}
		if b := brotliBytes(raw); len(b) < len(raw) {
			a.brotli = b
		}
	}
	return a
}

// fingerprintName inserts the content hash before the final extension, preserving
// any directory prefix: "tabler.min.css" -> "tabler.min.<hash>.css".
func fingerprintName(p, hash string) string {
	dir, file := path.Split(p)
	ext := path.Ext(file)
	return dir + strings.TrimSuffix(file, ext) + "." + hash + ext
}

// assetContentType mirrors http.FileServer: extension first, sniff as fallback.
func assetContentType(name string, raw []byte) string {
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		return ct
	}
	return http.DetectContentType(raw)
}

// compressibleType reports whether a content type benefits from text compression.
// Binary/already-compressed types (images, fonts, archives) are skipped.
func compressibleType(ct string) bool {
	ct = strings.ToLower(ct)
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	for _, tok := range []string{"javascript", "json", "xml", "svg", "css"} {
		if strings.Contains(ct, tok) {
			return true
		}
	}
	return false
}

func gzipBytes(raw []byte) []byte {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	return buf.Bytes()
}

func brotliBytes(raw []byte) []byte {
	var buf bytes.Buffer
	bw := brotli.NewWriterLevel(&buf, brotli.BestCompression)
	_, _ = bw.Write(raw)
	_ = bw.Close()
	return buf.Bytes()
}

// URL maps a logical asset reference ("/static/ui.js") to its fingerprinted URL
// ("/static/ui.<hash>.js"). Unknown paths pass through unchanged so a stray
// reference degrades to a working (un-immutable) URL rather than a 404;
// TestTemplatesUseAssetHelper guards against introducing those.
func (m *assetManifest) URL(logical string) string {
	name := strings.TrimPrefix(logical, "/static/")
	if a, ok := m.byLogical[name]; ok {
		return "/static/" + a.hashedName
	}
	return logical
}

// serveHTTP serves a static asset. It expects the "/static/" prefix already
// stripped (see SharedRoutes). A fingerprinted name is served with a one-year
// immutable Cache-Control; the logical name is served without it (an old link
// still works). Either way the best pre-compressed variant the client accepts is
// returned. Unknown names 404. Range requests are not supported — these assets
// are small scripts/styles that browsers fetch whole.
func (m *assetManifest) serveHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	a, fingerprinted := m.byHashed[name]
	if !fingerprinted {
		a = m.byLogical[name]
	}
	if a == nil {
		http.NotFound(w, r)
		return
	}

	h := w.Header()
	h.Set("Content-Type", a.contentType)
	if fingerprinted {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	if a.negotiable() {
		// Even when we end up serving identity, the response varies by encoding —
		// shared caches must key on it.
		h.Set("Vary", "Accept-Encoding")
	}

	body := a.raw
	ae := r.Header.Get("Accept-Encoding")
	switch {
	case a.brotli != nil && acceptsEncoding(ae, "br"):
		h.Set("Content-Encoding", "br")
		body = a.brotli
	case a.gzip != nil && acceptsEncoding(ae, "gzip"):
		h.Set("Content-Encoding", "gzip")
		body = a.gzip
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))

	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// acceptsEncoding reports whether an Accept-Encoding header accepts the given
// content coding, honoring an explicit "coding;q=0" refusal.
func acceptsEncoding(header, coding string) bool {
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(part, ";")
		if !strings.EqualFold(strings.TrimSpace(fields[0]), coding) {
			continue
		}
		for _, f := range fields[1:] {
			if v, ok := strings.CutPrefix(strings.TrimSpace(f), "q="); ok {
				if q, err := strconv.ParseFloat(v, 64); err == nil && q == 0 {
					return false
				}
			}
		}
		return true
	}
	return false
}
