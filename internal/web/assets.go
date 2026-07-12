package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// assetManifest fingerprints the embedded static assets by content hash so their
// URLs change whenever their bytes do. That lets us serve a fingerprinted URL
// with a one-year immutable Cache-Control: the browser never revalidates, and a
// deploy that changes a file automatically busts the cache via a new URL. The
// embedded FS carries a zero ModTime, so without this the assets have no
// validators at all (no ETag/Last-Modified) and fall back to heuristic caching.
type assetManifest struct {
	byLogical  map[string]string // "ui.js" -> "ui.<hash>.js" (template lookups)
	byHashed   map[string]string // "ui.<hash>.js" -> "ui.js" (request resolution)
	fileServer http.Handler      // serves the underlying (un-fingerprinted) files
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
		byLogical:  map[string]string{},
		byHashed:   map[string]string{},
		fileServer: http.FileServer(http.FS(sub)),
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
		hashed := fingerprintName(p, hex.EncodeToString(sum[:])[:8])
		m.byLogical[p] = hashed
		m.byHashed[hashed] = p
		return nil
	})
	if err != nil {
		panic("web: build asset manifest: " + err.Error())
	}
	return m
}

// fingerprintName inserts the content hash before the final extension, preserving
// any directory prefix: "tabler.min.css" -> "tabler.min.<hash>.css".
func fingerprintName(p, hash string) string {
	dir, file := path.Split(p)
	ext := path.Ext(file)
	return dir + strings.TrimSuffix(file, ext) + "." + hash + ext
}

// URL maps a logical asset reference ("/static/ui.js") to its fingerprinted URL
// ("/static/ui.<hash>.js"). Unknown paths pass through unchanged so a stray
// reference degrades to a working (un-immutable) URL rather than a 404;
// TestTemplatesUseAssetHelper guards against introducing those.
func (m *assetManifest) URL(logical string) string {
	name := strings.TrimPrefix(logical, "/static/")
	if hashed, ok := m.byLogical[name]; ok {
		return "/static/" + hashed
	}
	return logical
}

// serveHTTP serves a static asset. It expects the "/static/" prefix already
// stripped (see SharedRoutes). A recognized fingerprinted name is served with a
// one-year immutable Cache-Control after rewriting the request to the real file;
// any other name (an un-fingerprinted URL, an old link, a stale hash) is served
// as-is — a stale hash simply 404s through the file server.
func (m *assetManifest) serveHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if real, ok := m.byHashed[name]; ok {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		r = cloneWithPath(r, "/"+real)
	}
	m.fileServer.ServeHTTP(w, r)
}

// cloneWithPath returns a shallow copy of r with URL.Path rewritten, without
// mutating the caller's request (the URL is copied too).
func cloneWithPath(r *http.Request, p string) *http.Request {
	r2 := new(http.Request)
	*r2 = *r
	u := new(url.URL)
	*u = *r.URL
	u.Path = p
	r2.URL = u
	return r2
}
