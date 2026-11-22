# laravelkit

The code every Laravel project of mine repeats, written once: the uniform error body, filtering / sorting / pagination,
stateless JWT, the shared-Redis cache contract, OpenTelemetry, SMS and email. The Laravel twin of `gokit`: same
contracts, same JSON shapes, so a Laravel service and a Go service look the same from outside and can verify each
other's tokens. It has no business logic and **invents no wheels**: Eloquent, the cache and queue managers, `Mail`, the HTTP
client and the exception handler are Laravel's own; the kit only adds what was missing.

**Nothing runs outside Docker.** All commands go through `make` in the infrastructure repo (they call `scripts/laravelkit.sh`).

```bash
make laravelkit-install      # asks for a Laravel project, vendors the kit into <project>/.laravelkit and requires it
make laravelkit-sync         # refresh that copy after the kit changed
make laravelkit-check        # Pint + unit tests
make laravelkit-integration  # tests against a throw-away Redis and a stand-in OTLP collector
make laravelkit-jwt-key      # new Ed25519 key pair (ARGS=--hs256 for a shared secret)
```

## Installing into an existing project

```bash
make laravelkit-install                 # in this repo; then, in the project's own dev container:
composer update pourydev/laravelkit     # firebase/php-jwt is the only new required package
php artisan laravelkit:install          # publishes config/laravelkit.php, appends the variables to .env.example
php artisan laravelkit:jwt-key          # JWT key pair for the environment
```

