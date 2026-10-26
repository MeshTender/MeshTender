// serial-cli.js — talks a MeshCore repeater's own text CLI over USB serial.
//
// What the firmware does, from examples/simple_repeater/main.cpp (keep this in
// step with it): it echoes each character it reads, ignores "\n", and runs the
// line when it reads "\r"; then it prints "\n" and, if the command has a reply,
// "  -> <reply>\r\n". A line longer than its 160-byte buffer runs as soon as the
// buffer fills, so a missing "\r" turns several commands into one garbled one.
//
//   SerialCLI.open(port)        → Promise<cli>  opens the port at 115200 8N1
//   cli.waitReady(onWait)       → Promise<bool> until the repeater answers "ver"
//                                 (opening the port resets many boards, and a
//                                 booting repeater drops what it's sent)
//   cli.command(text)           → Promise<{reply, error, reboot}>
//                                 reply: the "-> " text, or null if none came;
//                                 error: the firmware said Err/Error;
//                                 reboot: it said the change needs a reboot
//   cli.send(text)              → writes a line without waiting (for "reboot")
//   cli.close()                 → releases and closes the port
//   SerialCLI.parseReply(text)  → the reply in what a command printed, or null
(function () {
  "use strict";

  var BAUD = 115200;
  var REPLY_WAIT_MS = 3000; // a reply is printed in the same loop pass; this is generous
  var QUIET_MS = 500;       // after the echo, this long with nothing means "no reply"

  // parseReply finds the firmware's "  -> reply" line in a command's output.
  function parseReply(text) {
    var m = /(?:^|\n)\s*-> ?([^\r\n]*)\r?\n/.exec(String(text || ""));
    return m ? m[1] : null;
  }

  function classify(reply) {
    var r = reply || "";
    return {
      reply: reply,
      error: /^\s*err/i.test(r),
      reboot: /reboot to apply/i.test(r),
    };
  }

  function sleep(ms) { return new Promise(function (res) { setTimeout(res, ms); }); }

  async function open(port) {
    await port.open({ baudRate: BAUD });
    var writer = port.writable.getWriter();
    var reader = port.readable.getReader();
    var encoder = new TextEncoder();
    var decoder = new TextDecoder();
    var buf = "";        // everything read since the last command was written
    var lastRead = 0;    // when bytes last arrived
    var closed = false;
    var wake = null;     // resolves when bytes arrive

    (async function pump() {
      try {
        for (;;) {
          var chunk = await reader.read();
          if (chunk.done) break;
          if (chunk.value) {
            buf += decoder.decode(chunk.value, { stream: true });
            lastRead = performance.now();
            if (wake) { var w = wake; wake = null; w(); }
          }
        }
      } catch (_) {
        // the port closed or the device went away; command() times out
      }
    })();

    function arrival(ms) {
      return new Promise(function (res) {
        var t = setTimeout(function () { wake = null; res(); }, ms);
        wake = function () { clearTimeout(t); res(); };
      });
    }

    function writeLine(text) {
      buf = "";
      lastRead = performance.now();
      return writer.write(encoder.encode(text + "\r"));
    }

    async function command(text) {
      if (closed) throw new Error("The serial port is closed.");
      await writeLine(text);
      var deadline = performance.now() + REPLY_WAIT_MS;
      for (;;) {
        var reply = parseReply(buf);
        if (reply !== null) return classify(reply);
        var now = performance.now();
        // The echo ends with "\n" once the line ran; a quiet spell after that
        // means the command has nothing to say.
        if (buf.indexOf("\n") !== -1 && now - lastRead >= QUIET_MS) return classify(null);
        if (now >= deadline) return classify(null);
        await arrival(Math.min(QUIET_MS, deadline - now));
      }
    }

    async function waitReady(onWait, attempts) {
      var n = attempts || 10;
      for (var i = 0; i < n; i++) {
        var r = await command("ver");
        if (r.reply !== null) return true;
        if (onWait) onWait(i + 1);
        await sleep(1000);
      }
      return false;
    }

    async function close() {
      if (closed) return;
      closed = true;
      try { await reader.cancel(); } catch (_) {}
      try { reader.releaseLock(); } catch (_) {}
      try { writer.releaseLock(); } catch (_) {}
      try { await port.close(); } catch (_) {}
    }

    return { command: command, send: writeLine, waitReady: waitReady, close: close };
  }

  window.SerialCLI = { open: open, parseReply: parseReply };
})();
