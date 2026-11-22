const { test } = require("node:test");
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const { join } = require("node:path");
const vm = require("node:vm");
const script = readFileSync(
  join(__dirname, "../assets/theme-preview.js"),
  "utf8",
);
function setup() {
  const control = () => ({
    handlers: {},
    addEventListener(name, fn) {
      this.handlers[name] = fn;
    },
  });
  const select = control(),
    reset = control(),
    next = control(),
    previous = control();
  next.dataset = { themeStep: "1" };
  previous.dataset = { themeStep: "-1" };
  const preset = (id, accent, layout) => ({
    theme: { id, name: id, accent, background: "#eee", layout },
    design: { Hero: "split", Grid: "four", Card: "round", Header: "bar" },
  });
  const preview = {
    original: preset("atelier", "#123456", "editorial"),
    themes: [
      preset("mono", "#252525", "minimal"),
      preset("bazaar", "#17655d", "market"),
    ],
    selected: "",
  };
  const styles = {},
    meta = {},
    status = {},
    urls = [];
  const bar = { querySelectorAll: () => [previous, next] };
  const nodes = {
    boot: { textContent: JSON.stringify({ themePreview: preview }) },
    "debug-theme": select,
    "reset-theme-preview": reset,
    "theme-preview-status": status,
  };
  const document = {
    body: {
      dataset: {},
      style: {
        setProperty: (key, value) => {
          styles[key] = value;
        },
      },
    },
    querySelector: (key) =>
      key === ".theme-debugbar"
        ? bar
        : {
            setAttribute: (key, value) => {
              meta[key] = value;
            },
          },
    getElementById: (id) => nodes[id],
  };
  vm.runInNewContext(script, {
    document,
    window: {},
    URL,
    location: {
      href: "http://shoptest.localhost/products?category=home",
      protocol: "http:",
    },
    history: { replaceState: (_, __, url) => urls.push(url) },
  });
  return { document, select, reset, next, previous, styles, status, urls };
}
test("changing presets updates composition/palette without a reload and preserves current filters", () => {
  const s = setup();
  s.select.value = "mono";
  s.select.handlers.change();
  assert.equal(s.document.body.dataset.layout, "minimal");
  assert.equal(s.document.body.dataset.card, "round");
  assert.equal(s.styles["--accent"], "#123456");
  assert.match(
    s.document.cookie,
    /storefront-preview=mono; Path=\/; SameSite=Lax/,
  );
  const url = new URL(s.urls.at(-1));
  assert.equal(url.searchParams.get("category"), "home");
  assert.equal(url.searchParams.get("preview"), "mono");
  s.next.handlers.click();
  assert.equal(s.select.value, "bazaar");
  s.previous.handlers.click();
  assert.equal(s.select.value, "mono");
});
test("reset restores the actual brand, clears the session preview and rejects unknown themes", () => {
  const s = setup();
  s.select.value = "mono";
  s.select.handlers.change();
  s.reset.handlers.click();
  assert.equal(s.styles["--accent"], "#123456");
  assert.equal(s.document.body.dataset.layout, "editorial");
  assert.match(s.document.cookie, /Max-Age=0/);
  assert.equal(new URL(s.urls.at(-1)).searchParams.has("preview"), false);
  s.select.value = "unknown";
  s.select.handlers.change();
  assert.equal(s.select.value, "");
});
