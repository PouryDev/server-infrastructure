"use strict";
window.initMerchandising = function initMerchandising() {
  const boot = JSON.parse(document.getElementById("boot").textContent);
  const signal = window.storefrontSignal;
  const $ = (s, r = document) => r.querySelector(s),
    $$ = (s, r = document) => [...r.querySelectorAll(s)];
  const esc = (value) =>
    String(value ?? "").replace(
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
  const fa = (value) => new Intl.NumberFormat("fa-IR").format(value);
  const queue = [];
  let running = 0;
  async function fill(row) {
    const root = $("[data-lazy-products]", row);
    if (!root || root.dataset.loaded) return;
    root.dataset.loaded = "1";
    root.setAttribute("aria-busy", "true");
    try {
      const params = new URLSearchParams({
        kind: row.dataset.rail,
        key: row.dataset.railKey,
      });
      const response = await fetch("/fragments/rail?" + params, {
        signal,
        credentials: "same-origin",
      });
      if (!response.ok) throw new Error("دریافت کالاها کامل نشد.");
      const html = await response.text();
      if (signal?.aborted) return;
      if (!html.trim()) {
        row.hidden = true;
        return;
      }
      root.innerHTML = html;
      window.hydrateStorefrontCards?.(root);
    } catch (error) {
      if (signal?.aborted) return;
      root.textContent =
        "کالاهای این بخش دریافت نشدند. می‌توانید همهٔ کالاها را از لینک بالا ببینید.";
      const retry = document.createElement("button");
      retry.type = "button";
      retry.className = "text-link rail-retry";
      retry.textContent = "دوباره تلاش کن ↻";
      retry.onclick = () => {
        delete root.dataset.loaded;
        queue.push(row);
        pump();
      };
      root.append(retry);
    } finally {
      root.setAttribute("aria-busy", "false");
    }
  }
  function pump() {
    while (running < 2 && queue.length && !signal?.aborted) {
      const row = queue.shift();
      running++;
      void fill(row).finally(() => {
        running--;
        pump();
      });
    }
  }
  const lazy = $$(".merch-rail").filter((row) =>
    $("[data-lazy-products]", row),
  );
  if ("IntersectionObserver" in window) {
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            observer.unobserve(entry.target);
            queue.push(entry.target);
          }
        }
        pump();
      },
      { rootMargin: "240px 0px" },
    );
    lazy.forEach((row) => observer.observe(row));
    signal?.addEventListener("abort", () => observer.disconnect(), {
      once: true,
    });
  } else {
    queue.push(...lazy);
    pump();
  }
  $$("[data-copy-coupon]").forEach((button) =>
    button.addEventListener("click", async () => {
      try {
        await navigator.clipboard.writeText(button.dataset.copyCoupon);
        $("small", button).textContent = "کپی شد ✓";
      } catch {
        $("small", button).textContent = "کد را انتخاب و کپی کنید";
      }
    }),
  );
  if (!["seller", "admin"].includes(boot.view)) return;
  const settings = boot.settings;
  settings.collections ??= [];
  async function request(path, data, method = "POST") {
    const response = await fetch("/api" + path, {
      method,
      credentials: "same-origin",
      signal,
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": boot.csrf,
      },
      body: JSON.stringify(data),
    });
    const result = await response.json();
    if (!response.ok)
      throw new Error(
        result.error ||
          "ذخیره انجام نشد؛ صفحه را تازه کنید و دوباره تلاش کنید.",
      );
    return result;
  }
  function renderList() {
    $("#collection-list").innerHTML =
      settings.collections
        .map(
          (c) =>
            `<article class="form-card collection-summary"><div><span class="status-pill">${c.enabled ? "فعال" : "پنهان"}</span><span class="small muted">${c.mode === "manual" ? "انتخاب دستی" : "انتخاب خودکار"}</span></div><h2>${esc(c.name)}</h2><p>${esc(c.description)}</p><small>حداکثر ${fa(c.limit)} کالا · ${c.lazy ? "دریافت هنگام مشاهده" : "رندر کامل در صفحه"}</small><div><button type="button" class="text-link" data-edit-collection="${esc(c.id)}">ویرایش و جایگاه ←</button><a href="/collections/${encodeURIComponent(c.id)}" target="_blank" rel="noopener">مشاهده ↗</a><button type="button" class="text-link" data-delete-collection="${esc(c.id)}">حذف</button></div></article>`,
        )
        .join("") ||
      '<div class="empty-state"><h2>یک ویترین هدفمند بسازید</h2><p>محصولات ویژه، انتخاب‌های اقتصادی یا یک مجموعهٔ مناسبتی؛ هرکدام می‌توانند یک ردیف مستقل باشند.</p></div>';
    $$("[data-edit-collection]").forEach(
      (b) => (b.onclick = () => editor(b.dataset.editCollection)),
    );
    $$("[data-delete-collection]").forEach(
      (b) =>
        (b.onclick = async () => {
          if (!confirm("کالکشن از ویترین حذف شود؟ خود کالاها حذف نمی‌شوند."))
            return;
          b.disabled = true;
          try {
            await request(
              "/seller/collections/" +
                encodeURIComponent(b.dataset.deleteCollection),
              { revision: settings.revision },
              "DELETE",
            );
            location.reload();
          } catch (error) {
            alert(error.message);
            b.disabled = false;
          }
        }),
    );
  }
  function editor(id = "") {
    const c = settings.collections.find((c) => c.id === id) || {
      id: "",
      name: "",
      description: "",
      mode: "manual",
      productIds: [],
      category: "",
      inStock: true,
      withBadge: false,
      minPrice: 0,
      maxPrice: 0,
      sort: "catalog",
      limit: 8,
      enabled: true,
      lazy: false,
    };
    let selected = [...(c.productIds || [])];
    const sections = settings.sections.filter(
      (s) => s.kind !== "collection" || s.collectionId !== id,
    );
    const originalPosition = settings.sections.findIndex(
      (s) => s.kind === "collection" && s.collectionId === id,
    );
    const position = originalPosition < 0 ? 0 : originalPosition;
    const categories = [...new Set(boot.products.map((p) => p.category))];
    const modal = $("#modal");
    $("#modal-body").innerHTML =
      `<form id="collection-form" class="stack-form"><span class="eyebrow">چیدمان هوشمند فروش</span><h2>${id ? "ویرایش کالکشن" : "کالکشن تازه"}</h2><div class="form-grid"><label>عنوان کالکشن<input name="name" required maxlength="120" value="${esc(c.name)}" placeholder="مثلاً انتخاب‌های ویژهٔ این هفته"></label><label>روش انتخاب<select name="mode"><option value="manual" ${c.mode === "manual" ? "selected" : ""}>دستی؛ با ترتیب دلخواه</option><option value="automatic" ${c.mode === "automatic" ? "selected" : ""}>خودکار؛ بر اساس قواعد</option></select></label><label class="span-2">توضیح کوتاه<textarea name="description" maxlength="700">${esc(c.description)}</textarea></label></div><section id="manual-collection-fields"><label>پیداکردن کالا<input type="search" id="collection-product-search" placeholder="نام یا دستهٔ کالا"></label><div id="collection-product-picker"></div><h3>ترتیب انتخاب‌های شما</h3><div id="collection-selected-order"></div></section><div id="automatic-collection-fields" class="form-grid"><label>دسته<select name="category"><option value="">همهٔ دسته‌ها</option>${categories.map((category) => `<option ${c.category === category ? "selected" : ""}>${esc(category)}</option>`).join("")}</select></label><label>مرتب‌سازی<select name="sort">${[
        ["catalog", "ترتیب فعلی ویترین"],
        ["price-asc", "ارزان‌ترین"],
        ["price-desc", "گران‌ترین"],
        ["name", "نام کالا"],
      ]
        .map(
          ([key, label]) =>
            `<option value="${key}" ${c.sort === key ? "selected" : ""}>${label}</option>`,
        )
        .join(
          "",
        )}</select></label><label>از قیمت (تومان)<input type="number" name="minPrice" min="0" value="${c.minPrice / 10 || ""}"></label><label>تا قیمت (تومان)<input type="number" name="maxPrice" min="0" value="${c.maxPrice / 10 || ""}"></label><label class="stock-filter"><input type="checkbox" name="inStock" ${c.inStock ? "checked" : ""}>فقط موجودها</label><label class="stock-filter"><input type="checkbox" name="withBadge" ${c.withBadge ? "checked" : ""}>فقط کالاهای نشان‌دار</label><p class="small muted span-2">قواعد با تغییر کالاها و موجودی، خودکار اعمال می‌شوند.</p></div><div class="form-grid"><label>جایگاه در صفحهٔ اصلی<select name="position">${Array.from({ length: sections.length + 1 }, (_, index) => `<option value="${index}" ${index === position ? "selected" : ""}>${index === 0 ? "ابتدای بخش‌های صفحه" : `بعد از: ${esc(sections[index - 1].title)}`}</option>`).join("")}</select></label><label>تعداد کالا در ردیف<input type="number" name="limit" min="1" max="24" value="${c.limit}" required></label><label class="stock-filter"><input type="checkbox" name="enabled" ${c.enabled ? "checked" : ""}>نمایش فعال باشد</label><label class="stock-filter"><input type="checkbox" name="lazy" ${c.lazy ? "checked" : ""}>دریافت هنگام رسیدن به بخش</label></div><div id="collection-preview" class="info-box"></div><p id="collection-error" class="form-error" role="alert"></p><button class="btn full" type="submit">ذخیره و انتشار کالکشن ←</button></form>`;
    const form = $("#collection-form");
    function data() {
      return {
        ...c,
        name: form.elements.name.value.trim(),
        description: form.elements.description.value,
        mode: form.elements.mode.value,
        productIds: selected,
        category: form.elements.category.value,
        sort: form.elements.sort.value,
        minPrice: Number(form.elements.minPrice.value) * 10,
        maxPrice: Number(form.elements.maxPrice.value) * 10,
        inStock: form.elements.inStock.checked,
        withBadge: form.elements.withBadge.checked,
        limit: Number(form.elements.limit.value),
        enabled: form.elements.enabled.checked,
        lazy: form.elements.lazy.checked,
      };
    }
    function preview() {
      const c = data();
      $("#manual-collection-fields").hidden = c.mode !== "manual";
      $("#automatic-collection-fields").hidden = c.mode !== "automatic";
      const matches = boot.products.filter((p) => {
        const available = p.variants.some((v) => v.stock > 0);
        const eligible = p.variants.filter((v) => !available || v.stock > 0);
        const price = Math.min(...eligible.map((v) => v.price));
        return (
          p.active &&
          (c.mode === "manual"
            ? selected.includes(p.id)
            : (!c.category || p.category === c.category) &&
              (!c.inStock || available) &&
              (!c.withBadge || p.badge) &&
              price >= c.minPrice &&
              (!c.maxPrice || price <= c.maxPrice))
        );
      });
      $("#collection-preview").textContent =
        `اکنون ${fa(matches.length)} کالا انتخاب می‌شوند؛ حداکثر ${fa(c.limit)} کالا در ردیف صفحهٔ اصلی دیده می‌شود.`;
    }
    function selectedOrder() {
      $("#collection-selected-order").innerHTML =
        selected
          .map((id, index) => {
            const p = boot.products.find((p) => p.id === id);
            return `<div><span>${esc(p?.name || "کالای حذف‌شده")}</span><button type="button" data-pick-move="${index}" data-direction="-1" aria-label="بالا بردن ${esc(p?.name)}">↑</button><button type="button" data-pick-move="${index}" data-direction="1" aria-label="پایین بردن ${esc(p?.name)}">↓</button></div>`;
          })
          .join("") ||
        '<p class="muted small">از فهرست بالا کالا انتخاب کنید.</p>';
      $$("[data-pick-move]").forEach(
        (b) =>
          (b.onclick = () => {
            const i = Number(b.dataset.pickMove),
              j = i + Number(b.dataset.direction);
            if (j < 0 || j >= selected.length) return;
            [selected[i], selected[j]] = [selected[j], selected[i]];
            selectedOrder();
          }),
      );
    }
    function picker() {
      const query = $("#collection-product-search").value.trim();
      $("#collection-product-picker").innerHTML = boot.products
        .filter((p) => !query || (p.name + p.category).includes(query))
        .map(
          (p) =>
            `<label><input type="checkbox" value="${esc(p.id)}" ${selected.includes(p.id) ? "checked" : ""}><img src="${esc(p.image)}" alt="" loading="lazy"><span>${esc(p.name)}<small>${esc(p.category)}</small></span></label>`,
        )
        .join("");
      $$("input", $("#collection-product-picker")).forEach(
        (input) =>
          (input.onchange = () => {
            selected = input.checked
              ? [...selected, input.value]
              : selected.filter((id) => id !== input.value);
            selectedOrder();
            preview();
          }),
      );
    }
    $("#collection-product-search").addEventListener("input", picker);
    form.addEventListener("input", preview);
    form.addEventListener("change", preview);
    picker();
    selectedOrder();
    preview();
    modal.showModal();
    form.onsubmit = async (event) => {
      event.preventDefault();
      const button = $("button[type=submit]", form);
      button.disabled = true;
      try {
        const c = data();
        if (c.mode === "manual" && c.enabled && !selected.length)
          throw new Error("حداقل یک کالا برای کالکشن دستی انتخاب کنید.");
        await request("/seller/collections", {
          collection: c,
          revision: settings.revision,
          position: Number(form.elements.position.value),
        });
        location.reload();
      } catch (error) {
        if (!signal?.aborted)
          $("#collection-error").textContent = error.message;
        button.disabled = false;
      }
    };
  }
  $("#new-collection")?.addEventListener("click", () => editor());
  renderList();
};
window.initMerchandising();
