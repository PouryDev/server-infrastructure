const { test } = require("node:test");
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const { join } = require("node:path");
const vm = require("node:vm");
const app = readFileSync(join(__dirname, "../assets/app.js"), "utf8");
// Exercise the actual cart initialization/rendering without requiring a browser
// or making provider/network calls.
async function renderCart(products, lines) {
  const root = { innerHTML: "", querySelectorAll: () => [] };
  const nodes = {
    "#cart-content": root,
    "#retry-cart": {},
    "#boot": {
      textContent: JSON.stringify({
        view: "cart",
        user: {},
        settings: {
          modules: { attachments: true },
          shipping: 50000,
          freeShipping: 15000000,
        },
      }),
    },
  };
  const context = vm.createContext({
    window: {},
    AbortController,
    document: {
      getElementById: (id) => nodes["#" + id],
      querySelector: (key) => nodes[key] || null,
      querySelectorAll: () => [],
    },
    localStorage: {
      getItem: (key) => (key === "hera-cart-v1" ? JSON.stringify(lines) : null),
    },
    fetch: async () => ({ status: 200, ok: true, json: async () => products }),
    FormData: class {},
    Intl,
    setTimeout,
    clearTimeout,
  });
  const prefix =
    "const lifetime = new AbortController();\n" +
    app.slice(app.indexOf("const boot ="), app.indexOf("void initCart();"));
  vm.runInContext(prefix, context);
  await vm.runInContext("initCart()", context);
  return root.innerHTML;
}
const line = { productId: "p1", variantId: "v1", quantity: 2, attachments: [] };
const product = {
  id: "p1",
  slug: "cup",
  name: "فنجان",
  image: "/assets/mug.jpg",
  variants: [{ id: "v1", name: "سفید", price: 2000000, stock: 5 }],
};
test("legacy null attachment rules render a nonempty cart and checkout link", async () => {
  const html = await renderCart([{ ...product, attachments: null }], [line]);
  assert.match(html, /فنجان/);
  assert.match(html, /href="\/checkout"/);
  assert.doesNotMatch(html, /دریافت سبد ممکن نشد|نیازمند فایل اختصاصی/);
});
test("required attachments remain visible and removed variants can be removed", async () => {
  assert.match(
    await renderCart([{ ...product, attachments: [{ id: "r1" }] }], [line]),
    /نیازمند فایل اختصاصی/,
  );
  const html = await renderCart([{ ...product, variants: null }], [line]);
  assert.match(html, /دیگر در دسترس نیست/);
  assert.match(html, /حذف از سبد/);
  assert.doesNotMatch(html, /دریافت سبد ممکن نشد/);
});
test("empty cart stays actionable and malformed catalog has a retry action", async () => {
  assert.match(await renderCart([], []), /کشف فروشگاه/);
  const html = await renderCart(null, [line]);
  assert.match(html, /retry-cart/);
  assert.doesNotMatch(html, /Cannot read/);
});

test("checkout rejection replaces the loading placeholder with a recovery action", async () => {
  const nodes = {
    "#pay-button": { disabled: false },
    "#gateway-options": { innerHTML: "در حال محاسبه" },
    "#checkout-totals": { innerHTML: "" },
    "#retry-quote": {},
  };
  const context = vm.createContext({
    $: (key) => nodes[key],
    quoteGeneration: 0,
 form:{elements:{province:{value:"تهران"},city:{value:"تهران"}}},
    lifetime: { signal: { aborted: false } },
    quote: null,
    cart: [line],
    code: "",
    err: { textContent: "" },
    api: async () => {
      throw new Error("فایل موردنیاز را بارگذاری کنید");
    },
  });
  const start = app.indexOf("async function refreshQuote()");
  const end = app.indexOf('let deliveryTimer;', start);
  vm.runInContext(app.slice(start, end), context);
  await vm.runInContext("refreshQuote()", context);
  assert.equal(nodes["#pay-button"].disabled, true);
  assert.match(nodes["#gateway-options"].innerHTML, /بررسی دوباره سبد/);
  assert.doesNotMatch(nodes["#gateway-options"].innerHTML, /در حال محاسبه/);
  assert.equal(typeof nodes["#retry-quote"].onclick, "function");
  assert.match(context.err.textContent, /فایل موردنیاز/);
});
