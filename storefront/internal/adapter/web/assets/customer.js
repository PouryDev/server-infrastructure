"use strict";
window.initCustomerPanel = function initCustomerPanel() {
  const boot = JSON.parse(document.getElementById("boot").textContent);
  const signal = window.storefrontSignal;
  const $ = (selector, root = document) => root.querySelector(selector);
  const $$ = (selector, root = document) => [
    ...root.querySelectorAll(selector),
  ];
  const digits = (value) =>
    String(value)
      .replace(/[۰-۹]/g, (n) => "۰۱۲۳۴۵۶۷۸۹".indexOf(n))
      .replace(/[٠-٩]/g, (n) => "٠١٢٣٤٥٦٧٨٩".indexOf(n));
  let addresses = Array.isArray(boot.addresses) ? boot.addresses : [];
  async function request(path, data, method = "POST") {
    const response = await fetch("/api" + path, {
      method,
      credentials: "same-origin",
      signal,
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": boot.csrf || "",
      },
      ...(data === undefined ? {} : { body: JSON.stringify(data) }),
    });
    const result = await response.json();
    if (!response.ok)
      throw new Error(
        result.error || "ذخیرهٔ اطلاعات انجام نشد؛ دوباره تلاش کنید.",
      );
    return result;
  }
  function refresh(path) {
    return window.storefrontNavigation.visit(path, { replace: true });
  }
  const form = $("#customer-address-form"),
    dialog = $("#address-dialog");
  function editAddress(id = "") {
    const saved = addresses.find((a) => a.id === id);
    form.reset();
    form.elements.id.value = id;
    form.elements.label.value = saved?.label || "";
    form.elements.default.checked = saved?.default || addresses.length === 0;
    for (const key of ["name", "phone", "province", "city", "street", "postal"])
      form.elements[key].value =
        saved?.address[key] ||
        (key === "name"
          ? boot.user.name
          : key === "phone"
            ? boot.user.phone
            : "");
    $("#address-dialog-title").textContent = id ? "ویرایش نشانی" : "نشانی تازه";
    $("#address-error").textContent = "";
    dialog.showModal();
  }
  $$("[data-new-address]").forEach((button) =>
    button.addEventListener("click", () => editAddress()),
  );
  $$("[data-edit-address]").forEach((button) =>
    button.addEventListener("click", () =>
      editAddress(button.dataset.editAddress),
    ),
  );
  $("[data-close-address]")?.addEventListener("click", () => dialog.close());
  form?.addEventListener("submit", async (event) => {
    event.preventDefault();
    const button = $("button[type=submit]", form);
    button.disabled = true;
    try {
      const address = Object.fromEntries(
        ["name", "phone", "province", "city", "street", "postal"].map((key) => [
          key,
          digits(form.elements[key].value),
        ]),
      );
      await request("/account/addresses", {
        id: form.elements.id.value,
        label: form.elements.label.value,
        default: form.elements.default.checked,
        address,
      });
      dialog.close();
      await refresh("/account/addresses?saved=1");
    } catch (error) {
      if (!signal?.aborted) $("#address-error").textContent = error.message;
    } finally {
      button.disabled = false;
    }
  });
  $$("[data-default-address]").forEach((button) =>
    button.addEventListener("click", async () => {
      button.disabled = true;
      try {
        const saved = addresses.find(
          (a) => a.id === button.dataset.defaultAddress,
        );
        await request("/account/addresses", { ...saved, default: true });
        await refresh("/account/addresses?saved=1");
      } catch (error) {
        if (!signal?.aborted)
          $("#customer-feedback").textContent = error.message;
      } finally {
        button.disabled = false;
      }
    }),
  );
  $$("[data-delete-address]").forEach((button) =>
    button.addEventListener("click", async () => {
      const saved = addresses.find(
        (a) => a.id === button.dataset.deleteAddress,
      );
      if (
        !confirm(
          `نشانی «${saved.label}» حذف شود؟ نشانی سفارش‌های قبلی تغییر نمی‌کند.`,
        )
      )
        return;
      button.disabled = true;
      try {
        await request(
          "/account/addresses/" + encodeURIComponent(saved.id),
          undefined,
          "DELETE",
        );
        await refresh("/account/addresses");
      } catch (error) {
        if (!signal?.aborted)
          $("#customer-feedback").textContent = error.message;
      } finally {
        button.disabled = false;
      }
    }),
  );
  $("#customer-profile-form")?.addEventListener("submit", async (event) => {
    event.preventDefault();
    const profile = event.currentTarget;
    const button = $("button[type=submit]", profile);
    button.disabled = true;
    try {
      await request("/account/profile", { name: profile.elements.name.value });
      await refresh("/account/profile?saved=1");
    } catch (error) {
      if (!signal?.aborted)
        $(".form-feedback", profile).textContent = error.message;
    } finally {
      button.disabled = false;
    }
  });
  const checkout = $("#checkout-form"),
    picker = $("#checkout-saved-address");
  function chooseAddress(id) {
    const saved = addresses.find((a) => a.id === id);
    if (!saved) return;
    for (const key of ["name", "phone", "province", "city", "street", "postal"])
      checkout.elements[key].value = saved.address[key];
 document.dispatchEvent(new Event("storefront:address-changed"));
  }
  picker?.addEventListener("change", () => {
    if (picker.value) chooseAddress(picker.value);
    else
      for (const key of [
        "name",
        "phone",
        "province",
        "city",
        "street",
        "postal",
      ])
        checkout.elements[key].value =
          key === "name"
            ? boot.user.name
            : key === "phone"
              ? boot.user.phone
              : "";
  });
  if (picker && !checkout.elements.street.value) {
    const preferred = addresses.find((a) => a.default);
    if (preferred) {
      picker.value = preferred.id;
      chooseAddress(preferred.id);
    }
  }
  $("#save-checkout-address")?.addEventListener("click", async (event) => {
    const button = event.currentTarget;
    button.disabled = true;
    try {
      const address = Object.fromEntries(
        ["name", "phone", "province", "city", "street", "postal"].map((key) => [
          key,
          digits(checkout.elements[key].value),
        ]),
      );
      await request("/account/addresses", { label: "نشانی خرید", address });
      $("#checkout-address-feedback").textContent =
        "نشانی برای خریدهای بعدی در دفترچهٔ آدرس ذخیره شد.";
    } catch (error) {
      if (!signal?.aborted)
        $("#checkout-address-feedback").textContent = error.message;
    } finally {
      button.disabled = false;
    }
  });
};
window.initCustomerPanel();
