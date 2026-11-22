const { test } = require("node:test");
const assert = require("node:assert/strict");
const vm = require("node:vm");
const { readFileSync } = require("node:fs");
const { join } = require("node:path");
test("checkout fills the actual default address and supports switching to a fresh address", () => {
  const elements = Object.fromEntries(
    ["name", "phone", "province", "city", "street", "postal"].map((key) => [
      key,
      { value: "" },
    ]),
  );
  const picker = {
    value: "",
    addEventListener: (name, fn) => {
      picker[name] = fn;
    },
  };
  const address = {
    name: "گیرندهٔ نمونه",
    phone: "+989121234567",
    province: "تهران",
    city: "تهران",
    street: "نشانی کامل نمونه",
    postal: "1234567890",
  };
  const nodes = {
    "#checkout-form": { elements },
    "#checkout-saved-address": picker,
  };
  const document = {
    dispatchEvent:()=>{},
 getElementById: () => ({
      textContent: JSON.stringify({
        user: { id: "u", name: "مشتری", phone: "+989121234568" },
        addresses: [{ id: "home", default: true, address }],
      }),
    }),
    querySelector: (selector) => nodes[selector] || null,
    querySelectorAll: () => [],
  };
  vm.runInNewContext(
    readFileSync(join(__dirname, "../assets/customer.js"), "utf8"),
    { window: {}, document, Event },
  );
  assert.equal(picker.value, "home");
  assert.equal(elements.street.value, address.street);
  assert.equal(elements.phone.value, address.phone);
  picker.value = "";
  picker.change();
  assert.equal(elements.street.value, "");
  assert.equal(elements.postal.value, "");
  assert.equal(elements.name.value, "مشتری");
});
