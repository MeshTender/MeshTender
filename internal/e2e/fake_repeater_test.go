//go:build browser

package e2e

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	meshcore "github.com/meshcore-go/meshcore-go"
)

// fakeRepeaterJS stands in for navigator.serial with one port wired to a fake
// MeshCore repeater, so the USB pages can be driven end to end without hardware.
// It follows the firmware's serial CLI loop (examples/simple_repeater/main.cpp)
// on the points that have broken us: it echoes each character, ignores "\n",
// runs a line on "\r" or when its 160-byte buffer fills, prints "\n" and then
// "  -> <reply>\r\n", and ignores everything for the first moments after the
// port opens, as a board that resets on open does.
//
// `set prv.key` derives the public key the way the firmware does, through the
// fwDerive binding (meshcore-go, in Go), and refuses a key whose public key
// starts with 00 or ff (LocalIdentity::validatePrivateKey).
//
// Each line it runs goes to Go through the fwRan binding ({line, at}), so the
// record survives the page navigating away; window.__fw holds its public key.
const fakeRepeaterJS = `
(function () {
  var fw = window.__fw = { pub: %q, bootMs: 1200 };
  var enc = new TextEncoder(), dec = new TextDecoder();
  var out, line = "", openedAt = 0, pending = null;
  function emit(s) { if (out) out.enqueue(enc.encode(s)); }
  function derive(prv) {
    return new Promise(function (res) { pending = res; window.fwDerive(prv); });
  }
  window.__fwDerived = function (pub) { var p = pending; pending = null; p(pub); };
  async function run(cmd) {
    window.fwRan(JSON.stringify({ line: cmd, at: Date.now() }));
    var reply = "OK";
    var m;
    if (cmd === "ver") reply = "v1.test (Build: fake)";
    else if (cmd === "get public.key") reply = "> " + fw.pub;
    else if (cmd === "reboot") reply = "";
    else if ((m = /^set prv\.key ([0-9a-f]{128})$/.exec(cmd))) {
      var pub = await derive(m[1]);
      if (!pub || /^(00|ff)/.test(pub)) reply = "Error, bad key";
      else { fw.pub = pub; reply = "OK, reboot to apply! New pubkey: " + pub; }
    } else if (/^set radio /.test(cmd)) reply = "OK - reboot to apply";
    emit("\n");
    if (reply) emit("  -> " + reply + "\r\n");
  }
  var queue = Promise.resolve();
  function feed(bytes) {
    if (performance.now() - openedAt < fw.bootMs) return; // still booting
    var s = dec.decode(bytes);
    for (var i = 0; i < s.length; i++) {
      var c = s[i];
      if (c !== "\n") { line += c; emit(c); }
      if (c === "\r" || line.length >= 159) {
        var cmd = line.replace(/\r$/, "");
        line = "";
        queue = queue.then(function () { return run(cmd); });
      }
    }
  }
  var port = {
    open: async function () { openedAt = performance.now(); },
    close: async function () {},
    readable: new ReadableStream({ start: function (c) { out = c; } }),
    writable: new WritableStream({ write: function (chunk) { feed(chunk); return queue; } }),
  };
  Object.defineProperty(navigator, "serial", {
    configurable: true,
    value: { requestPort: async function () { return port; }, getPorts: async function () { return [port]; } },
  });
})();
`

// fakeRepeater is what the fake repeater ran, as the page reported it.
type fakeRepeater struct {
	mu  sync.Mutex
	ran []fakeLine
}

// fakeLine is one line the fake repeater ran, and when (ms since the epoch).
type fakeLine struct {
	Line string `json:"line"`
	At   int64  `json:"at"`
}

// lines returns the command lines run so far.
func (f *fakeRepeater) lines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.ran))
	for i, e := range f.ran {
		out[i] = e.Line
	}
	return out
}

// at returns when the first line starting with prefix ran.
func (f *fakeRepeater) at(prefix string) (time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.ran {
		if strings.HasPrefix(e.Line, prefix) {
			return time.UnixMilli(e.At), true
		}
	}
	return time.Time{}, false
}

// installFakeRepeater makes every page the browser loads from now on see the
// fake repeater, starting with startPub as its identity.
func installFakeRepeater(t *testing.T, ctx context.Context, startPub string) *fakeRepeater {
	t.Helper()
	fake := &fakeRepeater{}
	chromedp.ListenTarget(ctx, func(ev any) {
		b, ok := ev.(*runtime.EventBindingCalled)
		if !ok {
			return
		}
		if b.Name == "fwRan" {
			var l fakeLine
			if json.Unmarshal([]byte(b.Payload), &l) == nil {
				fake.mu.Lock()
				fake.ran = append(fake.ran, l)
				fake.mu.Unlock()
			}
			return
		}
		if b.Name != "fwDerive" {
			return
		}
		pub := ""
		if raw, err := hex.DecodeString(b.Payload); err == nil {
			if id, err := meshcore.NewLocalIdentityFromExpandedKey(raw); err == nil {
				pub = strings.ToLower(id.String())
			}
		}
		// Not inline: chromedp.Run can't be called from inside a listener on the
		// same context (see acceptDialogs).
		go func() { _ = chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf("window.__fwDerived(%q)", pub), nil)) }()
	})
	if err := chromedp.Run(ctx,
		runtime.AddBinding("fwDerive"),
		runtime.AddBinding("fwRan"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(fmt.Sprintf(fakeRepeaterJS, startPub)).Do(ctx)
			return err
		}),
	); err != nil {
		t.Fatalf("install fake repeater: %v", err)
	}
	return fake
}
