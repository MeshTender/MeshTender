//go:build browser

package e2e

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/jleight/meshtender/internal/store"
)

// contrastProbe measures the WCAG contrast ratio of every visible piece of text on the
// page, against its actual rendered background, and returns the ones that fail AA.
//
// It runs in the browser because contrast is a property of what's *rendered*: the
// foreground may be translucent, the background usually comes from an ancestor, and both
// are resolved from CSS variables that only exist at runtime. Nothing about this is
// checkable by reading the stylesheets.
const contrastProbe = `(() => {
  // --- colour maths, per WCAG 2.1 -------------------------------------------------
  // Chrome serialises computed colours in more than one syntax: plain rgb()/rgba(),
  // and color(srgb r g b / a) with 0..1 components for values that came through
  // colour-mixing or a wide-gamut source. An earlier version of this probe only
  // matched rgb() and SILENTLY SKIPPED the rest — which hid every failing link on the
  // site, since Tabler's link colour arrives in color(srgb ...) form. Anything still
  // unparseable is now reported rather than ignored (see unparsed below).
  function parse(css) {
    let m = css.match(/^\s*color\(\s*srgb\s+([^)]+)\)/i);
    if (m) {
      const p = m[1].split(/[\s/]+/).filter(Boolean).map(Number);
      if (p.length < 3 || p.some(isNaN)) return null;
      return { r: p[0] * 255, g: p[1] * 255, b: p[2] * 255, a: p.length > 3 ? p[3] : 1 };
    }
    m = css.match(/rgba?\(([^)]+)\)/);
    if (m) {
      const p = m[1].split(/[,\s/]+/).filter(Boolean).map(Number);
      if (p.length < 3 || p.some(isNaN)) return null;
      return { r: p[0], g: p[1], b: p[2], a: p.length > 3 ? p[3] : 1 };
    }
    if (/^\s*transparent\s*$/i.test(css)) return { r: 0, g: 0, b: 0, a: 0 };
    return null;
  }
  function over(fg, bg) { // alpha-composite fg onto an opaque bg
    return {
      r: fg.a * fg.r + (1 - fg.a) * bg.r,
      g: fg.a * fg.g + (1 - fg.a) * bg.g,
      b: fg.a * fg.b + (1 - fg.a) * bg.b,
      a: 1,
    };
  }
  function luminance(c) {
    const f = (v) => {
      v /= 255;
      return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
    };
    return 0.2126 * f(c.r) + 0.7152 * f(c.g) + 0.0722 * f(c.b);
  }
  function ratio(a, b) {
    const la = luminance(a), lb = luminance(b);
    return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
  }

  // --- the background a pixel of text actually sits on ---------------------------
  // Walks ancestors compositing translucent layers until something opaque is found.
  // Returns null when an ancestor paints an image or gradient, since the effective
  // colour then isn't knowable from computed style alone.
  function backdrop(el) {
    let layers = [];
    for (let n = el; n; n = n.parentElement) {
      const cs = getComputedStyle(n);
      if (cs.backgroundImage && cs.backgroundImage !== "none") return null;
      const c = parse(cs.backgroundColor);
      if (!c || c.a === 0) continue;
      layers.push(c);
      if (c.a === 1) {
        // Composite from the opaque base upward.
        let base = layers.pop();
        while (layers.length) base = over(layers.pop(), base);
        return base;
      }
    }
    if (!layers.length) return { r: 255, g: 255, b: 255, a: 1 }; // canvas default
    let base = { r: 255, g: 255, b: 255, a: 1 };
    while (layers.length) base = over(layers.pop(), base);
    return base;
  }

  function path(el) {
    const bits = [];
    for (let n = el; n && bits.length < 3; n = n.parentElement) {
      let s = n.tagName.toLowerCase();
      const cls = (n.className || "").toString().trim().split(/\s+/).filter(Boolean).slice(0, 2);
      if (cls.length) s += "." + cls.join(".");
      bits.unshift(s);
    }
    return bits.join(" > ");
  }

  const out = [];
  const unparsed = new Set();
  let checked = 0;
  const seen = new Set();
  document.querySelectorAll("*").forEach((el) => {
    // Only elements with their own visible text.
    const own = Array.from(el.childNodes)
      .filter((n) => n.nodeType === 3)
      .map((n) => n.textContent)
      .join("")
      .trim();
    if (!own) return;

    const cs = getComputedStyle(el);
    if (cs.display === "none" || cs.visibility === "hidden" || Number(cs.opacity) === 0) return;
    const rect = el.getBoundingClientRect();
    if (rect.width < 2 || rect.height < 2) return; // clipped / visually-hidden

    // WCAG exempts disabled controls and purely decorative text.
    if (el.closest("[disabled],[aria-disabled=true],.disabled")) return;
    if (el.closest("[aria-hidden=true]")) return;

    const fg = parse(cs.color);
    if (!fg) {
      // Never skip quietly: an unrecognised colour syntax means this element went
      // unchecked, which is exactly how the original probe missed every link.
      unparsed.add(cs.color);
      return;
    }
    const bg = backdrop(el);
    if (!bg) return; // genuinely unknowable: an ancestor paints an image or gradient

    checked++;
    const size = parseFloat(cs.fontSize);
    const weight = Number(cs.fontWeight) || 400;
    const large = size >= 24 || (size >= 18.66 && weight >= 700);
    const required = large ? 3 : 4.5;
    const got = ratio(over(fg, bg), bg);
    if (got >= required) return;

    const key = path(el) + "|" + cs.color + "|" + own.slice(0, 20);
    if (seen.has(key)) return;
    seen.add(key);
    out.push({
      Path: path(el),
      Text: own.replace(/\s+/g, " ").slice(0, 45),
      Fg: cs.color,
      Bg: "rgb(" + [bg.r, bg.g, bg.b].map(Math.round).join(",") + ")",
      Ratio: Math.round(got * 100) / 100,
      Required: required,
      FontPx: size,
    });
  });
  return JSON.stringify({ Checked: checked, Failures: out, Location: location.href, Title: document.title, Unparsed: Array.from(unparsed) });
})()`

