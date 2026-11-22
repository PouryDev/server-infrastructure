# Storefront

A Go SSR storefront product generated with the infrastructure's `make new-go-project` command. One reusable image serves independent stores, each with its own PostgreSQL database/role, Redis ACL, RabbitMQ vhost, upload directory, secrets and container limits. The code has no accounting/Mizan dependency.

## Local sample

- Store: http://shoptest.localhost
- Customer: `/account`
- Seller: `/seller`, demo phone `+989120000002`
- Developer: `/admin`, demo phone `+989120000001`
- Infrastructure launcher: http://manager.localhost → فروشگاه‌ساز

Choose OTP on the sample login page. A newly generated code appears on that page **only in demo mode**; no SMS is sent and no money is collected by the explicitly labeled demo gateway. Passwords can be set after signing in. Production authentication requires the configured Kavenegar OTP template. There is no email identity, email registration or email notification flow.

## Build and verify

Run from the infrastructure root:

```sh
make storefront-check                    # formatting, vet, static analysis, tests, JS parsing, Go build
make storefront-integration              # real PgBouncer, isolated schema, transaction rolled back
make storefront-smoke                    # local demo HTTP flow; creates a labeled test order
make manager-check                       # manager Go/React tests and production build
make storefront-image                    # unique immutable image tag
make manager-up                          # local manager and restricted provisioner
```

All Go and Node commands execute in Docker. Infrastructure PostgreSQL/Redis/RabbitMQ are reused; the tools Compose file starts no databases or brokers. `storefront-integration` uses shoptest's database role through PgBouncer and rolls back its unique test schema. It does not delete existing application data.

To create a store, use the manager launcher or send a validated JSON document on stdin to `make storefront-create`:

```json
{
  "name": "newshop",
  "domain": "newshop.localhost",
  "title": "فروشگاه من",
  "theme": "atelier",
  "ownerPhone": "+989121234567",
  "adminPhone": "+989121234568",
  "demo": true,
  "image": "storefront-api:<built-immutable-tag>",
  "modules": ["blog", "attachments", "discounts", "wishlist", "analytics"]
}
```

For a live store set `demo:false`, use its public domain and include `smsKey` and `smsTemplate`. Never put real credentials in tracked files or shell arguments. The manager accepts them privately and does not include them in jobs/audit responses. TLS/DNS/CDN routing must be configured on the destination infrastructure. A manager running on a server provisions on that server; a local manager provisions locally. This builder does not silently provision an arbitrary remote SSH host. Existing manager Shipping remains available for approved release transfer.

Credentials live in `<APPS_DIR>/<store>/.env` and `storefront.env`, mode 0600. The latter is read as a raw env file (Docker Compose 2.30+). Gateway credentials configured by the seller are encrypted with AES-GCM using the instance secret; API reads mask them. Keep `STORE_SECRET` with the database backups. Never regenerate it when restarting/upgrading an instance.

## Runtime and deployment

`/healthz` checks process liveness; `/readyz` checks database readiness. HTTP is behind the existing nginx/CDN. Every running instance is non-root, read-only, has no Docker socket/published port, joins `infra` and has CPU/RAM limits. Application telemetry uses the shared OTel collector, with the actual instance name as namespace.

The manager API/web has no Docker access. The authenticated `manager-storefront` runner accepts a typed store specification, verifies names/domains/images and invokes the fixed make target. It has a single serialized worker, durable jobs and an additional global lock for shared nginx changes. Closing the browser does not cancel creation. Repeated request keys refer to the same job. An interrupted operation is not blindly replayed.

Provisioning journals are in `<APPS_DIR>/.storefront/`; runtime manifests/env files are in `<APPS_DIR>/<store>/`. Initial provisioning reuses `scripts/new-project.sh`. If this older infrastructure command fails after allocating resources but before writing credentials, creation deliberately stops for inspection rather than resetting passwords or deleting resources. Later stages can resume with the same identity. Nginx routing is activated after migrations and container health succeed; failed nginx validation restores its prior configuration.

Upgrade one local instance with a built image:

```sh
make storefront-release NAME=shoptest IMAGE=storefront-api:<new-tag>
```

Release saves the previous manifest, runs migrations as a one-shot service and waits for health. On failure it restores the previous image/manifest. SQL migrations are **not** automatically reversed; write compatible forward migrations. Files and database contents are not copied by image deployment. Back up the instance database through the infrastructure's backup process, the upload directory and both private env files; restore them together on an isolated target before routing production traffic.

## Product boundaries

