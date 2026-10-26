// Tab completion and history for the repeater console's command input.
//
// The commands come from the "Allowed commands" chips ([data-template], plus an
// optional [data-description]), so completion only ever offers what this user
// may send. A template is literal words followed by placeholders, e.g.
// "set tx <0-22>" or "set repeat <on|off>"; a placeholder listing choices with
// "|" completes to those choices too.
//
//   ConsoleComplete.candidates(templates, text) → [{template, label, completion}]
//       pure; what the text could complete to (exported for the e2e tests).
//       label is what the list shows: the template, or it with a choice filled in.
//   ConsoleComplete.commonPrefix(strings)      → the longest shared prefix
//
// Keys in the input: Tab completes as far as every match agrees, then steps
// through the matches; with nothing to complete it moves focus as usual, and so
// does Escape then Tab (Escape also closes the list). Up/Down move through the
// matches when the list is open and through sent commands (window "console:sent"
// events) when it isn't. Enter is the form's: it sends.
(function () {
  "use strict";

  function words(s) { return String(s || "").trim().split(/\s+/).filter(Boolean); }
  function isPlaceholder(w) { return w.charAt(0) === "<"; }
  function choices(w) {
    var inner = w.replace(/^<|>$/g, "");
    return inner.indexOf("|") === -1 || /\s/.test(inner) ? null : inner.split("|");
  }

  function candidates(templates, text) {
    var typed = String(text || "").replace(/^\s+/, "").toLowerCase();
    var out = [];
    templates.forEach(function (template) {
      var ws = words(template);
      var lit = [];
      while (lit.length < ws.length && !isPlaceholder(ws[lit.length])) lit.push(ws[lit.length]);
      var literal = lit.join(" ");
      var hasArgs = lit.length < ws.length;
      if (literal.toLowerCase().indexOf(typed) === 0) {
        out.push({ template: template, label: template, completion: literal + (hasArgs ? " " : "") });
        return;
      }
      // Past the command itself: complete the argument being typed, if its
      // placeholder lists the choices.
      if (!hasArgs || typed.indexOf(literal.toLowerCase() + " ") !== 0) return;
      var rest = typed.slice(literal.length + 1);
      var done = rest.split(/\s+/);
      var partial = done.pop();
      var argAt = lit.length + done.length;
      if (argAt >= ws.length) return;
      var opts = choices(ws[argAt]);
      var head = text.replace(/^\s+/, "").slice(0, typed.length - partial.length);
      if (!opts) {
        // A free-form argument: nothing to complete, but the template still
        // matches, so it stays in the list as a hint.
        out.push({ template: template, label: template, completion: null });
        return;
      }
      opts.forEach(function (o) {
        if (o.toLowerCase().indexOf(partial) === 0) {
          var label = ws.slice(0, argAt).concat([o], ws.slice(argAt + 1)).join(" ");
          out.push({ template: template, label: label, completion: head + o + (argAt + 1 < ws.length ? " " : "") });
        }
      });
    });
    return out;
  }

  function commonPrefix(list) {
    if (!list.length) return "";
    var p = list[0];
    list.forEach(function (s) {
      while (s.toLowerCase().indexOf(p.toLowerCase()) !== 0) p = p.slice(0, -1);
    });
    return p;
  }

  window.ConsoleComplete = { candidates: candidates, commonPrefix: commonPrefix };

  var input = document.getElementById("cmdinput");
  var list = document.getElementById("cmdsuggest");
  if (!input || !list) return;

  var commands = Array.prototype.map.call(document.querySelectorAll(".chip[data-template]"), function (c) {
    return { template: String(c.dataset.template || ""), description: String(c.dataset.description || "") };
  });
  var templates = commands.map(function (c) { return c.template; });
  var describe = {};
  commands.forEach(function (c) { describe[c.template] = c.description; });

  var shown = [];   // the matches in the list
  var active = -1;  // the highlighted one
  var history = [];
  var back = -1;    // how far back in history Up has gone (-1: not browsing)
  var draft = "";   // what was typed before browsing history
  var leaving = false; // Escape was pressed: the next Tab moves focus

  function close() {
    list.hidden = true;
    shown = [];
    active = -1;
    input.setAttribute("aria-expanded", "false");
    input.removeAttribute("aria-activedescendant");
  }

  function render() {
    list.textContent = "";
    if (!shown.length) { close(); return; }
    shown.forEach(function (m, i) {
      var li = document.createElement("li");
      li.id = "cmdsuggest-" + i;
      li.className = "console-suggest-item" + (i === active ? " active" : "");
      li.setAttribute("role", "option");
      li.setAttribute("aria-selected", i === active ? "true" : "false");
      var code = document.createElement("code");
      code.textContent = m.label;
      li.appendChild(code);
      if (describe[m.template]) {
        var d = document.createElement("span");
        d.className = "text-secondary ms-2";
        d.textContent = describe[m.template];
        li.appendChild(d);
      }
      li.addEventListener("mousedown", function (e) {
        e.preventDefault(); // keep focus in the input
        accept(m);
      });
      list.appendChild(li);
    });
    list.hidden = false;
    input.setAttribute("aria-expanded", "true");
    if (active >= 0) {
      input.setAttribute("aria-activedescendant", "cmdsuggest-" + active);
      var el = document.getElementById("cmdsuggest-" + active);
      if (el) el.scrollIntoView({ block: "nearest" });
    } else {
      input.removeAttribute("aria-activedescendant");
    }
  }

  function refresh(force) {
    active = -1;
    shown = input.value.trim() || force ? candidates(templates, input.value) : [];
    // A lone exact match with nothing left to type is noise.
    if (shown.length === 1 && shown[0].completion !== null && shown[0].completion.trim() === input.value.trim() &&
        shown[0].completion === shown[0].template) shown = [];
    render();
  }

  function setValue(v) {
    input.value = v;
    input.setSelectionRange(v.length, v.length);
  }

  function accept(m) {
    if (m.completion !== null) setValue(m.completion);
    input.focus();
    refresh(false);
  }

  // tab completes what it can and reports whether it did anything. When there's
  // nothing to complete (an empty input, no match, a command already typed in
  // full), Tab is left to move focus as usual, so it never traps the keyboard.
  function tab() {
    if (active >= 0) { // stepping through the matches
      active = (active + 1) % shown.length;
      if (shown[active].completion !== null) input.value = shown[active].completion;
      render();
      return true;
    }
    if (!input.value.trim()) return false;
    var matches = candidates(templates, input.value);
    var completions = matches.map(function (m) { return m.completion; }).filter(function (c) { return c !== null; });
    var prefix = commonPrefix(completions);
    if (completions.length && prefix.length > input.value.replace(/^\s+/, "").length) {
      setValue(prefix);
      refresh(true);
      if (completions.length === 1) close();
      return true;
    }
    if (completions.length < 2) return false;
    shown = matches;
    active = shown.findIndex(function (m) { return m.completion !== null; });
    input.value = shown[active].completion;
    render();
    return true;
  }

  input.setAttribute("role", "combobox");
  input.setAttribute("aria-autocomplete", "list");
  input.setAttribute("aria-controls", "cmdsuggest");
  input.setAttribute("aria-expanded", "false");

  input.addEventListener("input", function () { back = -1; refresh(false); });
  input.addEventListener("blur", close);
  input.addEventListener("keydown", function (e) {
    var escaped = leaving;
    leaving = false;
    if (e.key === "Tab" && !e.shiftKey && !e.altKey && !e.ctrlKey && !e.metaKey) {
      if (escaped) { close(); return; } // Esc, then Tab: move on
      if (tab()) e.preventDefault();
    } else if (e.key === "Escape") {
      // The first Escape closes the list; with it closed, the next Tab leaves.
      if (!list.hidden) close();
      leaving = true;
    } else if ((e.key === "ArrowDown" || e.key === "ArrowUp") && !list.hidden) {
      e.preventDefault();
      var step = e.key === "ArrowDown" ? 1 : -1;
      active = active < 0 ? (step > 0 ? 0 : shown.length - 1) : (active + step + shown.length) % shown.length;
      render();
    } else if (e.key === "ArrowUp" && history.length) {
      e.preventDefault();
      if (back < 0) draft = input.value;
      back = Math.min(back + 1, history.length - 1);
      setValue(history[history.length - 1 - back]);
    } else if (e.key === "ArrowDown" && back >= 0) {
      e.preventDefault();
      back--;
      setValue(back < 0 ? draft : history[history.length - 1 - back]);
    } else if (e.key === "Enter" && active >= 0) {
      // Enter on a match picked with the arrows takes it rather than sending half
      // a command; one Tab already put in the input sends as it is.
      if (shown[active].completion !== null && shown[active].completion !== input.value) {
        e.preventDefault();
        accept(shown[active]);
      } else {
        close();
      }
    }
  });

  window.addEventListener("console:sent", function (e) {
    var text = String((e.detail && e.detail.text) || "").trim();
    if (text && history[history.length - 1] !== text) history.push(text);
    back = -1;
    close();
  });
})();
