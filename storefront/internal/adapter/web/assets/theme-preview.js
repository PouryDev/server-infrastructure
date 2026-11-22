"use strict";
window.initThemePreview = function initThemePreview() {
  const bar = document.querySelector(".theme-debugbar");
  if (!bar) return;
  const preview = JSON.parse(
    document.getElementById("boot").textContent,
  ).themePreview;
  if (!preview) return;
  const select = document.getElementById("debug-theme");
  const choices = new Map(
    preview.themes.map((option) => [option.theme.id, option]),
  );
  choices.set("", preview.original);
  const accent = document.getElementById("debug-accent");
  if (accent) accent.value = preview.accent || preview.original.theme.accent;
  function apply(id, navigate = false) {
    if (!choices.has(id)) id = "";
    const { theme, design } = choices.get(id);
    document.body.dataset.layout = theme.layout;
    document.body.dataset.style = theme.id;
    for (const key of ["Hero", "Grid", "Card", "Header"]) {
      document.body.dataset[key.toLowerCase()] = design[key];
    }
    document.body.style.setProperty(
      "--accent",
      accent?.value || preview.original.theme.accent,
    );
    document.body.style.setProperty("--paper", theme.background);
    const hex = (accent?.value || preview.original.theme.accent).slice(1);
    const rgb = [0, 2, 4].map((index) =>
      parseInt(hex.slice(index, index + 2), 16),
    );
    document.body.style.setProperty(
      "--on-accent",
      rgb[0] * 299 + rgb[1] * 587 + rgb[2] * 114 > 150000
        ? "#172520"
        : "#ffffff",
    );
    document
      .querySelector('meta[name="theme-color"]')
      .setAttribute("content", accent?.value || preview.original.theme.accent);
    select.value = id;
    document.getElementById("theme-preview-status").textContent = id
      ? `${theme.name} · ${preview.themes.findIndex((option) => option.theme.id === id) + 1} از ${preview.themes.length}`
      : "تم فعلی فروشگاه";
    const url = new URL(location.href);
    if (id) url.searchParams.set("preview", id);
    else url.searchParams.delete("preview");
    history.replaceState(null, "", url.href);
    document.cookie = `storefront-preview=${encodeURIComponent(id)}; Path=/; SameSite=Lax${location.protocol === "https:" ? "; Secure" : ""}${id ? "" : "; Max-Age=0"}`;
    if (accent) {
      const custom = accent.value !== preview.original.theme.accent;
      document.cookie = `storefront-accent=${custom ? accent.value : ""}; Path=/; SameSite=Lax${location.protocol === "https:" ? "; Secure" : ""}${custom ? "" : "; Max-Age=0"}`;
    }
    if (
      navigate &&
      JSON.parse(document.getElementById("boot").textContent).view === "home"
    )
      void window.storefrontNavigation?.visit(url.href, {
        replace: true,
        preserveScroll: true,
      });
  }
  accent?.addEventListener("input", () => apply(select.value));
  select.addEventListener("change", () => apply(select.value, true));
  bar.querySelectorAll("[data-theme-step]").forEach((button) =>
    button.addEventListener("click", () => {
      const ids = ["", ...preview.themes.map((option) => option.theme.id)];
      const index = ids.indexOf(select.value);
      apply(
        ids[
          (index + Number(button.dataset.themeStep) + ids.length) % ids.length
        ],
        true,
      );
    }),
  );
  document
    .getElementById("reset-theme-preview")
    .addEventListener("click", () => {
      if (accent) accent.value = preview.original.theme.accent;
      apply("", true);
    });
  apply(preview.selected);
};
window.initThemePreview();