- SSR pages: catalog, product, category/search, blog/article and informational pages. Page metadata, canonical links, XML sitemap, robots and Product structured data are server rendered.
- Mobile auth: normalized `+989xxxxxxxxx`, OTP cooldown/attempt limits and single-use codes, Argon2id passwords, opaque hashed server-side sessions, CSRF and exact Origin/Host checks.
- Catalog: products, variants, SKU, prices, stock, images and per-variant availability. Existing stock edits apply an explicit adjustment so an intervening checkout reservation is not overwritten.
- Checkout: authoritative prices, stock reservation, stable request keys, address validation, campaign/redemption limits and immutable order snapshots. Expired unpaid reservations are released. A verified payment arriving after expiry is marked for review rather than silently fulfilled.
- Attachments: product-defined rule, min/max, MIME/size and per-row/per-unit counting; each upload belongs to a user/product/variant/rule. Orders snapshot the rules. Customer files are private, served with attachment disposition and checked ownership; public brand/product images use separate media records.
- Campaigns: percent, fixed amount, shipping discount, buy-X-get-Y (cheapest eligible units), automatic/coded, schedules, minimum spend/quantity, caps, product/variant scope, phone allowlist, first successful order, global/per-customer limits, stacking and best exclusive discount. A zero-total order skips external payment.
- Analytics: real page/product/cart/checkout events and authoritative paid orders/revenue; daily sales, product views, UTM campaign visits, 7/30/90-day ranges and CSV. Browser events can be blocked; they are not represented as exact server-side sales. Figures contain labeled demo orders when testing locally.
- Seller: catalog, orders/fulfillment, gateway configuration and BNPL product/variant policies, campaigns, blog, branding, content, media and section order. Developer admin can use the seller panel and additionally choose the theme/modules. Seller routes/API do not expose theme selection.
- Themes: sixteen storefront architectures: formal, startup, motion, minimal, marketplace, brutalist, luxury, playful, editorial and technical. Their DOM, density, typography, card and mobile treatments differ. Brand accent is independent of style. Shared checkout/domain behavior is reused.
- Support: the configured Gapchi widget/API owns conversations and agent logic. Optional `GAPCHI_IDENTITY_SECRET` signs logged-in customer identity for Gapchi. A support outage does not block checkout.

## Temporary theme debugbar

Demo storefronts expose a top bar with all sixteen styles and an independent brand color picker, previous/next controls and reset to the actual store branding. Changes apply instantly to the current page without reloading or clearing checkout/cart state. A session-only `storefront-preview` cookie preserves the selection across routes; the server validates its value against known presets. Query links such as `/?preview=mono` are also supported. Preview pages are private/no-store/noindex. Store settings and the installed PWA identity are unchanged. Production customers and sellers do not receive the debugbar/catalog; existing developer-authorized query previews remain available.

## App navigation and shopping

`/shop` is the searchable catalog, with categories, stock and price filters (customer input in toman; persisted prices remain IRR) and server-side sorting. Older `/products` collection links redirect to `/shop`; product URLs remain `/products/<slug>`. All routes render complete HTML without JavaScript.

`navigation.js` progressively enhances local links and GET forms into app navigation, including history/back-forward, focus, scroll recovery, loading and retry states. It fetches fresh SSR HTML, never queues payments and keeps payment callbacks, seller/admin and external destinations native. Each route has a scoped initializer/abort controller. Cards support quick-add for single-variant products; multi-variant cards link to model selection. Mobile uses a dedicated app home, horizontal category/product rails and a shop filter sheet, rather than shrinking the desktop showcase. Reduced-motion preferences disable decorative motion and view transitions.

New instances accept an independent `accent` hex color in the manager/creation JSON, seeded via `STORE_INITIAL_ACCENT`. Previous theme IDs resolve to their closest maintained family for existing stores.

## Customer workspace and merchandising

`/account` is the customer dashboard. Its dedicated order list/detail, address book, profile and security routes live under `/account/`. Order counters cover the customer's full history; lists are paginated and order details enforce ownership. The address book supports up to 20 named addresses, a default recipient and editing/deletion. Checkout can reuse a saved address or explicitly save a new one. Changing a saved address never changes an existing order's delivery snapshot. Phone/role/password are not editable through the name-profile endpoint.

The seller's **کالکشن و ویترین** section creates manual ordered product collections or automatic collections using category, availability, product badge and price rules. Each collection has an independent title, description, card limit, visibility, lazy-loading preference and homepage position. Collection edits use the existing settings revision/row lock; stale edits return a conflict. Enabled collections have complete SSR pages at `/collections/<id>` and appear in the sitemap. Inactive products are excluded.

The configurable `category-products` homepage section shows a separate product rail per category. Rails fetch escaped public card fragments when approaching the viewport, with at most two concurrent requests, retry/fallback links and route-scoped cancellation. Collection rails can use the same loading path or SSR. Homepage section order applies on mobile as well as desktop. Without JavaScript, category links and collection/catalog pages remain available to users and crawlers.

