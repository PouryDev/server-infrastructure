const { test } = require("node:test");
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const { join } = require("node:path");
const vm = require("node:vm");
function worker(fetch) {
  const listeners = {},
    writes = [],
    deleted = [];
  const offline = { offline: true };
  const cache = {
    addAll: async () => {},
    put: async (...args) => {
      writes.push(args);
    },
  };
  vm.runInNewContext(readFileSync(join(__dirname, "../assets/sw.js"), "utf8"), {
    self: {
      location: { origin: "https://shop.example" },
      addEventListener: (name, fn) => {
        listeners[name] = fn;
      },
      skipWaiting: () => {},
      clients: { claim: async () => {} },
    },
    caches: {
      open: async () => cache,
      match: async () => offline,
      keys: async () => ["other-app", "storefront-shell-old"],
      delete: async (key) => {
        deleted.push(key);
      },
    },
    fetch,
    URL,
  });
  return { listeners, writes, deleted, offline };
}
function request(path, method = "GET", mode = "navigate") {
  return { url: "https://shop.example" + path, method, mode };
}
function intercept(worker, request) {
  let promise;
  worker.listeners.fetch({
    request,
    respondWith: (p) => {
      promise = p;
    },
  });
  return promise;
}
test("offline navigation has a recovery page and never caches customer HTML", async () => {
  const w = worker(async () => {
    throw new Error("offline");
  });
  assert.equal(await intercept(w, request("/account")), w.offline);
  assert.equal(await intercept(w, request("/checkout")), w.offline);
  assert.equal(w.writes.length, 0);
});
test("API requests, all writes, and payment callbacks bypass the worker", () => {
  const w = worker(() => {
    throw new Error("must bypass");
  });
  for (const r of [
    request("/api/orders"),
    request("/api/checkout", "POST"),
    request("/payment/callback/order"),
    request("/readyz"),
  ])
    assert.equal(intercept(w, r), undefined);
});
test("online personalized navigation is network only; only shell assets enter cache", async () => {
  const response = { ok: true, clone: () => ({ copy: true }) };
  const w = worker(async () => response);
  assert.equal(await intercept(w, request("/account")), response);
  assert.equal(w.writes.length, 0);
  assert.equal(
    await intercept(w, request("/assets/style.css", "GET", "cors")),
    response,
  );
  assert.equal(w.writes.length, 1);
  assert.equal(
    intercept(w, request("/media/customer-photo", "GET", "cors")),
    undefined,
  );
});
test("activation cleans only this storefront shell caches", async () => {
  const w = worker(async () => {});
  let promise;
  w.listeners.activate({
    waitUntil: (p) => {
      promise = p;
    },
  });
  await promise;
  assert.deepEqual(w.deleted, ["storefront-shell-old"]);
});
