const { test } = require("node:test");
const assert = require("node:assert/strict");
const vm = require("node:vm");
const { readFileSync } = require("node:fs");
const { join } = require("node:path");
const source = readFileSync(
  join(__dirname, "../assets/merchandising.js"),
  "utf8",
);
const tick = () => new Promise(setImmediate);
function setup(count = 5) {
  const controller = new AbortController();
  let notify;
  const requests = [],
    hydrated = [];
  const rows = Array.from({ length: count }, (_, i) => ({
    dataset: { rail: "category", railKey: "category " + i },
    root: { dataset: {}, innerHTML: "", setAttribute() {}, append() {} },
  }));
  class Observer {
    constructor(callback) {
      notify = callback;
    }
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  const window = {
    storefrontSignal: controller.signal,
    IntersectionObserver: Observer,
    hydrateStorefrontCards: (root) => hydrated.push(root),
  };
  const document = {
    getElementById: () => ({
      textContent: JSON.stringify({ view: "home", settings: {} }),
    }),
    querySelectorAll: (selector) => (selector === ".merch-rail" ? rows : []),
    querySelector: (selector) => null,
  };
  rows.forEach((row) => {
    row.querySelector = (selector) =>
      selector === "[data-lazy-products]" ? row.root : null;
  });
  vm.runInNewContext(source, {
    window,
    document,
    IntersectionObserver: Observer,
    URLSearchParams,
    fetch: (url, options) =>
      new Promise((resolve, reject) =>
        requests.push({ url, options, resolve, reject }),
      ),
  });
  return {
    controller,
    requests,
    hydrated,
    rows,
    intersect: () =>
      notify(rows.map((target) => ({ target, isIntersecting: true }))),
  };
}
test("category products wait for visibility and run no more than two concurrent loads", async () => {
  const s = setup();
  assert.equal(s.requests.length, 0);
  s.intersect();
  assert.equal(s.requests.length, 2);
  for (let index = 0; index < 5; index++) {
    assert.ok(s.requests[index]);
    s.requests[index].resolve({
      ok: true,
      text: async () => "<article>کالا</article>",
    });
    await tick();
    assert.ok(s.requests.length <= index + 3);
  }
  assert.equal(s.requests.length, 5);
  assert.equal(s.hydrated.length, 5);
  assert.match(s.requests[0].url, /kind=category/);
  assert.equal(s.rows[0].root.innerHTML, "<article>کالا</article>");
});
test("aborted route loads do not hydrate products into a later page or drain the queue", async () => {
  const s = setup();
  s.intersect();
  s.controller.abort();
  s.requests.forEach((r) =>
    r.resolve({ ok: true, text: async () => "<article>stale</article>" }),
  );
  await tick();
  assert.equal(s.requests.length, 2);
  assert.equal(s.hydrated.length, 0);
  assert.equal(s.rows[0].root.innerHTML, "");
});