Conversion aids include quick-add, saved items, an actual free-shipping threshold/progress indicator, real low-stock signals and eligible active coupon offers with explicit terms. Offer visibility respects schedule, customer targeting, first paid order and redemption limits; checkout remains authoritative. These features do not establish a measured conversion uplift.

Migration `20261115000004_customer_merchandising.sql` creates the owned address book and order-history index, and adds default collection/category sections while preserving existing store content and gateway secrets. Release applies it through the normal one-shot migration command. The Go/PostgreSQL suite covers ownership/default-address concurrency boundaries, settings conflicts and collection selection; browser-source tests cover address selection, deferred rails, bounded concurrency and navigation cancellation.

## Mobile navigation and PWA

Every storefront theme uses the shared bottom navigation at widths up to 800px, with active-page states, cart counts and device safe-area spacing. The optional wishlist entry follows its module setting. Desktop navigation is retained; seller/developer panels keep their dedicated navigation.

`/manifest.webmanifest` uses the store name, selected theme and brand accent. The app has 192/512px maskable PNG icons, an Apple touch icon and standalone launch mode. Supporting browsers expose installation; the footer also offers installation when the browser supplies its native prompt, with Add to Home Screen guidance on iOS. Production PWA APIs require HTTPS; localhost development is supported.

The root-scoped `/sw.js` updates its revision when shell assets change. It caches only the static offline recovery page, stylesheet, font and mark. Navigations use the network, with the recovery page on connection failure. Personalized HTML, APIs, private uploads and payment callbacks are not cached; writes/payments are never queued or replayed. The browser-local cart survives disconnection. Prices, stock, sign-in and checkout require a live connection.

The frontend regression suite exercises legacy null attachment collections, removed variants, empty/error states, and the worker's cache/write boundaries. Go HTTP tests check manifest/icons and mobile navigation across all sixteen themes.

## Payment integration status

Adapters exist for Zibal, Zarinpal, SnappPay, DigiPay, Azkivam, TorobPay and Tara. Official contracts and source URLs are recorded in [docs/payment-contracts.md](docs/payment-contracts.md). Contract tests use local mock servers; they are not provider acceptance/certification tests.

SnappPay/TorobPay verify then settle, with status reconciliation. Their gross/net cart arithmetic intentionally differs according to their respective primary sources. DigiPay binds callback purchase type and reports delivery when the seller marks actual delivery complete. Tara uses a provider POST form and reconciled invoice amounts. Azkivam uses its documented auth/purchase/status/verify/cancel/reverse protocols and a stable numeric provider identifier.

Real credentials, an externally reachable approved callback, registered IP/domain and provider approval are required for live acceptance tests. `shoptest.localhost` cannot establish that certification. General refunds/reconciliation beyond the provider operations documented here are not an accounting system; unsupported refunds must be handled in the merchant's provider panel. Partial-order edits are not exposed as a completed return/refund workflow.

## Source layout

```text
internal/domain/shop       entities, phone normalization, campaign/BNPL rules, themes
internal/usecase/identity  mobile authentication and session workflows
internal/usecase/commerce  commerce transactions and provider ports
internal/adapter/store     PostgreSQL, encrypted payment configuration, persistence
internal/adapter/payments  each provider's HTTP/callback/settlement protocol
internal/adapter/sms       Kavenegar OTP delivery
internal/adapter/web       SSR templates, HTTP delivery, public/private media
internal/bootstrap        wiring, seed and graceful process lifetime
migrations                explicit, PgBouncer-safe one-shot SQL migrations
```

Dependency tests enforce inward imports. The generated local `.kit` snapshot contains generic infrastructure utilities, not store/accounting business logic. SSR uses Go's escaping `html/template` and plain browser JavaScript; there is no additional Node SSR process. Product images are locally served Unsplash photographs, and the Persian typeface is Vazirmatn (SIL OFL).


## Commerce platform upgrade (November 2026)

See [owner and developer handover](docs/commerce-upgrade.md) for the delivery tariff editor, product swatches/gallery/specifications, CMS pages and blocks, Torob Product API v3, verified reviews and return requests. The reusable image remains selectable from the manager launcher; `storefront/catalog.json` is the runner catalog and is checked against the Go renderer.

Full demo QA (creates labeled examples and a demo order, never a real charge):

```sh
STOREFRONT_SMOKE_HOST=shopdemo.localhost make storefront-smoke
STOREFRONT_SMOKE_HOST=shopdemo.localhost make storefront-commerce-smoke
```
