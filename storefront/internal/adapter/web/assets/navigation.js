"use strict";
(() => {
  const privatePaths = /^\/(api|payment|seller|admin|assets|media)(\/|$)/;
  function eligible(raw, base = location.href) {
    const url = new URL(raw, base);
    return (
      url.origin === location.origin &&
      !privatePaths.test(url.pathname) &&
      !/\.[a-z0-9]+$/i.test(url.pathname)
    );
  }
  let active,
    generation = 0;
  let currentURL = location.href;
  const positions = new Map();
  history.scrollRestoration = "manual";
  function error(url) {
    document.getElementById("navigation-error")?.remove();
    const box = document.createElement("div");
    box.id = "navigation-error";
    box.setAttribute("role", "alert");
    box.textContent = "ارتباط برقرار نشد. صفحه و سبد شما حفظ شده‌اند.";
    const retry = document.createElement("button");
    retry.className = "navigation-retry";
    retry.textContent = "تلاش دوباره";
    retry.onclick = () => visit(url);
    box.append(retry);
    document.body.append(box);
  }
  async function visit(raw, options = {}) {
    const requested = new URL(raw, location.href);
    if (!eligible(requested.href)) {
      location.assign(requested.href);
      return;
    }
    const ticket = ++generation;
    active?.abort();
    active = new AbortController();
    const controller = active;
    if (!options.pop) currentURL = location.href;
    document.getElementById("navigation-progress")?.remove();
    document.getElementById("navigation-error")?.remove();
    const progress = document.createElement("div");
    progress.id = "navigation-progress";
    progress.setAttribute("aria-label", "در حال دریافت صفحه");
    document.body.append(progress);
    document.getElementById("main")?.setAttribute("aria-busy", "true");
    const timeout = setTimeout(() => controller.abort(), 15000);
    try {
      const response = await fetch(requested.href, {
        credentials: "same-origin",
        headers: { Accept: "text/html" },
        signal: controller.signal,
        cache: "no-store",
      });
      if (ticket !== generation) return;
      if (!response.ok) throw new Error("page unavailable");
      if (!eligible(response.url)) {
        location.assign(response.url);
        return;
      }
      const next = new DOMParser().parseFromString(
        await response.text(),
        "text/html",
      );
      const currentVersion = document.querySelector(
        'meta[name="storefront-assets"]',
      )?.content;
      const nextVersion = next.querySelector(
        'meta[name="storefront-assets"]',
      )?.content;
      if (currentVersion && nextVersion && currentVersion !== nextVersion) {
        location.assign(requested.href);
        return;
      }
      const shell = next.getElementById("app-shell"),
        boot = next.getElementById("boot");
      if (!shell || !boot || !shell.querySelector("#main"))
        throw new Error("incomplete page");
      JSON.parse(boot.textContent);
      if (ticket !== generation) return;
      positions.set(currentURL, [scrollX, scrollY]);
      const destination = new URL(response.url);
      if (requested.hash) destination.hash = requested.hash;
      const update = () => {
        if (ticket !== generation) return;
        window.storefrontCleanup?.();
        document
          .getElementById("app-shell")
          .replaceWith(document.importNode(shell, true));
        document.title = next.title;
        for (const name of document.body.getAttributeNames())
          document.body.removeAttribute(name);
        for (const attr of next.body.attributes)
          document.body.setAttribute(attr.name, attr.value);
        document.getElementById("boot").textContent = boot.textContent;
        for (const selector of [
          'meta[name="description"]',
 'meta[name="product_id"]','meta[name="product_name"]','meta[name="product_price"]','meta[name="product_old_price"]','meta[name="availability"]','meta[name="guarantee"]','meta[property="og:image"]','meta[property="og:type"]',
          'meta[name="theme-color"]',
          'meta[property="og:title"]',
          'meta[property="og:description"]',
          'link[rel="canonical"]',
          'script[type="application/ld+json"]',
        ]) {
          const fresh = next.head.querySelector(selector),
            old = document.head.querySelector(selector);
          if (fresh && old) old.replaceWith(document.importNode(fresh, true));
 else if(fresh)document.head.append(document.importNode(fresh,true));
 else if(old)old.remove();
        }
        if (options.pop)
          history.replaceState(history.state, "", destination.href);
        else if (options.replace)
          history.replaceState({}, "", destination.href);
        else history.pushState({}, "", destination.href);
        currentURL = destination.href;
        window.initStorefront?.();
        window.initThemePreview?.();
        window.initCustomerPanel?.();
        window.initMerchandising?.();
 window.initBusiness?.();
        currentURL = location.href;
        const main = document.getElementById("main");
        main?.focus({ preventScroll: true });
        const saved = options.pop ? positions.get(currentURL) : null;
        if (saved)
          scrollTo({ left: saved[0], top: saved[1], behavior: "instant" });
        else if (destination.hash)
          document.getElementById(destination.hash.slice(1))?.scrollIntoView();
        else if (!options.preserveScroll)
          scrollTo({ top: 0, left: 0, behavior: "instant" });
      };
      if (
        document.startViewTransition &&
        !matchMedia("(prefers-reduced-motion: reduce)").matches
      )
        await document.startViewTransition(update).finished;
      else update();
    } catch (e) {
      if (ticket === generation) {
        if (options.pop) {
          history.replaceState(history.state, "", currentURL);
        }
        error(requested.href);
      }
    } finally {
      clearTimeout(timeout);
      if (ticket === generation) {
        document.getElementById("navigation-progress")?.remove();
        document.getElementById("main")?.removeAttribute("aria-busy");
      }
    }
  }
  document.addEventListener("click", (event) => {
    const link = event.target.closest?.("a[href]");
    if (
      !link ||
      event.defaultPrevented ||
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.altKey ||
      event.shiftKey ||
      link.target ||
      link.hasAttribute("download") ||
      link.hasAttribute("data-native") ||
      privatePaths.test(location.pathname)
    )
      return;
    const url = new URL(link.href);
    if (
      !eligible(url.href) ||
      (url.pathname === location.pathname &&
        url.search === location.search &&
        url.hash)
    )
      return;
    event.preventDefault();
    void visit(url.href);
  });
  document.addEventListener("submit", (event) => {
    const form = event.target;
    if (
      event.defaultPrevented ||
      form.method.toLowerCase() !== "get" ||
      !eligible(form.action) ||
      privatePaths.test(location.pathname)
    )
      return;
    const url = new URL(form.action);
    url.search = new URLSearchParams(new FormData(form)).toString();
    event.preventDefault();
    void visit(url.href);
  });
  addEventListener("popstate", () => void visit(location.href, { pop: true }));
  window.storefrontNavigation = { visit, eligible };
})();
