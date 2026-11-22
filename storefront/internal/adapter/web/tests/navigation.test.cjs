const { test } = require("node:test");
const assert = require("node:assert/strict");
const vm = require("node:vm");
const { readFileSync } = require("node:fs");
const { join } = require("node:path");
test("soft navigation keeps payments, private handlers, assets and other origins native", () => {
  const context = {
    window: {},
    URL,
    location: {
      href: "https://shop.example/shop",
      origin: "https://shop.example",
      pathname: "/shop",
    },
    history: {},
    document: { addEventListener() {} },
    addEventListener() {},
  };
  vm.runInNewContext(
    readFileSync(join(__dirname, "../assets/navigation.js"), "utf8"),
    context,
  );
  const eligible = context.window.storefrontNavigation.eligible;
  for (const path of [
    "/shop?q=ماگ",
    "/products/mug",
    "/cart",
    "/checkout",
    "/blog/article",
    "/account",
  ])
    assert.equal(eligible(path), true, path);
  for (const path of [
    "https://evil.example/shop",
    "/payment/callback/id",
    "/payment/demo/id",
    "/api/products",
    "/seller",
    "/admin",
    "/media/id",
    "/assets/app.js",
    "/manifest.webmanifest",
  ])
    assert.equal(eligible(path), false, path);
});