The kit is a Composer *path* package (a vendored copy in `.laravelkit/`, like gokit's `.kit/`), so a project builds before the
kit is published anywhere: **commit `.laravelkit/` and `COPY` it in the Dockerfile before `composer install`**. Its version follows its content, so
`make laravelkit-sync` + `composer update pourydev/laravelkit` always picks up changes. Requirements: PHP 8.2+, Laravel 11, 12 or 13.
Nothing is switched on by editing code: the service provider is auto-discovered and registers everything below; each part is
configured in `config/laravelkit.php` from environment variables.

Telemetry is optional: `composer require open-telemetry/sdk open-telemetry/exporter-otlp guzzlehttp/guzzle google/protobuf`
(add `ext-protobuf` for speed) and set `OTEL_EXPORTER_OTLP_ENDPOINT`. EdDSA JWT keys need `ext-sodium` (bundled with PHP images).

## What it gives you

| Part | What it does |
|---|---|
| `Errors` | `AppException` with a `Kind` (`Invalid`, `NotFound`, `Conflict`, `Unauthorized`, `Forbidden`, `Unavailable`, `Internal`), a stable code, a client-safe message, field errors and a hidden cause. `ErrorRenderer` turns **every** exception on API requests (Laravel's validation, auth, 404, throttle, unique-violation included) into `{"error": {"code", "message", "fields"?, "request_id"?}}`. 5xx bodies are always `internal error`; HTTP exception texts are never echoed. |
| `Query` | **Filtering, sorting, pagination** on any Eloquent / query builder: `Schema` (white / black lists, per field), `Params::fromRequest`, `Lister::paginate` (offset, `has_next`, optional total) and `Lister::cursor` (keyset, Laravel's `cursorPaginate`). Same query-string grammar and `{items, meta}` response as gokit. |
| `Auth` | Stateless JWT (HS256 or **Ed25519**, `kid` rotation, access + refresh, algorithm pinned per key id), optional **revocation** (`logout`, "log out everywhere") on the shared Redis, a Laravel guard (`jwt`), middleware `kit.auth` / `kit.roles:admin`, and `PasswordHasher` (argon2id that upgrades old bcrypt hashes). Keys are the PEM files gokit prints, so tokens verify across languages. |
| `Http` | `RequestId` (kept if sane, echoed, shared with every log line, API responses `no-store` unless the handler chose otherwise), `AccessLog` (route template, status, duration, `trace_id`, no query string), `GET /healthz` (no dependencies) and `GET /readyz` (database, redis), all registered for you. |
| `Telemetry` | OTLP traces + metrics under the infra contract: always-on parent-based sampling, W3C Trace Context + Baggage, `http.server.request.duration`, spans for DB queries, outgoing HTTP calls (`traceparent` is injected) and queue jobs (context travels in the payload). No endpoint = disabled. |
| `Logging` | Channel `kit` (`LOG_CHANNEL=kit`): one JSON object per line on stderr with `trace_id` / `span_id`. |
| `Cache` | `KitCache` (`remember` with mandatory capped TTL, jitter, lock-based stampede protection, short negative caching, **fail-open**; `scopedKey` + `bump` invalidation without `KEYS`), `ReadModel` (`getMany` = one MGET + one batched query), `Structures` (counters, rankings). |
| `Notify` | `Notifier` (`sendEmail`, `sendSms`) with drivers `sync` (retry with jittered backoff), `async` (Laravel queue job; retries and dead letters are `queue:work` / `failed_jobs`) and `log`. Email goes through Laravel's mailer, SMS through Kavenegar, Twilio or a log provider. Validated first, masked recipients in logs, never the body. |

## Errors

```php
throw AppException::notFound('Order');                          // 404 {"error":{"code":"not_found","message":"Order not found"}}
throw AppException::invalid('invalid input', ['email' => 'is required']);   // 400 with fields
throw AppException::make(Kind::Conflict, 'order_paid', 'order already paid');
```

API requests are those under `laravelkit.errors.paths` (`api/*`, `v1/*` by default) or that ask for JSON. Other requests keep Laravel's rendering.
`AppException`s below 500 are not *reported* (they are expected outcomes); everything else is, with the request id in the log context.

## Filtering, sorting, pagination

```
GET /v1/users?filter[role]=admin&filter[email][prefix]=ada&filter[created_at][between]=2026-01-01,2026-02-01
             &filter[id][in]=<uuid>,<uuid>&filter[deleted_at][null]=true&sort=-created_at,name&page=2&page_size=50
```

Operators: `eq ne gt gte lt lte like prefix in nin between null`; which apply depends on the field type
(`string int float bool time uuid`). Responses are `{"items": [...], "meta": {"page", "page_size", "has_next", "total", "total_pages"}}`.

```php
$schema = Schema::make()
    ->field('id', FieldType::Uuid)->field('email', FieldType::String)->field('created_at', FieldType::Time)
    ->field('notes', FieldType::String)->deny('notes')                       // black list; allowOnlyFilter()/allowOnlySort() = white list
    ->defaultSort('-created_at')->tieBreaker('id')->pageSize(20, 100);

return Lister::paginate(User::query()->where('tenant_id', $tenant), $schema, Params::fromRequest($request),
    map: fn (User $u) => new UserResource($u));                              // returns a Page: Responsable, JSON as above
// $schema->restrict(denyFilter: ['role'])  = a stricter copy for one request (non-admins)
// Lister::cursor(...)                      = keyset pagination, constant cost at any depth
```

* **Safety:** column names come only from the schema, never from the request; every value is a bind; `LIKE` wildcards in user
  input are escaped; an unknown and a forbidden field give the same message; limits on filters, list size, sort keys, page size and
  offset depth. `restrict()` only accumulates, and a mistyped name in a list is an error (it cannot silently expose a field).
* `paginate` fetches `page_size + 1` rows for `has_next` and skips `count(*)` when the first page already holds everything;
  `withTotal: false` never counts.
* `like` is `ILIKE` on PostgreSQL. A `JOIN`ed column goes in as `->field('author', FieldType::String, column: 'authors.name')`.

## Auth (stateless JWT)

```php
$jwt = app(Jwt::class);
$pair = $jwt->issuePair((string) $user->id, roles: ['admin'], extra: ['tenant' => $tenantId]);  // access 15 min, refresh 30 d
return $pair;                                                      // {"access_token", "refresh_token", "token_type": "Bearer", "expires_in"}
$claims = $jwt->verify($refreshToken, TokenType::Refresh);         // throws AppException: token_expired | invalid_token | token_revoked

Route::middleware(['kit.auth', 'kit.roles:admin,editor'])->group(...);   // precise error codes, claims in request attributes
Route::middleware('auth:jwt')->get('/me', fn () => auth()->user());     // standard guard; JwtUser, or your Eloquent user via a provider
```

Tokens are stateless: validity = signature + issuer + audience + type + expiry. Access tokens carry roles; refresh tokens carry
identity only, so the role is re-read when they are exchanged. Rotate keys with `previous_keys` in the config and drop the old one
after its tokens expired. Set `JWT_REVOCATION_STORE=redis` to make tokens revocable (`Revocations::revoke($claims)`,
`revokeSubject($id)`); every key has a TTL, and if Redis is down the request **fails closed (503)**. A guard with a user provider
(`'jwt' => ['driver' => 'jwt', 'provider' => 'users']`) loads the Eloquent user by the token's subject.
Passwords: `PasswordHasher::verify()` returns `[matches, needsRehash]`; after a successful login store `hash()` again when asked.
The installer does **not** change Laravel's default hash driver: with `hashing.verify` on, Laravel refuses a bcrypt hash once the driver is argon2id, so switching a project that already has bcrypt hashes is a deliberate step (use `PasswordHasher`, which accepts both).

## Cache and read models

Caching is opt-in and lives in a **decorator over a repository**, never in models or controllers. Cache only what you choose.

```php
$user = $cache->remember($cache->scopedKey('user', $id), 300, fn () => User::find($id), negativeTtl: 30);
$cache->bump('user');                                             // after a write: every cached user is stale, old keys just expire
$card = new ReadModel($cache, 'product-card', 3600, fn (string $id) => ProductCard::build($id));
$cards = $card->getMany($ids, fn (array $missing) => ProductCard::buildMany($missing));   // one MGET + ONE query for the misses
```

Rules the kit enforces for the shared Redis: every key expires (a TTL is mandatory, capped by `max_ttl`) with jitter; **fail open** (a
cache outage is logged and the loader runs); concurrent misses run one loader (an atomic lock, the others wait up to 3 s); `null` is cached only with
`negativeTtl`; loader errors are never cached. Set `REDIS_PREFIX` and `CACHE_PREFIX` to `REDIS_KEY_PREFIX` (the installer adds both) so every key starts with it.
Whatever is cached must be rebuildable from the database; money, stock and authorisation read the database.

## Notifications

```php
$notifier = app(Notifier::class);                                                   // driver from NOTIFY_DRIVER: sync | async | log
$notifier->sendSms(new Sms('09123456789', 'کد ورود شما: 123456'));
$notifier->sendEmail(new Email(['ada@example.com'], 'Welcome', text: '...', html: '<p>...</p>'));
```

* Both drivers validate first (`invalid_argument` with fields: header injection, bad address or number), so a bad message fails in the request.
* **sync** returns after the provider accepted it, retrying transient failures (`NOTIFY_RETRIES`, jittered backoff); an unfixable error
  (`PermanentFailure`: bad credentials, rejected number) is not retried. Errors: `delivery_rejected` (400) or `delivery_failed` (503).
* **async** queues one job per message on `NOTIFY_QUEUE`; run `php artisan queue:work --queue=notifications`. Retries and dead letters are
  Laravel's (`tries`, `backoff`, `failed_jobs`, `queue:retry`). Delivery is at-least-once: `Email::$id` / `Sms::$id` is stable across retries.
* Logs carry masked recipients (`0912***6789`) and the message id, never the body (OTP codes, reset links). New SMS providers implement `SmsSender`.

## Telemetry

The infra hands out the **gRPC** endpoint (`http://otel-collector:4317`, `OTEL_EXPORTER_OTLP_PROTOCOL=grpc`). PHP has no gRPC client
without an extension, so the kit sends OTLP/**HTTP** to the collector's port 4318 automatically (verified against a stand-in collector in the integration tests).
Spans are exported after the response was sent; metrics are cumulative per PHP worker (`service.instance.id` = host:pid), exported at most every
`metric_interval` seconds, and flushed at worker exit. The collector does the tail sampling, so the kit never samples.
Bounded labels only: method, route *template*, status.

## Limits (by design or not yet)

* Same as gokit: no rate limiting (nginx does it at the edge). Laravel migrations stay Laravel's: there is no "migrations from models" here because Eloquent models do not declare columns.
* RabbitMQ: the kit does not wrap AMQP. For Laravel, use a queue driver such as `vladimir-yuldashev/laravel-queue-rabbitmq` configured from `AMQP_URL` (declare durable queues; RabbitMQ 4 rejects transient non-exclusive ones). Not included yet.
* Notify has no attachments, templating, delivery webhooks, provider failover or SMTP pooling (same as gokit).
* `Query` filters columns of one table or of tables you `join` yourself; there is no `filter[relation.column]` sugar.
* If the OTLP collector is down the OpenTelemetry SDK prints an error line per export to stderr, and an export that hangs (not refused) can hold a worker up to a few seconds *after* the response was sent.
* Tested: PHP 8.4, Laravel 13, predis against Redis 8, FPM-style per-request lifecycle. Not yet exercised: phpredis, Octane (the guard resets per request, but nothing else was run under it), Laravel 11 / 12 (the constraints allow them).

## Working on the kit

* `make laravelkit-check` must stay green (Pint + 71 unit tests); `make laravelkit-integration` runs everything else (Redis contract, real OTLP export).
* Composer packages come from the shared Docker volume `laravelkit-composer`; adding or updating one needs the network. Behind a blocking network run
  `HTTPS_PROXY=http://127.0.0.1:2020 scripts/laravelkit.sh composer require ...`.
