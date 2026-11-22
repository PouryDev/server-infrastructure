"use strict";
window.initBusiness = function initBusiness() {
  const $ = (q, r = document) => r.querySelector(q),
    $$ = (q, r = document) => [...r.querySelectorAll(q)];
  const boot = JSON.parse($("#boot").textContent),
    signal = window.storefrontSignal;
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
  $$("[data-gallery]").forEach((button) =>
    button.addEventListener(
      "click",
      () => {
        $("#product-main-image").src = button.dataset.gallery;
        $$("[data-gallery]").forEach((b) =>
          b.setAttribute("aria-pressed", String(b === button)),
        );
      },
      { signal },
    ),
  );
  for (const [selector, path, payload, success] of [
    [
      "#review-form",
      "/reviews",
      (f, form) => ({
        productId: form.dataset.product,
        rating: Number(f.get("rating")),
        title: f.get("title"),
        body: f.get("body"),
      }),
      "نظر شما برای بررسی ثبت شد.",
    ],
    [
      "#return-form",
      "/customer/returns",
      (f, form) => ({ orderId: form.dataset.order, reason: f.get("reason") }),
      "درخواست بازگشت ثبت شد.",
    ],
  ]) {
    const form = $(selector);
    form?.addEventListener(
      "submit",
      async (e) => {
        e.preventDefault();
        const button = $("button[type=submit]", form);
        button.disabled = true;
        try {
          await api(path, payload(new FormData(form), form));
          $(".form-feedback", form).textContent = success;
          if (selector === "#return-form") location.reload();
          else form.reset();
        } catch (error) {
          $(".form-feedback", form).textContent = error.message;
        } finally {
          button.disabled = false;
        }
      },
      { signal },
    );
  }
  if (!$("#delivery-list")) return;
  const settings = structuredClone(boot.settings);
  settings.pages ??= [];
  settings.navigation ??= [];
  settings.deliveryMethods ??= [];
  settings.torob ??= { enabled: false };
  const money = (n) => new Intl.NumberFormat("fa-IR").format(n / 10) + " تومان";
  const field = (label, name, value = "", type = "text", extra = "") =>
    `<label>${label}<input name="${name}" type="${type}" value="${esc(value)}" ${extra}></label>`;
  const area = (label, name, value = "") =>
    `<label class="span-2">${label}<textarea name="${name}">${esc(value)}</textarea></label>`;
  const check = (label, name, value) =>
    `<label class="stock-filter"><input type="checkbox" name="${name}" ${value ? "checked" : ""}>${label}</label>`;
  const message = (form, text) => {
    const el = $(".form-feedback", form);
    if (el) el.textContent = text;
  };
  async function api(path, data, method = "POST") {
    const response = await fetch("/api" + path, {
      method,
      signal,
      credentials: "same-origin",
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": boot.csrf,
      },
      ...(data !== undefined ? { body: JSON.stringify(data) } : {}),
    });
    const out = await response.json();
    if (!response.ok) throw Error(out.error || "انجام درخواست ممکن نشد.");
    return out;
  }
  async function save(next) {
    await api("/seller/settings", next);
    location.reload();
  }
  function editor(title, html, submit) {
    const dialog = $("#modal");
    $("#modal-body").innerHTML =
      `<h2>${esc(title)}</h2><form id="business-editor" class="stack-form"><div class="form-grid">${html}</div><p class="form-feedback" role="alert"></p><button class="btn" type="submit">ذخیرهٔ تغییرات</button></form>`;
    dialog.showModal();
    const form = $("#business-editor");
    form.addEventListener(
      "submit",
      async (e) => {
        e.preventDefault();
        const button = $("button[type=submit]", form);
        button.disabled = true;
        try {
          await submit(new FormData(form), form);
        } catch (error) {
          message(form, error.message);
        } finally {
          button.disabled = false;
        }
      },
      { signal },
    );
  }
  const list = (v) =>
    String(v || "")
      .split(/[,،\n]/)
      .map((v) => v.trim())
      .filter(Boolean);
  function deliveryEditor(
    method = {
      id: crypto.randomUUID(),
      name: "",
      kind: "shipping",
      carrier: "",
      fee: 0,
      freeAbove: 0,
      minSubtotal: 0,
      maxWeightGrams: 0,
      minDays: 2,
      maxDays: 5,
      enabled: true,
    },
  ) {
    editor(
      "روش تحویل",
      field(
        "نام نمایشی",
        "name",
        method.name,
        "text",
        "required maxlength=100",
      ) +
        `<label>نوع تحویل<select name="kind">${[
          ["shipping", "ارسال با شرکت حمل"],
          ["courier", "پیک محلی"],
          ["pickup", "تحویل حضوری"],
        ]
          .map(
            ([v, t]) =>
              `<option value="${v}" ${method.kind === v ? "selected" : ""}>${t}</option>`,
          )
          .join("")}</select></label>` +
        field("حمل‌کننده (پست، تیپاکس، پیک…)", "carrier", method.carrier) +
        field(
          "هزینه (تومان)",
          "fee",
          method.fee / 10,
          "number",
          "required min=0",
        ) +
        field(
          "ارسال رایگان از مبلغ (تومان؛ صفر = غیرفعال)",
          "freeAbove",
          method.freeAbove / 10,
          "number",
          "min=0",
        ) +
        field(
          "حداقل خرید (تومان)",
          "minSubtotal",
          method.minSubtotal / 10,
          "number",
          "min=0",
        ) +
        field(
          "حداکثر وزن (گرم؛ صفر = بدون محدودیت)",
          "maxWeightGrams",
          method.maxWeightGrams,
          "number",
          "min=0",
        ) +
        field(
          "زودترین تحویل (روز تقویمی)",
          "minDays",
          method.minDays,
          "number",
          "min=0 max=90 required",
        ) +
        field(
          "دیرترین تحویل (روز تقویمی)",
          "maxDays",
          method.maxDays,
          "number",
          "min=0 max=90 required",
        ) +
        area(
          "استان‌ها؛ جداشده با ویرگول، خالی = همه",
          "provinces",
          (method.provinces || []).join("، "),
        ) +
        area(
          "شهرها؛ جداشده با ویرگول، خالی = همه",
          "cities",
          (method.cities || []).join("، "),
        ) +
        area(
          "نشانی و ساعت تحویل حضوری",
          "pickupAddress",
          method.pickupAddress,
        ) +
        check("فعال و قابل انتخاب", "enabled", method.enabled),
      async (f) => {
        const next = {
          ...method,
          name: f.get("name"),
          kind: f.get("kind"),
          carrier: f.get("carrier"),
          fee: Number(f.get("fee")) * 10,
          freeAbove: Number(f.get("freeAbove")) * 10,
          minSubtotal: Number(f.get("minSubtotal")) * 10,
          maxWeightGrams: Number(f.get("maxWeightGrams")),
          minDays: Number(f.get("minDays")),
          maxDays: Number(f.get("maxDays")),
          provinces: list(f.get("provinces")),
          cities: list(f.get("cities")),
          pickupAddress: f.get("pickupAddress"),
          enabled: f.has("enabled"),
        };
        const methods = settings.deliveryMethods.filter(
          (m) => m.id !== next.id,
        );
        methods.push(next);
        await save({ ...settings, deliveryMethods: methods });
      },
    );
  }
  $("#delivery-list").innerHTML = settings.deliveryMethods.length
    ? settings.deliveryMethods
        .map(
          (m) =>
            `<article class="form-card"><span class="eyebrow">${m.enabled ? "فعال" : "غیرفعال"} · ${m.kind === "pickup" ? "تحویل حضوری" : m.kind === "courier" ? "پیک محلی" : "ارسال"}</span><h2>${esc(m.name)}</h2><p>${money(m.fee)} · ${esc(m.minDays)} تا ${esc(m.maxDays)} روز تقویمی</p><p>${esc([...(m.provinces || []), ...(m.cities || [])].join("، ") || "همهٔ شهرها")}</p><button class="btn btn-outline" data-edit-delivery="${esc(m.id)}">ویرایش تعرفه و شرایط</button></article>`,
        )
        .join("")
    : "<article class=form-card><h2>ارسال استاندارد فعلی</h2><p>تا زمان ثبت روش جدید، تعرفهٔ عمومی تنظیم‌شده در هویت و چیدمان استفاده می‌شود.</p></article>";
  $("#new-delivery").onclick = () => deliveryEditor();
  $$("[data-edit-delivery]").forEach(
    (b) =>
      (b.onclick = () =>
        deliveryEditor(
          settings.deliveryMethods.find((m) => m.id === b.dataset.editDelivery),
        )),
  );
  function pageEditor(
    page = { slug: "", title: "", description: "", body: "", published: false },
  ) {
    editor(
      "صفحهٔ فروشگاه",
      field("عنوان", "title", page.title, "text", "required maxlength=200") +
        field(
          "نشانی صفحه؛ مثل faq",
          "slug",
          page.slug,
          "text",
          'required pattern="[a-z0-9][a-z0-9-]{0,99}" dir="ltr"',
        ) +
        area("توضیح کوتاه برای جست‌وجو", "description", page.description) +
        area("متن صفحه", "body", page.body) +
        check("صفحه منتشر شود", "published", page.published),
      async (f) => {
        const next = {
          slug: f.get("slug"),
          title: f.get("title"),
          description: f.get("description"),
          body: f.get("body"),
          published: f.has("published"),
        };
        if (
          settings.pages.some(
            (p) => p.slug === next.slug && p.slug !== page.slug,
          )
        )
          throw Error("این نشانی قبلاً استفاده شده است.");
        await save({
          ...settings,
          pages: [...settings.pages.filter((p) => p.slug !== page.slug), next],
        });
      },
    );
  }
  $("#new-page").onclick = () => pageEditor();
  $("#content-page-list").innerHTML = settings.pages
    .map(
      (p, i) =>
        `<article class="form-card"><span class="eyebrow">${p.published ? "منتشرشده" : "پیش‌نویس"}</span><h2>${esc(p.title)}</h2><p>${esc(p.description)}</p><div class="inline-actions"><button class="btn btn-outline" data-edit-page="${i}">ویرایش</button>${p.published ? `<a href="/pages/${esc(p.slug)}" target="_blank" rel="noopener">مشاهدهٔ صفحه</a>` : ""}</div></article>`,
    )
    .join("");
  $$("[data-edit-page]").forEach(
    (b) =>
      (b.onclick = () =>
        pageEditor(settings.pages[Number(b.dataset.editPage)])),
  );
  function navigationRow(link = { label: "", url: "" }) {
    const row = document.createElement("div");
    row.className = "navigation-editor-row";
    row.innerHTML =
      field(
        "عنوان پیوند",
        "label",
        link.label,
        "text",
        "required maxlength=100",
      ) +
      field(
        "نشانی",
        "url",
        link.url,
        "text",
        'required dir="ltr" placeholder="/pages/faq"',
      ) +
      '<button type="button" class="text-link" aria-label="حذف پیوند">حذف</button>';
    $("button", row).onclick = () => row.remove();
    $("#navigation-links").append(row);
  }
  settings.navigation.forEach(navigationRow);
  $("#add-navigation").onclick = () => {
    if ($$(".navigation-editor-row").length < 12) navigationRow();
  };
  $("#navigation-form").onsubmit = async (e) => {
    e.preventDefault();
    const form = e.target,
      button = $("button[type=submit]", form);
    button.disabled = true;
    try {
      await save({
        ...settings,
        navigation: $$(".navigation-editor-row").map((row) => ({
          label: $("[name=label]", row).value,
          url: $("[name=url]", row).value,
        })),
      });
    } catch (error) {
      message(form, error.message);
    } finally {
      button.disabled = false;
    }
  };
  async function loadCare() {
    const feedback = $("#care-feedback");
    feedback.textContent = "در حال دریافت…";
    try {
      const [returns, reviews] = await Promise.all([
        api("/seller/returns", undefined, "GET"),
        api("/seller/reviews", undefined, "GET"),
      ]);
      if (signal?.aborted) return;
      const states = {
        requested: "درخواست تازه",
        approved: "تأییدشده",
        rejected: "ردشده",
        received: "دریافت‌شده",
        closed: "بسته‌شده",
        pending: "در انتظار بررسی",
        published: "منتشرشده",
        hidden: "پنهان",
      };
      $("#return-list").innerHTML = returns.length
        ? returns
            .map(
              (r) =>
                `<article class="form-card"><span class="eyebrow">${states[r.status]}</span><h3>سفارش <bdi>${esc(r.orderId.slice(0, 8))}</bdi></h3><p>${esc(r.reason)}</p><p>${esc(r.sellerNote)}</p>${["requested", "approved", "received"].includes(r.status) ? `<button class="btn btn-outline" data-decide-return="${esc(r.orderId)}">رسیدگی به درخواست</button>` : ""}</article>`,
            )
            .join("")
        : "<p class=muted>درخواست بازگشتی ثبت نشده است.</p>";
      $("#review-list").innerHTML = reviews.length
        ? reviews
            .map(
              (r) =>
                `<article class="form-card"><span class="eyebrow">${states[r.status]} · ${r.rating} / ۵ ★</span><h3>${esc(r.title)}</h3><p>${esc(r.body)}</p><small>${esc(r.author)} · ${esc(r.productId)}</small><div class="inline-actions"><button class="btn btn-outline" data-review="${esc(r.id)}" data-state="published">انتشار</button><button class="text-link" data-review="${esc(r.id)}" data-state="hidden">پنهان‌کردن</button></div></article>`,
            )
            .join("")
        : "<p class=muted>نظری از خریداران ثبت نشده است.</p>";
      $$("[data-review]").forEach(
        (b) =>
          (b.onclick = async () => {
            b.disabled = true;
            try {
              await api("/seller/reviews/" + b.dataset.review, {
                status: b.dataset.state,
              });
              await loadCare();
            } catch (error) {
              feedback.textContent = error.message;
              b.disabled = false;
            }
          }),
      );
      $$("[data-decide-return]").forEach(
        (b) =>
          (b.onclick = () => {
            const r = returns.find((r) => r.orderId === b.dataset.decideReturn);
            const options =
              r.status === "requested"
                ? [
                    ["approved", "تأیید بازگشت"],
                    ["rejected", "رد درخواست"],
                  ]
                : r.status === "approved"
                  ? [["received", "ثبت دریافت کالا"]]
                  : [["closed", "بستن رسیدگی"]];
            editor(
              "رسیدگی به بازگشت کالا",
              `<label>مرحلهٔ بعد<select name="status">${options.map(([v, t]) => `<option value="${v}">${t}</option>`).join("")}</select></label>` +
                area("پاسخ به مشتری / نتیجهٔ رسیدگی", "note", r.sellerNote),
              async (f) => {
                await api("/seller/returns/" + r.orderId, {
                  status: f.get("status"),
                  note: f.get("note"),
                });
                $("#modal").close();
                await loadCare();
              },
            );
          }),
      );
      feedback.textContent = "";
    } catch (error) {
      if (!signal?.aborted) feedback.textContent = error.message;
    }
  }
  $("#load-care").onclick = () => void loadCare();
  document
    .querySelector('[data-tab="care"]')
    .addEventListener("click", () => void loadCare(), { signal });
  const cmsKinds = ["banner", "richtext", "benefits", "faq"];
  const sectionNames = {
    banner: "بنر تصویری",
    richtext: "متن و معرفی",
    benefits: "مزایا و خدمات",
    faq: "پرسش‌های رایج",
  };
  function sectionEditor(section, index = -1) {
    editor(
      sectionNames[section.kind],
      field(
        "عنوان بخش",
        "title",
        section.title,
        "text",
        "required maxlength=200",
      ) +
        area("توضیح", "body", section.body) +
        field(
          "تصویر",
          "image",
          section.image,
          "text",
          'dir="ltr" placeholder="/media/..."',
        ) +
        '<label>بارگذاری تصویر<input id="cms-image-file" type="file" accept="image/jpeg,image/png,image/webp"></label>' +
        field(
          "نشانی دکمه",
          "href",
          section.href,
          "text",
          'dir="ltr" placeholder="/shop"',
        ) +
        field("متن دکمه", "button", section.button) +
        check("نمایش بخش", "enabled", section.enabled) +
        `<div class="span-2" id="cms-item-fields"></div>${["faq", "benefits"].includes(section.kind) ? '<button type="button" class="btn btn-outline" id="add-cms-item">افزودن مورد</button>' : ""}`,
      async (f, form) => {
        const next = {
          ...section,
          title: f.get("title"),
          body: f.get("body"),
          image: f.get("image"),
          href: f.get("href"),
          button: f.get("button"),
          enabled: f.has("enabled"),
          items: $$(".cms-item-editor", form).map((row) => ({
            title: $("[name=itemTitle]", row).value,
            text: $("[name=itemText]", row).value,
          })),
        };
        const sections = [...settings.sections];
        if (index < 0) sections.push(next);
        else sections[index] = next;
        await save({ ...settings, sections });
      },
    );
    function itemRow(item = { title: "", text: "" }) {
      const row = document.createElement("div");
      row.className = "cms-item-editor";
      row.innerHTML =
        field(
          section.kind === "faq" ? "پرسش" : "عنوان",
          "itemTitle",
          item.title,
          "text",
          "required",
        ) +
        area(section.kind === "faq" ? "پاسخ" : "توضیح", "itemText", item.text) +
        '<button type="button" class="text-link">حذف مورد</button>';
      $("button", row).onclick = () => row.remove();
      $("#cms-item-fields").append(row);
    }
    (section.items || []).forEach(itemRow);
    $("#add-cms-item")?.addEventListener(
      "click",
      () => {
        if ($$(".cms-item-editor").length < 12) itemRow();
      },
      { signal },
    );
    $("#cms-image-file").onchange = async (e) => {
      const file = e.target.files[0];
      if (!file) return;
      try {
        const data = new FormData();
        data.set("file", file);
        const response = await fetch("/api/seller/media", {
          method: "POST",
          headers: { "X-CSRF-Token": boot.csrf },
          body: data,
          signal,
        });
        const out = await response.json();
        if (!response.ok) throw Error(out.error);
        $("#business-editor [name=image]").value = out.url;
      } catch (error) {
        message($("#business-editor"), error.message);
      }
    };
  }
  $("#cms-section-list").innerHTML = settings.sections
    .map((s, i) =>
      cmsKinds.includes(s.kind)
        ? `<div class="section-row"><strong>${esc(s.title)}</strong><span>${sectionNames[s.kind]}</span><button type="button" data-edit-section="${i}">ویرایش</button></div>`
        : "",
    )
    .join("");
  $$("[data-edit-section]").forEach(
    (b) =>
      (b.onclick = () =>
        sectionEditor(
          settings.sections[Number(b.dataset.editSection)],
          Number(b.dataset.editSection),
        )),
  );
  $("#new-cms-section").onclick = () =>
    sectionEditor({
      kind: $("#cms-section-kind").value,
      title: "بخش تازه",
      enabled: true,
      items: [],
    });
  $("#torob-form").onsubmit = async (e) => {
    e.preventDefault();
    const form = e.target,
      button = $("button[type=submit]", form);
    button.disabled = true;
    try {
      const f = new FormData(form);
      await save({
        ...settings,
        torob: {
          enabled: f.has("enabled"),
          webhookToken: f.get("webhookToken"),
          clearToken: f.has("clearToken"),
        },
      });
    } catch (error) {
      message(form, error.message);
    } finally {
      button.disabled = false;
    }
  };
  void api("/seller/torob", undefined, "GET")
    .then((out) => {
      if (signal?.aborted) return;
      $("#torob-sync-status").textContent =
        `${out.sync.pending} محصول در صف اطلاع‌رسانی. ${out.sync.lastSuccess ? "آخرین پذیرش وبهوک: " + new Date(out.sync.lastSuccess).toLocaleString("fa-IR") : "هنوز وبهوکی توسط ترب تأیید نشده است."}`;
    })
    .catch((error) => {
      if (!signal?.aborted) $("#torob-sync-status").textContent = error.message;
    });
};
window.initBusiness();