type contrastFailure struct {
	Path     string
	Text     string
	Fg, Bg   string
	Ratio    float64
	Required float64
	FontPx   float64
}

// TestContrastMeetsWCAGAA measures real rendered contrast across the app.
//
// This is the check that makes shipping a single dark theme defensible: WCAG has no
// requirement to offer two colour schemes, but it does require the one you ship to meet
// AA — 4.5:1 for body text, 3:1 for large text. That had never been verified, and the
// stylesheet already carried a hand-picked colour added to fix a contrast problem
// (.badge.bg-purple-lt) with nothing guarding it.
//
// Deliberately measured in a browser rather than read from CSS: the foreground is often
// translucent, the background usually comes from an ancestor, and both resolve from CSS
// variables that only exist at runtime.
func TestContrastMeetsWCAGAA(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2econtrast")
	if err := srv.store.SetCapabilities(srv.ctx, user.ID, true, true); err != nil {
		t.Fatalf("grant caps: %v", err)
	}
	rep, err := srv.store.CreateRepeater(srv.ctx, &store.Repeater{
		OwnerID: user.ID, Name: "Contrast Relay", PublicKeyHex: strings.Repeat("a", 64),
		RadioFreqHz: 869525000, RadioBwHz: 250000, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}
	org, err := srv.store.CreateOrg(srv.ctx, "Contrast Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	// Seed one violation per classification and disposition so the CSP page renders a
	// populated table rather than its empty state — the badges on those rows
	// (bg-secondary-lt, bg-blue-lt, bg-purple-lt) are exactly the ones this test
	// caught failing, so an empty table would measure none of them.
	cspSeed := store.CSPReport{
		Disposition: "enforce", Directive: "script-src-elem", BlockedURI: "inline",
		DocumentPath: "/repeaters", Host: appHost(), Source: store.CSPReportSourcePage,
		Hits: 3, LastSeen: time.Now(), Sample: "window.injected = 1",
	}
	reportOnly := cspSeed
	reportOnly.Disposition = "report"
	extension := cspSeed
	extension.BlockedURI = "chrome-extension://contrastcheck"
	extension.Source = store.CSPReportSourceExtension
	if err := srv.store.RecordCSPReports(srv.ctx, []store.CSPReport{cspSeed, reportOnly, extension}); err != nil {
		t.Fatalf("seed csp reports: %v", err)
	}

	// Two passes, and the split is load-bearing. Visiting /login while a session cookie
	// is present triggers the cross-host handoff, which rotates the session token — so a
	// single authenticated pass over both surfaces silently unauthenticates everything
	// after the first auth page, and those pages get measured as the sign-in screen. The
	// no-redirect assertion below is what surfaced that.
	anonymous := []struct{ label, url string }{
		{"root landing", srv.rootURL + "/"},
		{"root directory", srv.rootURL + "/orgs"},
		{"root docs", srv.rootURL + "/docs"},
		{"root public org", srv.rootURL + "/orgs/" + org.Slug},
		{"root 404", srv.rootURL + "/no-such-page"},
		{"auth sign-in", srv.authURL + "/login"},
		{"auth sign-up", srv.authURL + "/signup"},
	}
	authenticated := []struct{ label, url string }{
		{"app dashboard", srv.appURL + "/"},
		{"app repeaters", srv.appURL + "/repeaters"},
		{"app repeater", srv.appURL + "/repeaters/" + rep.PublicID},
		{"app sharing", srv.appURL + "/repeaters/" + rep.PublicID + "/share"},
		{"app my orgs", srv.appURL + "/orgs"},
		{"app org", srv.appURL + "/orgs/" + org.Slug},
		{"admin hub", srv.appURL + "/admin"},
		{"admin catalog", srv.appURL + "/admin/catalog"},
		{"admin users", srv.appURL + "/admin/users"},
		{"admin identity", srv.appURL + "/admin/identity"},
		{"admin csp", srv.appURL + "/admin/csp?source=all"},
	}

	bctx, cancel, _ := startBrowser(t)
	defer cancel()

	total, pages := 0, 0
	measure := func(label, url string, auth bool) {
		pages++
		actions := []chromedp.Action{network.Enable(), cdplog.Enable()}
		if auth {
			actions = append(actions, setSessionCookie(cookie))
		} else {
			actions = append(actions, network.ClearBrowserCookies())
		}
		var raw string
		actions = append(actions,
			chromedp.EmulateViewport(1280, 1000),
			chromedp.Navigate(url),
			chromedp.WaitVisible(`body`, chromedp.ByQuery),
			chromedp.Sleep(700*time.Millisecond),
			chromedp.Evaluate(contrastProbe, &raw),
		)
		if err := chromedp.Run(bctx, actions...); err != nil {
			t.Errorf("%s: %v", label, err)
			return
		}
		var result struct {
			Checked  int
			Failures []contrastFailure
			Location string
			Title    string
			Unparsed []string
		}
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			t.Errorf("%s: decode probe output: %v", label, err)
			return
		}
		// A redirect means we measured a different page than intended — and would have
		// reported zero failures for the one we meant to check.
		if !strings.HasPrefix(result.Location, url) {
			t.Errorf("%s: expected %s but landed on %s (%q) — its contrast was never checked",
				label, url, result.Location, result.Title)
			return
		}
		if result.Checked < 3 {
			t.Errorf("%s: only %d text element(s) measured — page rendered empty", label, result.Checked)
			return
		}
		if len(result.Unparsed) > 0 {
			t.Errorf("%s: colour syntax the probe can't read, so those elements went "+
				"unchecked: %v", label, result.Unparsed)
		}
		t.Logf("%-18s %3d elements checked", label, result.Checked)
		if len(result.Failures) == 0 {
			return
		}
		fails := result.Failures
		sort.Slice(fails, func(i, j int) bool { return fails[i].Ratio < fails[j].Ratio })
		total += len(fails)
		var b strings.Builder
		fmt.Fprintf(&b, "%s: %d element(s) below WCAG AA contrast:", label, len(fails))
		for _, f := range fails {
			fmt.Fprintf(&b, "\n    %.2f:1 (need %.1f:1) %s on %s  %.0fpx  %s\n      %q",
				f.Ratio, f.Required, f.Fg, f.Bg, f.FontPx, f.Path, f.Text)
		}
		t.Error(b.String())
	}

	for _, p := range anonymous {
		measure(p.label, p.url, false)
	}
	for _, p := range authenticated {
		measure(p.label, p.url, true)
	}

	if total > 0 {
		t.Logf("%d contrast failure(s) across %d pages", total, pages)
	}
}
