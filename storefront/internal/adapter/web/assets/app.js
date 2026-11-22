"use strict";
window.initStorefront = function initStorefront() {
  window.storefrontCleanup?.();
  const lifetime = new AbortController();
  window.storefrontSignal = lifetime.signal;
  window.storefrontCleanup = () => lifetime.abort();
  const boot = JSON.parse(document.getElementById("boot").textContent);
  const $ = (q, root = document) => root.querySelector(q);
  const $$ = (q, root = document) => [...root.querySelectorAll(q)];
  const esc = (v) =>
    String(v ?? "").replace(
      /[&<>"']/g,
      (c) =>
        ({
          "&": "&amp;",
          "<": "&lt;",
          ">": "&gt;",
          '"': "&quot;",
          "'": "&#39;",
        })[c],
    );
  const fa = (v) => new Intl.NumberFormat("fa-IR").format(v);
  const money = (v) => `${fa(v / 10)} <small>تومان</small>`;
  const number = (v) =>
    String(v)
      .replace(/[۰-۹]/g, (c) => "۰۱۲۳۴۵۶۷۸۹".indexOf(c))
      .replace(/[٠-٩]/g, (c) => "٠١٢٣٤٥٦٧٨٩".indexOf(c));
  function dateInput(value) {
    if (!value || value.startsWith("0001")) return "";
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return "";
    return new Date(date.getTime() - date.getTimezoneOffset() * 60000)
      .toISOString()
      .slice(0, 16);
  }
  let session = { user: boot.user, csrf: boot.csrf };
  let toastTimer;
  function toast(message, cartLink = false) {
    const node = $("#toast");
    if (!node || lifetime.signal.aborted) return;
    node.innerHTML =
      esc(message) +
      (cartLink ? '<a class="toast-cart" href="/cart">مشاهده سبد ↗</a>' : "");
    node.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => (node.hidden = true), 6000);
  }
  async function api(path, data, method = "POST") {
    const multipart = data instanceof FormData;
    const res = await fetch("/api" + path, {
      method,
      credentials: "same-origin",
      signal: lifetime.signal,
      headers: {
        ...(multipart ? {} : { "Content-Type": "application/json" }),
        ...(session.csrf ? { "X-CSRF-Token": session.csrf } : {}),
      },
      ...(data !== undefined
        ? { body: multipart ? data : JSON.stringify(data) }
        : {}),
    });
    let result = {};
    if (res.status !== 204) {
      try {
        result = await res.json();
      } catch {
        throw new Error("ارتباط با فروشگاه کامل نشد؛ دوباره تلاش کنید.");
      }
    }
    if (!res.ok) {
      if (res.status === 401) {
        throw new Error("نشست شما تمام شده است؛ دوباره وارد حساب شوید.");
      }
      throw new Error(result.error || "ثبت تغییرات انجام نشد.");
    }
    return result;
  }
  async function busy(button, action) {
    if (button?.disabled) return;
    const label = button?.innerHTML;
    if (button) {
      button.disabled = true;
      button.textContent = "کمی صبر کنید…";
    }
    try {
      return await action();
    } catch (e) {
      toast(e.message);
      throw e;
    } finally {
      if (button) {
        button.disabled = false;
        button.innerHTML = label;
      }
    }
  }
  function readLocal(key, fallback) {
    try {
      return JSON.parse(localStorage.getItem(key)) ?? fallback;
    } catch {
      return fallback;
    }
  }
  let cart = window.storefrontCart || readLocal("hera-cart-v1", []);
  if (!Array.isArray(cart)) cart = [];
  cart = cart
    .filter(
      (l) =>
        l &&
        typeof l.productId === "string" &&
        typeof l.variantId === "string" &&
        Number.isInteger(l.quantity) &&
        l.quantity > 0 &&
        l.quantity <= 100,
    )
    .map((l) => ({
      ...l,
      attachments: Array.isArray(l.attachments) ? l.attachments : [],
    }));
  function saveCart() {
    window.storefrontCart = cart;
    try {
      localStorage.setItem("hera-cart-v1", JSON.stringify(cart));
    } catch {
      toast(
        "فضای ذخیره مرورگر در دسترس نیست؛ سبد تا بستن این صفحه حفظ می‌شود.",
      );
    }
    updateCount();
  }
  function updateCount() {
    const count = cart.reduce((a, l) => a + l.quantity, 0);
    $$("[data-cart-count]").forEach((node) => {
      node.textContent = fa(count);
      node.classList.toggle("is-empty", count === 0);
    });
  }
  function sessionID() {
    let id;
    try {
      id = sessionStorage.getItem("hera-analytics");
      if (!id) {
        id = crypto.randomUUID();
        sessionStorage.setItem("hera-analytics", id);
      }
    } catch {
      id = crypto.randomUUID();
    }
    return id;
  }
  function event(kind, productId = "") {
    if (!boot.settings.modules.analytics) return;
    const url = new URL(location.href);
    void api("/events", {
      kind,
      productId,
      session: sessionID(),
      source: (url.searchParams.get("utm_source") || "").slice(0, 100),
      campaign: (url.searchParams.get("utm_campaign") || "").slice(0, 100),
    }).catch(() => {});
  }
  updateCount();
  if (!["seller", "admin", "login"].includes(boot.view)) {
    event("page_view");
    if (boot.view === "product") event("product_view", boot.product.id);
  }
  $("#search-toggle")?.addEventListener("click", () => {
    const f = $("#search-form");
    f.hidden = !f.hidden;
    if (!f.hidden) $("#search").focus();
  });
  let wishes = readLocal("hera-wishes", []);
  if (!Array.isArray(wishes)) wishes = [];
  function bindWishButtons(root = document) {
    $$(".wish", root).forEach((button) => {
      if (button.dataset.boundWish) return;
      button.dataset.boundWish = "1";
      button.classList.toggle("saved", wishes.includes(button.dataset.id));
      button.setAttribute(
        "aria-pressed",
        String(wishes.includes(button.dataset.id)),
      );
      button.addEventListener("click", () => {
        const id = button.dataset.id;
        wishes = wishes.includes(id)
          ? wishes.filter((x) => x !== id)
          : [...wishes, id];
        try {
          localStorage.setItem("hera-wishes", JSON.stringify(wishes));
        } catch {}
        $$(".wish")
          .filter((node) => node.dataset.id === id)
          .forEach((node) => {
            node.classList.toggle("saved", wishes.includes(id));
            node.setAttribute("aria-pressed", String(wishes.includes(id)));
          });
        toast(
          wishes.includes(id)
            ? "برای خرید بعدی ذخیره شد."
            : "از ذخیره‌ها حذف شد.",
        );
      });
    });
  }
  bindWishButtons();
  if ($("#add-form")) {
    const p = boot.product;
    const getVariant = () =>
      p.variants.find((v) => v.id === $("input[name=variant]:checked")?.value);
    function variantChanged() {
      const v = getVariant();
      if (!v) return;
      $("#variant-price").textContent = fa(v.price / 10);
      $("#stock-note").textContent =
        v.stock > 0 ? `${fa(v.stock)} عدد موجود / آماده سفارش` : "ناموجود";
      $("#quantity").max = Math.min(100, v.stock);
      if ($("#variant-sku")) $("#variant-sku").textContent = v.sku || "";
      if ($("#variant-selection"))
        $("#variant-selection").textContent = [v.colorName, v.size]
          .filter(Boolean)
          .join(" · ");
      const oldPrice = $("#variant-old-price");
      if (oldPrice) {
        oldPrice.hidden = !(v.oldPrice > v.price);
        oldPrice.textContent = fa((v.oldPrice || 0) / 10);
      }
      const image = $("#product-main-image");
      if (image) image.src = v.image || p.image;
      $("#quantity").value = Math.max(
        1,
        Math.min(Number($("#quantity").value), v.stock),
      );
      const variantURL = new URL(location.href);
      variantURL.searchParams.set("variant", v.id);
      history.replaceState(history.state, "", variantURL.href);
      $("#add-form button[type=submit]").disabled = v.stock === 0;
    }
    $$("input[name=variant]").forEach((el) =>
      el.addEventListener("change", variantChanged),
    );
    variantChanged();
    $$("[data-quantity]").forEach((b) =>
      b.addEventListener("click", () => {
        const el = $("#quantity");
        el.value = Math.max(
          1,
          Math.min(
            Number(el.max),
            Number(el.value) + Number(b.dataset.quantity),
          ),
        );
      }),
    );
    $("#add-form").addEventListener("submit", (e) => {
      e.preventDefault();
      const v = getVariant();
      if (!v) return;
      const qty = Number($("#quantity").value);
      let old = cart.find((l) => l.variantId === v.id);
      if ((old?.quantity || 0) + qty > v.stock) {
        toast("تعداد انتخاب‌شده از موجودی محصول بیشتر است.");
        return;
      }
      if (old) old.quantity += qty;
      else
        cart.push({
          productId: p.id,
          variantId: v.id,
          quantity: qty,
          attachments: [],
        });
      saveCart();
      event("add_to_cart", p.id);
      $("#add-feedback").textContent = "به سبد خرید شما اضافه شد.";
      toast("انتخاب شما به سبد خرید اضافه شد.", true);
    });
  }
  let authMode = "otp";
  $$("[data-auth-mode]").forEach((b) =>
    b.addEventListener("click", () => {
      authMode = b.dataset.authMode;
      $("#otp-section").hidden = authMode !== "otp";
      $("#password-section").hidden = authMode !== "password";
      $$(".segmented button").forEach((el) =>
        el.classList.toggle("active", el.dataset.authMode === authMode),
      );
      $("#auth-error").textContent = "";
    }),
  );
  function phoneValue() {
    let p = number($("#login-form [name=phone]").value).replace(/[\s()-]/g, "");
    if (p.startsWith("+98") || p.startsWith("0098") || p.startsWith("09"))
      return p;
    return "+98" + p;
  }
  $("#request-otp")?.addEventListener("click", async (e) => {
    try {
      await busy(e.target, async () => {
        const r = await api("/auth/otp", { phone: phoneValue() });
        $("#code-wrap").hidden = false;
        $("#login-form [name=code]").focus();
        $("#otp-hint").textContent = r.demoCode
          ? `کد آزمایشی: ${r.demoCode} — این کد در محیط واقعی نمایش داده نمی‌شود.`
          : "کد پیامکی تا دو دقیقه اعتبار دارد.";
        const b = e.target;
        let remaining = 60;
        setTimeout(() => {
          if (lifetime.signal.aborted) return;
          b.disabled = true;
          const timer = setInterval(() => {
            remaining--;
            b.textContent = `ارسال مجدد (${fa(remaining)})`;
            if (remaining <= 0) {
              clearInterval(timer);
              b.disabled = false;
              b.textContent = "دریافت مجدد کد";
            }
          }, 1000);
          lifetime.signal.addEventListener(
            "abort",
            () => clearInterval(timer),
            { once: true },
          );
        }, 0);
      });
    } catch (err) {
      $("#auth-error").textContent = err.message;
    }
  });
  $("#login-form")?.addEventListener("submit", async (e) => {
    e.preventDefault();
    try {
      await busy(
        $(
          "button[type=submit]",
          authMode === "otp" ? $("#code-wrap") : $("#password-section"),
        ),
        async () => {
          const result = await api("/auth/login", {
            phone: phoneValue(),
            password:
              authMode === "password"
                ? $("#login-form [name=password]").value
                : "",
            code:
              authMode === "otp"
                ? number($("#login-form [name=code]").value)
                : "",
          });
          session = result;
          let next = new URL(location.href).searchParams.get("next");
          if (!["/checkout", "/account", "/seller", "/admin"].includes(next))
            next =
              result.user.role === "admin"
                ? "/admin"
                : result.user.role === "seller"
                  ? "/seller"
                  : "/account";
          location.assign(next);
        },
      );
    } catch (err) {
      $("#auth-error").textContent = err.message;
    }
  });
  $("#logout")?.addEventListener("click", async (e) => {
    try {
      await busy(e.target, async () => {
        await api("/auth/logout", {});
        location.assign("/");
      });
    } catch {}
  });
  $("#set-password")?.addEventListener("submit", async (e) => {
    e.preventDefault();
    try {
      await busy($("button", e.target), async () => {
        await api("/auth/password", {
          password: $("[name=password]", e.target).value,
        });
        $(".form-feedback", e.target).textContent =
          "رمز عبور با موفقیت ذخیره شد.";
        e.target.reset();
      });
    } catch {}
  });
  // Legacy products can contain null collections; keep cart and checkout usable
  // while old responses or an in-flight deployment are present.
  function catalogProducts(value) {
    if (!Array.isArray(value))
      throw new Error("اطلاعات محصولات کامل دریافت نشد؛ دوباره تلاش کنید.");
    return value
      .filter((p) => p && typeof p.id === "string")
      .map((p) => ({
        ...p,
        variants: Array.isArray(p.variants) ? p.variants : [],
        attachments: Array.isArray(p.attachments) ? p.attachments : [],
      }));
  }
  function cartDetails(products) {
    return cart.map((l) => {
      const p = products.find((p) => p.id === l.productId);
      return {
        line: l,
        product: p,
        variant: p?.variants.find((v) => v.id === l.variantId),
      };
    });
  }
  function totals(q) {
    return `<div class="totals-line"><span>جمع محصولات</span><strong>${money(q.subtotal)}</strong></div><div class="totals-line"><span>ارسال</span><strong>${q.shipping == null ? "پس از انتخاب روش تحویل" : q.shipping === 0 ? "رایگان" : money(q.shipping)}</strong></div>${q.discount ? `<div class="totals-line"><span>تخفیف شما</span><strong>− ${money(q.discount)}</strong></div>` : ""}<div class="totals-line total"><span>${q.shipping == null ? "جمع بدون هزینهٔ ارسال" : "مبلغ قابل پرداخت"}</span><strong>${money(q.total)}</strong></div>`;
  }
  function shippingNudge(subtotal) {
    if (boot.settings.deliveryMethods?.length)
      return '<p class="small muted">هزینهٔ ارسال پس از انتخاب روش تحویل و نشانی محاسبه می‌شود.</p>';
    const threshold = boot.settings.freeShipping;
    if (!threshold) return "";
    const remaining = Math.max(0, threshold - subtotal);
    return `<div class="shipping-nudge"><strong>${remaining ? `با ${money(remaining)} خرید بیشتر، ارسال این سبد رایگان می‌شود.` : "ارسال رایگان برای این سبد فعال است."}</strong><progress max="${threshold}" value="${Math.min(subtotal, threshold)}"></progress>${remaining ? '<a href="/shop">یک انتخاب دیگر پیدا کن ←</a>' : ""}</div>`;
  }
  async function initCart() {
    const root = $("#cart-content");
    if (!root) return;
    try {
      const ps = catalogProducts(await api("/products", undefined, "GET"));
      function render() {
        if (!cart.length) {
          root.innerHTML =
            '<div class="empty-state"><span>⌑</span><h2>سبد شما منتظر یک انتخاب خوب است</h2><p>محصولاتی را که دوست دارید، به سبد اضافه کنید.</p><a class="btn" href="/shop">کشف فروشگاه ↗</a></div>';
          return;
        }
        const ds = cartDetails(ps);
        let sub = 0;
        root.innerHTML = `<div>${ds
          .map(({ line: l, product: p, variant: v }) => {
            if (!p || !v)
              return `<div class="cart-item"><p>این محصول دیگر در دسترس نیست.</p><button class="remove-item" data-variant="${esc(l.variantId)}">حذف از سبد</button></div>`;
            sub += v.price * l.quantity;
            return `<article class="cart-item"><img src="${esc(p.image)}" alt="${esc(p.name)}"><div class="cart-item-body"><h3><a href="/products/${esc(p.slug)}">${esc(p.name)}</a></h3><p>${esc(v.name)}${boot.settings.modules.attachments && p.attachments.length ? " · نیازمند فایل اختصاصی" : ""}</p><div class="cart-item-actions"><div class="quantity-control"><button type="button" data-cart-change="-1" data-variant="${esc(v.id)}" aria-label="کاهش تعداد">−</button><span>${fa(l.quantity)}</span><button type="button" data-cart-change="1" data-variant="${esc(v.id)}" aria-label="افزایش تعداد">+</button></div><strong class="price">${money(v.price * l.quantity)}</strong><button class="remove-item" data-variant="${esc(v.id)}">حذف</button></div>${v.stock < l.quantity ? '<p class="form-error">تعداد انتخابی از موجودی بیشتر است. تعداد را کاهش دهید.</p>' : ""}</div></article>`;
          })
          .join(
            "",
          )}</div><aside class="order-summary"><h2>خلاصه سبد خرید</h2>${shippingNudge(sub)}${totals({ subtotal: sub, shipping: boot.settings.deliveryMethods?.length ? null : boot.settings.freeShipping && sub >= boot.settings.freeShipping ? 0 : boot.settings.shipping, discount: 0, total: sub + (boot.settings.deliveryMethods?.length || (boot.settings.freeShipping && sub >= boot.settings.freeShipping) ? 0 : boot.settings.shipping) })}<a href="/checkout" class="btn full">ادامه و تکمیل سفارش ↗</a><p class="small muted">قیمت و موجودی هنگام ثبت سفارش دوباره بررسی می‌شوند.</p></aside>`;
        $$(".remove-item", root).forEach(
          (b) =>
            (b.onclick = () => {
              cart = cart.filter((l) => l.variantId !== b.dataset.variant);
              saveCart();
              render();
            }),
        );
        $$("[data-cart-change]", root).forEach(
          (b) =>
            (b.onclick = () => {
              const l = cart.find((l) => l.variantId === b.dataset.variant);
              const v = ps
                .flatMap((p) => p.variants)
                .find((v) => v.id === l.variantId);
              const n = l.quantity + Number(b.dataset.cartChange);
              if (n < 1) return;
              if (n > Math.min(100, v.stock)) {
                toast("موجودی کافی نیست.");
                return;
              }
              l.quantity = n;
              saveCart();
              render();
            }),
        );
      }
      render();
    } catch (e) {
      root.innerHTML = `<div class="empty-state"><h2>دریافت سبد ممکن نشد</h2><p>${esc(e.message)}</p><button class="btn" id="retry-cart">تلاش دوباره</button></div>`;
      $("#retry-cart").onclick = initCart;
    }
  }
  void initCart();
  if ($("#checkout-form"))
    void (async () => {
      const form = $("#checkout-form");
      let products = [],
        quote = null,
        code = "",
        quoteGeneration = 0;
      let requestKey;
      try {
        requestKey = sessionStorage.getItem("hera-checkout-key");
        if (!requestKey) {
          requestKey = crypto.randomUUID();
          sessionStorage.setItem("hera-checkout-key", requestKey);
        }
      } catch {
        requestKey = crypto.randomUUID();
      }
      const err = $("#checkout-error");
      try {
        products = catalogProducts(await api("/products", undefined, "GET"));
      } catch (e) {
        err.textContent = e.message;
        return;
      }
      const ds = cartDetails(products);
      if (!ds.length) {
        form.innerHTML =
          '<div class="empty-state"><h2>سبد خرید شما خالی است</h2><a class="btn" href="/shop">بازگشت به فروشگاه ↗</a></div>';
        return;
      }
      $("#checkout-lines").innerHTML = ds
        .map(
          ({ product: p, variant: v, line: l }) =>
            `<div class="order-line"><span>${esc(p?.name || "محصول حذف شده")}<small class="muted"> × ${fa(l.quantity)}</small></span><strong>${v ? money(v.price * l.quantity) : "—"}</strong></div>`,
        )
        .join("");
      let requiredMissing = false;
      const uploadRoot = $("#upload-fields");
      for (const { product: p, variant: v, line: l } of ds) {
        if (!p || !v) continue;
        for (const rule of boot.settings.modules.attachments
          ? p.attachments
          : []) {
          $("#checkout-attachments").hidden = false;
          const div = document.createElement("div");
          div.className = "upload-item";
          div.innerHTML = `<h3>${esc(p.name)} · ${esc(v.name)}</h3><label>${esc(rule.label)} <small>(${fa(rule.min * (rule.perUnit ? l.quantity : 1))} تا ${fa(rule.max * (rule.perUnit ? l.quantity : 1))} فایل)</small></label><p class="muted">${esc(rule.help)} · هر فایل حداکثر ${fa(rule.maxBytes / 1048576)} مگابایت</p><input type="file" multiple accept="${esc(rule.types.join(","))}"><div class="file-results"></div>`;
          uploadRoot.append(div);
          $("input", div).addEventListener("change", async (e) => {
            const input = e.target;
            const files = [...input.files];
            if (files.length > rule.max * (rule.perUnit ? l.quantity : 1)) {
              toast("تعداد فایل‌ها بیش از حد مجاز است.");
              return;
            }
            input.disabled = true;
            try {
              for (const file of files) {
                if (file.size > rule.maxBytes) {
                  throw new Error("حجم فایل بیش از حد مجاز است.");
                }
                const data = new FormData();
                data.set("file", file);
                data.set("productId", p.id);
                data.set("variantId", v.id);
                data.set("ruleId", rule.id);
                const upload = await api("/uploads", data);
                l.attachments.push(upload.id);
                const row = document.createElement("span");
                row.className = "uploaded-file";
                row.textContent = "✓ " + upload.name;
                $(".file-results", div).append(row);
              }
              saveCart();
              await refreshQuote();
            } catch (e) {
              toast(e.message);
            } finally {
              input.disabled = false;
            }
          });
          if (rule.min > 0 && !l.attachments.length) requiredMissing = true;
        }
      }
      async function refreshQuote() {
        const generation = ++quoteGeneration;
        $("#pay-button").disabled = true;
        try {
          const delivery = {
            methodId: $("input[name=deliveryMethod]:checked")?.value || "",
            province: form.elements.province.value,
            city: form.elements.city.value,
          };
          const q = await api("/quote", { lines: cart, code, delivery });
          if (generation !== quoteGeneration || lifetime.signal.aborted) return;
          quote = q;
          const date = (value) =>
            new Intl.DateTimeFormat("fa-IR", {
              month: "long",
              day: "numeric",
              timeZone: "Asia/Tehran",
            }).format(new Date(value + "T12:00:00+03:30"));
          $("#delivery-options").innerHTML = q.deliveryOptions
            .map(
              (d) =>
                `<label class="delivery-option"><input type="radio" name="deliveryMethod" value="${esc(d.methodId)}" ${d.methodId === q.delivery.methodId ? "checked" : ""}><span><strong>${esc(d.name)}</strong><small>${d.kind === "pickup" ? "آمادهٔ دریافت" : "تحویل تخمینی"}: ${esc(date(d.from))} تا ${esc(date(d.to))}</small>${d.pickupAddress ? `<small>${esc(d.pickupAddress)}</small>` : ""}</span><b>${d.fee ? money(d.fee) : "رایگان"}</b></label>`,
            )
            .join("");
          $$("input[name=deliveryMethod]").forEach((input) =>
            input.addEventListener("change", refreshQuote, {
              signal: lifetime.signal,
            }),
          );
          $("#checkout-totals").innerHTML = totals(q);
          const selected = $("input[name=gateway]:checked")?.value;
          $("#gateway-options").innerHTML = q.gateways.length
            ? q.gateways
                .map(
                  (g, i) =>
                    `<label class="gateway-option"><input type="radio" name="gateway" value="${esc(g.id)}" ${g.id === selected || (!selected && i === 0) ? "checked" : ""}><span>${esc(g.name)}<small>${g.description ? esc(g.description) : g.bnpl ? "پرداخت اعتباری با تأیید ارائه‌دهنده" : "پرداخت یک‌جا"}${g.id === "demo" ? " — فقط محیط آزمایشی" : ""}</small></span><b>↗</b></label>`,
                )
                .join("")
            : '<div class="info-box">برای این سبد هنوز روش پرداخت آماده‌ای فعال نیست. می‌توانید محصولات را تغییر دهید یا با پشتیبانی تماس بگیرید.</div>';
          $("#pay-button").disabled = !q.gateways.length;
          err.textContent = "";
          $("#coupon-feedback").textContent = code
            ? "کد تخفیف بررسی و اعمال شد."
            : "";
        } catch (e) {
          if (generation !== quoteGeneration || lifetime.signal.aborted) return;
          quote = null;
          if ($("#delivery-options"))
            $("#delivery-options").innerHTML =
              "<p class=muted>استان و شهر را بررسی کنید و دوباره روش تحویل را انتخاب کنید.</p>";
          err.textContent = e.message;
          $("#gateway-options").innerHTML =
            '<div class="info-box">پس از تکمیل فایل‌های موردنیاز و بررسی سبد، روش‌های پرداخت نمایش داده می‌شوند. <button type="button" class="text-link" id="retry-quote">بررسی دوباره سبد ↻</button></div>';
          $("#checkout-totals").innerHTML =
            '<p class="small muted">مبلغ نهایی پس از تکمیل اطلاعات سبد محاسبه می‌شود.</p>';
          $("#retry-quote").onclick = refreshQuote;
          if (code) $("#coupon-feedback").textContent = e.message;
        }
      }
      let deliveryTimer;
      form.addEventListener(
        "input",
        (e) => {
          if (["province", "city"].includes(e.target.name)) {
            quote = null;
            $("#pay-button").disabled = true;
            clearTimeout(deliveryTimer);
            deliveryTimer = setTimeout(() => {
              if (!lifetime.signal.aborted) void refreshQuote();
            }, 450);
          }
        },
        { signal: lifetime.signal },
      );
      form.addEventListener(
        "change",
        (e) => {
          if (e.target.id === "checkout-saved-address") void refreshQuote();
        },
        { signal: lifetime.signal },
      );
      document.addEventListener(
        "storefront:address-changed",
        () => void refreshQuote(),
        { signal: lifetime.signal },
      );
      $("#apply-coupon").onclick = async (e) => {
        code = $("#coupon").value.trim();
        try {
          await busy(e.target, refreshQuote);
        } catch {}
      };
      form.addEventListener("submit", async (e) => {
        e.preventDefault();
        if (!quote) return;
        if (!form.reportValidity()) return;
        const button = $("#pay-button");
        try {
          await busy(button, async () => {
            const f = new FormData(form);
            const address = Object.fromEntries(
              ["name", "phone", "province", "city", "street", "postal"].map(
                (k) => [k, number(f.get(k) || "")],
              ),
            );
            const out = await api("/checkout", {
              lines: cart,
              code,
              gateway: f.get("gateway"),
              delivery: { methodId: quote.delivery.methodId },
              customerNote: f.get("customerNote") || "",
              address,
              key: requestKey,
            });
            cart = [];
            saveCart();
            try {
              sessionStorage.removeItem("hera-checkout-key");
            } catch {}
            location.assign(out.redirect || "/account");
          });
        } catch (e) {
          err.textContent = e.message;
        }
      });
      event("begin_checkout");
      await refreshQuote();
      if (requiredMissing)
        err.textContent =
          "پیش از پرداخت، فایل‌های موردنیاز محصولات را بارگذاری کنید.";
    })();
  $("#demo-pay")?.addEventListener("click", async (e) => {
    try {
      await busy(e.target, async () => {
        const r = await api("/payment/demo/" + e.target.dataset.order, {});
        location.assign(r.redirect);
      });
    } catch {}
  });
  const modal = $("#modal");
  $(".dialog-close", modal).onclick = () => modal.close();
  modal.addEventListener("click", (e) => {
    if (e.target === modal) {
      const r = modal.getBoundingClientRect();
      if (
        e.clientX < r.left ||
        e.clientX > r.right ||
        e.clientY < r.top ||
        e.clientY > r.bottom
      )
        modal.close();
    }
  });
  function openModal(html) {
    $("#modal-body").innerHTML = html;
    modal.showModal();
  }
  function field(label, name, value = "", type = "text", extra = "") {
    return `<label>${esc(label)}<input name="${esc(name)}" type="${type}" value="${esc(value)}" ${extra}></label>`;
  }
  function textArea(label, name, value = "", extra = "") {
    return `<label class="span-2">${esc(label)}<textarea name="${esc(name)}" ${extra}>${esc(value)}</textarea></label>`;
  }
  function selectField(label, name, options, value) {
    return `<label>${esc(label)}<select name="${esc(name)}">${options.map(([v, t]) => `<option value="${esc(v)}" ${v === value ? "selected" : ""}>${esc(t)}</option>`).join("")}</select></label>`;
  }
  function check(label, name, checked) {
    return `<label class="checkbox-label"><span><input name="${esc(name)}" type="checkbox" ${checked ? "checked" : ""}> ${esc(label)}</span></label>`;
  }
  function modalForm(title, html, submit) {
    openModal(
      `<h2>${esc(title)}</h2><form class="stack-form" id="editor-form"><div class="form-grid">${html}</div><button class="btn" type="submit">ذخیره تغییرات ↗</button><p class="form-error" role="alert"></p></form>`,
    );
    $("#editor-form").onsubmit = async (e) => {
      e.preventDefault();
      try {
        await busy($("button[type=submit]", e.target), async () => {
          await submit(new FormData(e.target), e.target);
          modal.close();
          toast("تغییرات ذخیره شد.");
          setTimeout(() => location.reload(), 400);
        });
      } catch (err) {
        $(".form-error", e.target).textContent = err.message;
      }
    };
  }
  if (["seller", "admin"].includes(boot.view)) {
    const settings = boot.settings;
    let tab = "overview";
    try {
      tab = sessionStorage.getItem("hera-panel-tab") || tab;
    } catch {}
    if (!$(`[data-panel="${CSS.escape(tab)}"]`)) tab = "overview";
    function showTab(t) {
      $$("[data-panel]").forEach((p) => (p.hidden = p.dataset.panel !== t));
      $$("[data-tab]").forEach((b) =>
        b.classList.toggle("active", b.dataset.tab === t),
      );
      try {
        sessionStorage.setItem("hera-panel-tab", t);
      } catch {}
      if (t === "campaigns") void loadCampaigns();
    }
    showTab(tab);
    $$("[data-tab]").forEach((b) => (b.onclick = () => showTab(b.dataset.tab)));
    async function saveSettings(next) {
      await api("/seller/settings", next);
    }
    function renderSections() {
      const root = $("#section-list");
      root.innerHTML = settings.sections
        .map(
          (s, i) =>
            `<div class="section-row"><input type="checkbox" data-section-enable="${i}" ${s.enabled ? "checked" : ""} aria-label="فعال بودن ${esc(s.title)}"><input type="text" data-section-title="${i}" value="${esc(s.title)}" aria-label="عنوان بخش"><button type="button" data-section-move="${i}" data-delta="-1" aria-label="انتقال به بالا">↑</button><button type="button" data-section-move="${i}" data-delta="1" aria-label="انتقال به پایین">↓</button></div>`,
        )
        .join("");
      $$("[data-section-enable]", root).forEach(
        (el) =>
          (el.onchange = () =>
            (settings.sections[Number(el.dataset.sectionEnable)].enabled =
              el.checked)),
      );
      $$("[data-section-title]", root).forEach(
        (el) =>
          (el.oninput = () =>
            (settings.sections[Number(el.dataset.sectionTitle)].title =
              el.value)),
      );
      $$("[data-section-move]", root).forEach(
        (el) =>
          (el.onclick = () => {
            const i = Number(el.dataset.sectionMove),
              j = i + Number(el.dataset.delta);
            if (j < 0 || j >= settings.sections.length) return;
            [settings.sections[i], settings.sections[j]] = [
              settings.sections[j],
              settings.sections[i],
            ];
            renderSections();
          }),
      );
    }
    renderSections();
    $("#logo-file")?.addEventListener("change", async (e) => {
      const file = e.target.files[0];
      if (!file) return;
      const data = new FormData();
      data.set("file", file);
      try {
        const r = await api("/seller/media", data);
        $("#appearance-form [name=logo]").value = r.url;
        toast("لوگو بارگذاری شد؛ برای انتشار، تغییرات را ذخیره کنید.");
      } catch (e) {
        toast(e.message);
      }
    });
    const contentFields = {
      heroTitle: "عنوان اصلی ویترین",
      brandSubtitle: "زیرعنوان برند",
      heroKicker: "بالانویس بخش اصلی",
      heroText: "توضیح بخش اصلی",
      heroButton: "متن دکمه اصلی",
      heroCaption: "عنوان روی تصویر",
      heroCaptionSub: "زیرعنوان روی تصویر",
      storyText: "متن معرفی برند",
      storyText2: "ادامه معرفی برند",
      footerText: "متن پایین لوگو",
      aboutText: "درباره فروشگاه",
      shippingPolicy: "شرایط ارسال",
      returnPolicy: "شرایط بازگشت",
      contactPhone: "تلفن تماس عمومی",
      contactAddress: "نشانی تماس",
      heroImage: "تصویر اصلی سایت",
      storyImage: "تصویر معرفی برند",
    };
    $("#content-fields").innerHTML = Object.entries(contentFields)
      .map(([key, label]) =>
        key.endsWith("Image")
          ? field(label, "content:" + key, settings.content?.[key] || "") +
            '<label>بارگذاری تصویر<input type="file" class="content-image-file" data-target="' +
            key +
            '" accept="image/jpeg,image/png,image/webp"></label>'
          : textArea(label, "content:" + key, settings.content?.[key] || ""),
      )
      .join("");
    $$(".content-image-file").forEach((input) =>
      input.addEventListener("change", async () => {
        const file = input.files[0];
        if (!file) return;
        const data = new FormData();
        data.set("file", file);
        try {
          const out = await api("/seller/media", data);
          $(
            '#appearance-form [name="content:' + input.dataset.target + '"]',
          ).value = out.url;
          toast("تصویر بارگذاری شد؛ برای انتشار، تنظیمات را ذخیره کنید.");
        } catch (e) {
          toast(e.message);
        }
      }),
    );
    $("#appearance-form").onsubmit = async (e) => {
      e.preventDefault();
      try {
        await busy($("button[type=submit]", e.target), async () => {
          const f = new FormData(e.target);
          const next = {
            ...settings,
            content: Object.fromEntries(
              Object.keys(contentFields).map((key) => [
                key,
                f.get("content:" + key),
              ]),
            ),
            name: f.get("name"),
            tagline: f.get("tagline"),
            announcement: f.get("announcement"),
            logo: f.get("logo"),
            accent: f.get("accent"),
            shipping: Number(f.get("shipping")) * 10,
            freeShipping: Number(f.get("freeShipping")) * 10,
            gapchiUrl: f.get("gapchiUrl") ?? settings.gapchiUrl,
            gapchiChannel: f.get("gapchiChannel") ?? settings.gapchiChannel,
          };
          await saveSettings(next);
          location.reload();
        });
      } catch {}
    };
    if ($("#launcher-form")) {
      const moduleNames = {
        blog: "وبلاگ",
        attachments: "پیوست سفارش",
        discounts: "تخفیف و کمپین",
        wishlist: "علاقه‌مندی",
        support: "پشتیبانی گپچی",
        analytics: "آنالیتیکس",
      };
      $("#module-list").innerHTML = Object.entries(moduleNames)
        .map(([key, label]) => check(label, key, settings.modules[key]))
        .join("");
      $("#launcher-form").onsubmit = async (e) => {
        e.preventDefault();
        try {
          await busy($("button[type=submit]", e.target), async () => {
            const f = new FormData(e.target);
            const theme = f.get("theme");
            const chosen = $("input[name=theme]:checked")?.closest(
              ".theme-choice",
            );
            const accent = chosen
              ? getComputedStyle($(".theme-preview", chosen))
                  .getPropertyValue("--preview-accent")
                  .trim()
              : settings.accent;
            await saveSettings({
              ...settings,
              theme,
              accent,
              modules: Object.fromEntries(
                Object.keys(moduleNames).map((k) => [k, f.has(k)]),
              ),
            });
            location.reload();
          });
        } catch {}
      };
    }
    const gatewayFields = {
      zibal: [["merchant", "کد پذیرنده زیبال"]],
      zarinpal: [["merchant", "شناسه پذیرنده زرین‌پال"]],
      snappay: [
        ["base", "آدرس API پذیرندگی"],
        ["clientId", "Client ID"],
        ["clientSecret", "Client Secret"],
        ["username", "نام کاربری"],
        ["password", "رمز پذیرندگی"],
      ],
      torobpay: [
        ["base", "آدرس API پذیرندگی"],
        ["clientId", "Client ID"],
        ["clientSecret", "Client Secret"],
        ["username", "نام کاربری"],
        ["password", "رمز پذیرندگی"],
      ],
      digipay: [
        ["clientId", "Client ID"],
        ["clientSecret", "Client Secret"],
        ["username", "نام کاربری"],
        ["password", "رمز پذیرندگی"],
      ],
      tara: [
        ["username", "نام کاربری"],
        ["password", "رمز پذیرندگی"],
        ["serviceId", "شناسه سرویس"],
      ],
      azkivam: [
        ["merchantId", "شناسه پذیرنده"],
        ["username", "نام کاربری پذیرندگی"],
        ["password", "رمز پذیرندگی"],
      ],
    };
    $("#gateway-list").innerHTML = settings.gateways
      .map(
        (g) =>
          `<article class="form-card gateway-card"><div class="gateway-icon">${g.bnpl ? "◷" : "↗"}</div><header><h2>${esc(g.name)}</h2><span class="status-pill">${g.enabled ? "فعال" : "غیرفعال"}</span></header><p>${g.bnpl ? "خرید اعتباری · امکان تعیین محصولات و تنوع‌های مجاز" : "پرداخت یک‌جا از درگاه بانکی"}</p><p>${g.configured ? "اطلاعات اتصال ذخیره شده است." : "اطلاعات پذیرندگی هنوز وارد نشده است."}</p><button class="text-link configure-gateway" data-id="${esc(g.id)}">تنظیمات ${g.bnpl ? "و محدودیت محصولات " : ""}↗</button></article>`,
      )
      .join("");
    $$(".configure-gateway").forEach(
      (b) =>
        (b.onclick = () => {
          const g = settings.gateways.find((g) => g.id === b.dataset.id);
          const fields = gatewayFields[g.id] || [];
          let html =
            check("این درگاه فعال باشد", "enabled", g.enabled) +
            `<p class="small muted">برای حفظ اطلاعات قبلی، فیلدهای محرمانه را خالی بگذارید. مبلغ‌ها با واحد ریال به درگاه ارسال می‌شوند.</p>` +
            fields
              .map(([key, label]) =>
                field(
                  label,
                  key,
                  "",
                  key.toLowerCase().includes("secret") ||
                    key.toLowerCase().includes("password") ||
                    key === "apiKey"
                    ? "password"
                    : "text",
                  'autocomplete="off" dir="ltr"',
                ),
              )
              .join("");
          if (g.bnpl) {
            html +=
              selectField(
                "دسترسی محصولات",
                "mode",
                [
                  ["all", "همه محصولات"],
                  ["whitelist", "فقط محصولات انتخاب‌شده"],
                  ["blacklist", "همه به‌جز محصولات انتخاب‌شده"],
                ],
                g.policy.mode,
              ) +
              `<div class="span-2"><h3>انتخاب محصولات یا تنوع‌ها</h3>${boot.products.map((p) => `<div class="editor-row">${check(p.name, "p:" + p.id, g.policy.products?.includes(p.id))}${p.variants.map((v) => check("↳ " + v.name, "v:" + v.id, g.policy.variants?.includes(v.id))).join("")}</div>`).join("")}</div>`;
          }
          modalForm("تنظیمات " + g.name, html, async (f) => {
            const entered = fields.some(([key]) => f.get(key));
            let credential = "";
            if (entered) {
              if (fields.some(([key]) => !f.get(key)))
                throw new Error(
                  "برای تغییر اتصال، تمام اطلاعات پذیرندگی را وارد کنید.",
                );
              credential = ["zibal", "zarinpal"].includes(g.id)
                ? f.get("merchant")
                : JSON.stringify(
                    Object.fromEntries(
                      fields.map(([key]) => [key, f.get(key)]),
                    ),
                  );
            }
            const next = {
              ...g,
              enabled: f.has("enabled"),
              credential,
              policy: g.bnpl
                ? {
                    mode: f.get("mode"),
                    products: boot.products
                      .filter((p) => f.has("p:" + p.id))
                      .map((p) => p.id),
                    variants: boot.products
                      .flatMap((p) => p.variants)
                      .filter((v) => f.has("v:" + v.id))
                      .map((v) => v.id),
                  }
                : g.policy,
            };
            await saveSettings({
              ...settings,
              gateways: settings.gateways.map((x) =>
                x.id === g.id ? next : x,
              ),
            });
          });
        }),
    );
    function variantEditor(v = {}, i) {
      return `<div class="editor-row variant-editor" data-index="${i}" data-id="${esc(v.id || "")}"><div class="form-grid">${field("نام تنوع", "variantName" + i, v.name || "مدل اصلی")}${field("کد کالا (SKU)", "variantSku" + i, v.sku || "")}${field("قیمت (تومان)", "variantPrice" + i, (v.price || 0) / 10, "number", 'min="1000" required')}${field("موجودی", "variantStock" + i, v.stock || 0, "number", 'min="0" required')}${field("نام رنگ", "variantColorName" + i, v.colorName || "")}${check("نمایش نمونهٔ رنگ", "variantHasColor" + i, !!v.color)}${field("انتخاب رنگ نمونه", "variantColor" + i, v.color || "#17655d", "color")}${field("سایز / اندازه", "variantSize" + i, v.size || "")}${field("قیمت قبل از تخفیف (تومان)", "variantOldPrice" + i, (v.oldPrice || 0) / 10, "number", 'min="0"')}${field("تصویر این تنوع", "variantImage" + i, v.image || "", "text", 'dir="ltr" placeholder="/media/..."')}<label>بارگذاری تصویر این تنوع<input type="file" class="variant-image-file" data-index="${i}" accept="image/jpeg,image/png,image/webp"></label></div><button class="text-link remove-variant" type="button">حذف این تنوع</button></div>`;
    }
    function ruleEditor(a = {}, i) {
      return `<div class="editor-row rule-editor" data-id="${esc(a.id || "")}"><div class="form-grid">${field("عنوان پیوست", "ruleLabel" + i, a.label || "")}${field("راهنمای مشتری", "ruleHelp" + i, a.help || "")}${field("حداقل تعداد", "ruleMin" + i, a.min || 0, "number", 'min="0"')}${field("حداکثر تعداد", "ruleMax" + i, a.max || 3, "number", 'min="1" max="20"')}${field("حداکثر حجم هر فایل (MB)", "ruleSize" + i, (a.maxBytes || 5242880) / 1048576, "number", 'min="1" max="20"')}${check("به‌ازای هر واحد از محصول", "ruleUnit" + i, a.perUnit)}<div class="span-2">${[
        ["image/jpeg", "تصویر JPEG"],
        ["image/png", "تصویر PNG"],
        ["application/pdf", "فایل PDF"],
      ]
        .map(([v, t]) =>
          check(
            t,
            "ruleType" + i + v,
            (
              a.types || ["image/jpeg", "image/png", "application/pdf"]
            ).includes(v),
          ),
        )
        .join("")}</div></div></div>`;
    }
    function productEditor(
      p = {
        id: "",
        name: "",
        slug: "",
        category: "",
        description: "",
        image: "/assets/vase.jpg",
        active: true,
        variants: [],
        attachments: [],
      },
    ) {
      modalForm(
        p.id ? "ویرایش محصول" : "محصول تازه",
        field("نام محصول", "name", p.name, "text", "required") +
          field(
            "آدرس کوتاه صفحه",
            "slug",
            p.slug,
            "text",
            'dir="ltr" required pattern="[a-z0-9-]+"',
          ) +
          field("دسته‌بندی", "category", p.category, "text", "required") +
          field("تصویر محصول", "image", p.image, "text", 'dir="ltr" required') +
          '<label>بارگذاری تصویر<input type="file" id="product-image-file" accept="image/jpeg,image/png,image/webp"></label>' +
          textArea("توضیحات", "description", p.description) +
          field("برچسب ویترین", "badge", p.badge || "") +
          field("برند", "brand", p.brand || "") +
          field("گارانتی / ضمانت", "guarantee", p.guarantee || "") +
          field(
            "وزن بسته (گرم)",
            "weightGrams",
            p.weightGrams || 0,
            "number",
            'min="0" max="1000000"',
          ) +
          textArea(
            "تصاویر گالری؛ هر نشانی در یک خط",
            "images",
            (p.images || []).join("\n"),
          ) +
          '<label>افزودن عکس به گالری<input type="file" id="gallery-files" multiple accept="image/jpeg,image/png,image/webp"></label>' +
          textArea(
            "مشخصات؛ هر خط به شکل عنوان: مقدار",
            "specs",
            Object.entries(p.specs || {})
              .map(([k, v]) => k + ": " + v)
              .join("\n"),
          ) +
          check("محصول فعال باشد", "active", p.active) +
          `<div class="span-2"><div class="editor-actions"><h3>تنوع‌ها</h3><button class="text-link" id="add-variant" type="button">تنوع تازه +</button></div><div id="variant-editors">${(p.variants.length ? p.variants : [{}]).map(variantEditor).join("")}</div></div>` +
          `<div class="span-2"><div class="editor-actions"><h3>فایل‌های موردنیاز مشتری</h3><button class="text-link" id="add-rule" type="button">پیوست تازه +</button></div><div id="rule-editors">${p.attachments.map(ruleEditor).join("")}</div><p class="small muted">برای محصول بدون پیوست، این بخش را خالی بگذارید.</p></div>`,
        async (f, form) => {
          const variants = $$(".variant-editor", form).map((el) => {
            const i = Number(el.dataset.index);
            return {
              id: el.dataset.id || crypto.randomUUID(),
              name: f.get("variantName" + i),
              color: f.has("variantHasColor" + i)
                ? f.get("variantColor" + i)
                : "",
              colorName: f.get("variantColorName" + i) || "",
              size: f.get("variantSize" + i) || "",
              image: f.get("variantImage" + i) || "",
              oldPrice: Number(f.get("variantOldPrice" + i) || 0) * 10,
              sku: f.get("variantSku" + i),
              price: Number(f.get("variantPrice" + i)) * 10,
              stock: Number(f.get("variantStock" + i)),
              stockChange:
                Number(f.get("variantStock" + i)) -
                (p.variants.find((v) => v.id === el.dataset.id)?.stock || 0),
            };
          });
          const attachments = $$(".rule-editor", form).map((el, i) => ({
            id: el.dataset.id || crypto.randomUUID(),
            label: f.get("ruleLabel" + i),
            help: f.get("ruleHelp" + i),
            min: Number(f.get("ruleMin" + i)),
            max: Number(f.get("ruleMax" + i)),
            maxBytes: Number(f.get("ruleSize" + i)) * 1048576,
            perUnit: f.has("ruleUnit" + i),
            types: ["image/jpeg", "image/png", "application/pdf"].filter((t) =>
              f.has("ruleType" + i + t),
            ),
          }));
          await api("/seller/products", {
            ...p,
            name: f.get("name"),
            slug: f.get("slug"),
            category: f.get("category"),
            description: f.get("description"),
            image: f.get("image"),
            badge: f.get("badge"),
            brand: f.get("brand"),
            guarantee: f.get("guarantee"),
            weightGrams: Number(f.get("weightGrams") || 0),
            images: String(f.get("images") || "")
              .split("\n")
              .map((x) => x.trim())
              .filter(Boolean),
            specs: Object.fromEntries(
              String(f.get("specs") || "")
                .split("\n")
                .filter((x) => x.includes(":"))
                .map((x) => {
                  const i = x.indexOf(":");
                  return [x.slice(0, i).trim(), x.slice(i + 1).trim()];
                }),
            ),
            active: f.has("active"),
            variants,
            attachments,
          });
        },
      );
      $("#gallery-files")?.addEventListener("change", async (e) => {
        const input = e.target;
        input.disabled = true;
        try {
          for (const file of input.files) {
            const data = new FormData();
            data.set("file", file);
            const out = await api("/seller/media", data);
            const target = $("#editor-form [name=images]");
            target.value = [target.value, out.url].filter(Boolean).join("\n");
          }
        } catch (error) {
          toast(error.message);
        } finally {
          input.disabled = false;
        }
      });
      $("#product-image-file")?.addEventListener("change", async (e) => {
        const file = e.target.files[0];
        if (!file) return;
        const data = new FormData();
        data.set("file", file);
        try {
          const r = await api("/seller/media", data);
          $("#editor-form [name=image]").value = r.url;
          toast("تصویر بارگذاری شد.");
        } catch (e) {
          toast(e.message);
        }
      });
      $("#variant-editors").addEventListener("click", (e) => {
        if (e.target.closest(".remove-variant"))
          e.target.closest(".variant-editor").remove();
      });
      $("#variant-editors").addEventListener("change", async (e) => {
        const input = e.target;
        if (!input.matches(".variant-image-file") || !input.files[0]) return;
        input.disabled = true;
        try {
          const data = new FormData();
          data.set("file", input.files[0]);
          const out = await api("/seller/media", data);
          $(
            "#editor-form [name=variantImage" + input.dataset.index + "]",
          ).value = out.url;
        } catch (error) {
          toast(error.message);
        } finally {
          input.disabled = false;
        }
      });
      let nextVariantIndex = Math.max(1, p.variants.length);
      $("#add-variant").onclick = () =>
        $("#variant-editors").insertAdjacentHTML(
          "beforeend",
          variantEditor({}, nextVariantIndex++),
        );
      $("#add-rule").onclick = () =>
        $("#rule-editors").insertAdjacentHTML(
          "beforeend",
          ruleEditor({}, $$(".rule-editor").length),
        );
    }
    $("#new-product").onclick = () => productEditor();
    $$(".edit-product").forEach(
      (b) =>
        (b.onclick = () =>
          productEditor(boot.products.find((p) => p.id === b.dataset.id))),
    );
    let campaigns = [];
    async function loadCampaigns() {
      try {
        campaigns = await api("/seller/campaigns", undefined, "GET");
        $("#campaign-list").innerHTML =
          campaigns
            .map(
              (d) =>
                `<article class="form-card"><span class="eyebrow">${d.automatic ? "کمپین خودکار" : esc(d.code)}</span><h2>${esc(d.name)}</h2><p class="muted">${d.kind === "percent" ? fa(d.value) + " درصد" : d.kind === "shipping" ? "تخفیف ارسال" : money(d.value)} · ${d.active ? "فعال" : "غیرفعال"}</p><p class="small muted">${d.firstOrder ? "فقط خرید اول · " : ""}${d.stackable ? "قابل ترکیب" : "بهترین تخفیف قابل استفاده"}</p><button class="text-link edit-campaign" data-id="${esc(d.id)}">ویرایش کمپین ↗</button></article>`,
            )
            .join("") ||
          '<div class="empty-state"><h2>کمپین تازه بسازید</h2><p>قوانین تخفیف را متناسب با فروشگاه خودتان تنظیم کنید.</p></div>';
        $$(".edit-campaign").forEach(
          (b) =>
            (b.onclick = () =>
              campaignEditor(campaigns.find((d) => d.id === b.dataset.id))),
        );
      } catch (e) {
        $("#campaign-list").textContent = e.message;
      }
    }
    function campaignEditor(
      d = {
        id: "",
        code: "",
        name: "",
        kind: "percent",
        value: 10,
        active: true,
        automatic: false,
        products: [],
        variants: [],
        phones: [],
      },
    ) {
      modalForm(
        d.id ? "ویرایش کمپین" : "کمپین تازه",
        field("نام کمپین", "name", d.name, "text", "required") +
          field("کد تخفیف", "code", d.code, "text", 'dir="ltr"') +
          selectField(
            "نوع تخفیف",
            "kind",
            [
              ["percent", "درصدی"],
              ["fixed", "مبلغ ثابت (تومان)"],
              ["shipping", "تخفیف هزینه ارسال"],
              ["buy_x_get_y", "خرید X، هدیه Y (ارزان‌ترین اقلام)"],
            ],
            d.kind,
          ) +
          field(
            "مقدار",
            "value",
            d.kind === "percent" ? d.value : (d.value || 0) / 10,
            "number",
            'min="0"',
          ) +
          field(
            "حداقل تعداد اقلام مشمول",
            "minQuantity",
            d.minQuantity || 0,
            "number",
            'min="0"',
          ) +
          field(
            "تعداد خرید X",
            "buyQuantity",
            d.buyQuantity || 0,
            "number",
            'min="0"',
          ) +
          field(
            "تعداد هدیه Y",
            "freeQuantity",
            d.freeQuantity || 0,
            "number",
            'min="0"',
          ) +
          field(
            "سقف تخفیف (تومان، صفر: نامحدود)",
            "cap",
            (d.cap || 0) / 10,
            "number",
            'min="0"',
          ) +
          field(
            "حداقل سبد (تومان)",
            "minSubtotal",
            (d.minSubtotal || 0) / 10,
            "number",
            'min="0"',
          ) +
          field("سقف استفاده کلی", "limit", d.limit || 0, "number", 'min="0"') +
          field(
            "سقف استفاده هر مشتری",
            "perCustomer",
            d.perCustomer || 0,
            "number",
            'min="0"',
          ) +
          field(
            "شروع کمپین",
            "startsAt",
            dateInput(d.startsAt),
            "datetime-local",
          ) +
          field(
            "پایان کمپین",
            "endsAt",
            dateInput(d.endsAt),
            "datetime-local",
          ) +
          check("فعال", "active", d.active) +
          check("اعمال خودکار بدون کد", "automatic", d.automatic) +
          check("فقط اولین سفارش موفق", "firstOrder", d.firstOrder) +
          check("قابل ترکیب با کمپین‌های مشابه", "stackable", d.stackable) +
          textArea(
            "محدود به موبایل‌ها (هر خط یک شماره؛ خالی: همه)",
            "phones",
            (d.phones || []).join("\n"),
          ) +
          `<div class="span-2"><h3>محدود به محصول یا تنوع (خالی: همه)</h3>${boot.products.map((p) => `<div class="editor-row">${check(p.name, "p:" + p.id, d.products?.includes(p.id))}${p.variants.map((v) => check("↳ " + v.name, "v:" + v.id, d.variants?.includes(v.id))).join("")}</div>`).join("")}</div>`,
        async (f) => {
          const kind = f.get("kind");
          await api("/seller/campaigns", {
            ...d,
            name: f.get("name"),
            code: f.get("code"),
            kind,
            minQuantity: Number(f.get("minQuantity")),
            buyQuantity: Number(f.get("buyQuantity")),
            freeQuantity: Number(f.get("freeQuantity")),
            value: Number(f.get("value")) * (kind === "percent" ? 1 : 10),
            cap: Number(f.get("cap")) * 10,
            minSubtotal: Number(f.get("minSubtotal")) * 10,
            limit: Number(f.get("limit")),
            perCustomer: Number(f.get("perCustomer")),
            startsAt: f.get("startsAt")
              ? new Date(f.get("startsAt")).toISOString()
              : new Date().toISOString(),
            endsAt: f.get("endsAt")
              ? new Date(f.get("endsAt")).toISOString()
              : "0001-01-01T00:00:00Z",
            active: f.has("active"),
            automatic: f.has("automatic"),
            firstOrder: f.has("firstOrder"),
            stackable: f.has("stackable"),
            phones: f
              .get("phones")
              .split("\n")
              .map((x) => x.trim())
              .filter(Boolean),
            products: boot.products
              .filter((p) => f.has("p:" + p.id))
              .map((p) => p.id),
            variants: boot.products
              .flatMap((p) => p.variants)
              .filter((v) => f.has("v:" + v.id))
              .map((v) => v.id),
          });
        },
      );
    }
    $("#new-campaign")?.addEventListener("click", () => campaignEditor());
    let articles = [];
    async function articleEditor(id) {
      if (!articles.length) {
        articles = await api("/seller/articles", undefined, "GET");
      }
      const a = articles.find((a) => a.id === id) || {
        id: "",
        title: "",
        slug: "",
        excerpt: "",
        body: "",
        image: "/assets/room.jpg",
        published: false,
      };
      modalForm(
        a.id ? "ویرایش یادداشت" : "یادداشت تازه",
        field("عنوان", "title", a.title, "text", "required") +
          field("آدرس کوتاه", "slug", a.slug, "text", 'required dir="ltr"') +
          field("تصویر", "image", a.image, "text", 'dir="ltr"') +
          check("انتشار عمومی", "published", a.published) +
          textArea("خلاصه", "excerpt", a.excerpt) +
          textArea("متن یادداشت", "body", a.body, "required"),
        async (f) => {
          await api("/seller/articles", {
            ...a,
            title: f.get("title"),
            slug: f.get("slug"),
            image: f.get("image"),
            published: f.has("published"),
            excerpt: f.get("excerpt"),
            body: f.get("body"),
          });
        },
      );
    }
    $("#new-article")?.addEventListener("click", () => void articleEditor());
    $$(".edit-article").forEach(
      (b) => (b.onclick = () => void articleEditor(b.dataset.id)),
    );
    $$(".advance-order").forEach(
      (b) =>
        (b.onclick = async () => {
          if (b.dataset.status === "shipped") {
            modalForm(
              "ثبت تحویل به حمل‌کننده / آمادهٔ دریافت",
              field("حمل‌کننده", "carrier") +
                field("کد رهگیری", "trackingCode") +
                textArea("یادداشت تحویل", "note"),
              async (f) => {
                await api(
                  "/seller/orders/" + b.dataset.order + "/shipment",
                  Object.fromEntries(f),
                );
              },
            );
            return;
          }
          try {
            await busy(b, async () => {
              await api("/seller/orders/" + b.dataset.order, {
                status: b.dataset.status,
              });
              location.reload();
            });
          } catch {}
        }),
    );
    $$(".order-details").forEach(
      (b) =>
        (b.onclick = async () => {
          try {
            const orders = await api("/orders", undefined, "GET");
            const o = orders.find((o) => o.id === b.dataset.order);
            openModal(
              `<h2>سفارش ${esc(o.id.slice(0, 8))}</h2><p>${esc(o.address.name)} · ${esc(o.address.phone)}</p><p>${esc(o.address.province)}، ${esc(o.address.city)}، ${esc(o.address.street)}</p><p>کد پستی: ${esc(o.address.postal)}</p><p>روش تحویل: ${esc(o.quote.delivery?.name || "ارسال استاندارد")} · ${esc(o.shipment?.carrier || "")} ${esc(o.shipment?.trackingCode || "")}</p><p>${esc(o.customerNote || "")}</p>${o.quote.lines.map((l) => `<div class="order-line"><span>${esc(l.name)} × ${fa(l.quantity)}<small>${esc(l.variant)}</small></span><strong>${money(l.price * l.quantity)}</strong></div>${l.attachments.map((id) => `<p><a class="text-link" href="/api/uploads/${encodeURIComponent(id)}" target="_blank" rel="noopener">دریافت فایل پیوست ↗</a></p>`).join("")}`).join("")}<div class="order-total"><strong>جمع نهایی</strong><strong>${money(o.quote.total)}</strong></div>`,
            );
          } catch (e) {
            toast(e.message);
          }
        }),
    );
    const chart = $("#revenue-chart");
    if (chart?.dataset.series) {
      const series = chart.dataset.series
        .split(",")
        .filter(Boolean)
        .map((s) => {
          const [label, value] = s.split(":");
          return { label, value: Number(value) };
        });
      const max = Math.max(...series.map((x) => x.value), 1);
      chart.innerHTML = series
        .map(
          (x) =>
            `<div class="chart-bar" style="height:${Math.max(1, (x.value / max) * 100)}%" title="${esc(x.label)}: ${fa(x.value / 10)} تومان"><span>${esc(x.label)}</span></div>`,
        )
        .join("");
    }
  }
  if (
    boot.settings.modules.support &&
    boot.settings.gapchiUrl &&
    boot.settings.gapchiChannel &&
    !window.storefrontGapchiLoaded
  ) {
    window.storefrontGapchiLoaded = true;
    const script = document.createElement("script");
    script.src = boot.settings.gapchiUrl.replace(/\/$/, "") + "/widget.js";
    script.dataset.channel = boot.settings.gapchiChannel;
    script.async = true;
    script.onload = () => {
      if (session.user?.id)
        void api("/support/identity", undefined, "GET")
          .then((identity) => window.SaadatChat?.identify(identity))
          .catch(() => {});
    };
    document.body.append(script);
  }

  if (boot.view === "wishlist") {
    const cards = $$(".product-card");
    cards.forEach((c) => {
      const id = $(".wish", c)?.dataset.id;
      c.hidden = !wishes.includes(id);
    });
    if (!cards.some((c) => !c.hidden))
      $(".product-grid").innerHTML =
        '<div class="empty-state"><span>♡</span><h2>انتخاب‌های دوست‌داشتنی‌تان را ذخیره کنید</h2><p>با زدن قلب کنار هر محصول، آن را برای بعد نگه دارید.</p><a class="btn" href="/shop">دیدن محصولات ↗</a></div>';
  }

  $$(".cancel-order").forEach((button) =>
    button.addEventListener("click", async () => {
      try {
        await busy(button, async () => {
          await api("/orders/" + button.dataset.order + "/cancel", {});
          try {
            sessionStorage.removeItem("hera-checkout-key");
          } catch {}
          location.reload();
        });
      } catch {}
    }),
  );

  $$("time[datetime]").forEach((el) => {
    const date = new Date(el.dateTime);
    if (!Number.isNaN(date.getTime()))
      el.textContent = new Intl.DateTimeFormat("fa-IR", {
        year: "numeric",
        month: "long",
        day: "numeric",
      }).format(date);
  });
  $("#analytics-period")?.addEventListener("change", (e) => {
    const url = new URL(location.href);
    url.searchParams.set("days", e.target.value);
    location.assign(url.href);
  });
  {
    const bars = $$(".funnel-row i[data-count]");
    const base = Number(bars[0]?.dataset.count || 0);
    bars.forEach((bar) =>
      bar.style.setProperty(
        "--bar",
        (base ? Math.min(100, (Number(bar.dataset.count) / base) * 100) : 0) +
          "%",
      ),
    );
  }

  // Installation is progressive: browsers without an install prompt retain the web flow.
  let installPrompt = window.storefrontInstallPrompt;
  window.addEventListener(
    "beforeinstallprompt",
    (event) => {
      event.preventDefault();
      installPrompt = event;
      window.storefrontInstallPrompt = event;
      $$(".install-app").forEach((button) => {
        button.hidden = false;
      });
    },
    { signal: lifetime.signal },
  );
  $$(".install-app").forEach((button) =>
    button.addEventListener("click", async () => {
      if (!installPrompt) return;
      const prompt = installPrompt;
      installPrompt = null;
      window.storefrontInstallPrompt = null;
      $$(".install-app").forEach((node) => {
        node.hidden = true;
      });
      await prompt.prompt();
      await prompt.userChoice;
    }),
  );
  window.addEventListener(
    "appinstalled",
    () => {
      installPrompt = null;
      window.storefrontInstallPrompt = null;
      $$(".install-app, .install-help").forEach((node) => {
        node.hidden = true;
      });
    },
    { signal: lifetime.signal },
  );
  if (
    /iPad|iPhone|iPod/.test(navigator.userAgent) &&
    !navigator.standalone &&
    !window.matchMedia("(display-mode: standalone)").matches
  ) {
    $$(".install-help").forEach((node) => {
      node.hidden = false;
    });
  }
  if ("serviceWorker" in navigator && window.isSecureContext) {
    window.addEventListener(
      "load",
      () => {
        void navigator.serviceWorker
          .register("/sw.js", { scope: "/", updateViaCache: "none" })
          .catch(() => {});
      },
      { signal: lifetime.signal },
    );
  }

  function bindQuickAdd(root = document) {
    $$("[data-quick-add]", root).forEach((button) => {
      if (button.dataset.boundCart) return;
      button.dataset.boundCart = "1";
      button.addEventListener("click", () => {
        const p = boot.products.find((p) => p.id === button.dataset.quickAdd);
        const productId = p?.id || button.dataset.quickAdd;
        const v = p?.variants?.[0] || {
          id: button.dataset.variant,
          stock: Number(button.dataset.stock),
        };
        if (!v.id) return;
        const existing = cart.find((line) => line.variantId === v.id);
        if ((existing?.quantity || 0) >= Math.min(100, v.stock)) {
          toast("تعداد انتخابی به موجودی کالا رسیده است.");
          return;
        }
        if (existing) existing.quantity++;
        else
          cart.push({
            productId,
            variantId: v.id,
            quantity: 1,
            attachments: [],
          });
        saveCart();
        event("add_to_cart", productId);
        toast("به سبد خرید اضافه شد.", true);
      });
    });
  }
  window.hydrateStorefrontCards = (root) => {
    bindWishButtons(root);
    bindQuickAdd(root);
  };
  bindQuickAdd();
  $("#open-filters")?.addEventListener("click", () =>
    $("#filter-sheet").showModal(),
  );
  $("#close-filters")?.addEventListener("click", () =>
    $("#filter-sheet").close(),
  );
  $$("[data-auto-submit]").forEach((input) =>
    input.addEventListener("change", () => input.form.requestSubmit()),
  );
  $$("[data-back]").forEach((button) =>
    button.addEventListener("click", () => {
      if (history.length > 1) history.back();
      else window.storefrontNavigation?.visit("/shop");
    }),
  );
  lifetime.signal.addEventListener("abort", () => clearTimeout(toastTimer), {
    once: true,
  });
};
window.initStorefront();
