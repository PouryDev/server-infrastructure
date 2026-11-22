"use strict";
const CACHE = "storefront-shell-__STOREFRONT_VERSION__";
const SHELL = [
  "/offline.html",
  "/assets/style.css",
  "/assets/commerce.css",
  "/assets/customer.css",
  "/assets/vazir.woff2",
  "/assets/mark.svg",
];
self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(CACHE).then((cache) => cache.addAll(SHELL)));
  self.skipWaiting();
});
self.addEventListener("activate", (event) => {
  event.waitUntil(
    (async () => {
      const keys = await caches.keys();
      await Promise.all(
        keys
          .filter((key) => key.startsWith("storefront-shell-") && key !== CACHE)
          .map((key) => caches.delete(key)),
      );
      await self.clients.claim();
    })(),
  );
});
self.addEventListener("fetch", (event) => {
  const request = event.request;
  const url = new URL(request.url);
  // Never cache API, payment callbacks, sessions, checkout or personalized HTML.
  // Writes are never queued or replayed in the background.
  if (
    request.method !== "GET" ||
    url.origin !== self.location.origin ||
    url.pathname.startsWith("/api/") ||
    url.pathname.startsWith("/payment/") ||
    url.pathname === "/healthz" ||
    url.pathname === "/readyz"
  )
    return;
  if (request.mode === "navigate") {
    event.respondWith(
      fetch(request).catch(() => caches.match("/offline.html")),
    );
    return;
  }
  if (url.search || !SHELL.includes(url.pathname)) return;
  event.respondWith(
    (async () => {
      try {
        const response = await fetch(request);
        if (response.ok) {
          const cache = await caches.open(CACHE);
          await cache.put(request, response.clone());
        }
        return response;
      } catch (error) {
        const cached = await caches.match(request);
        if (cached) return cached;
        throw error;
      }
    })(),
  );
});
