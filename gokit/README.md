# gokit

The code every Go project of mine repeats, written once: database access with generic filtering / sorting /
pagination, stateless JWT authentication, OpenTelemetry, migrations, HTTP plumbing, config and graceful shutdown.
It has no business logic. Projects use it through a clean-architecture skeleton that this repo can generate.

**Nothing runs outside Docker**, not even locally. All commands go through `make` in the repo root (they call
`scripts/gokit.sh`, which starts the Go container).

```bash
make new-go-project     # asks for a directory and module, creates a ready project (make, docker, tests, deploy files)
make gokit-check        # gofmt + vet + golangci-lint + unit tests of the kit
make gokit-integration  # tests against throw-away PostgreSQL, PgBouncer, Redis, RabbitMQ
make gokit-bench        # micro-benchmarks (no database);  make gokit-bench-db  = ORM / PgBouncer benchmarks
make gokit-sync         # copy the current kit into an existing project's .kit/
make gokit-jwt-key      # new Ed25519 key pair (ARGS=--hs256 for a shared secret)
```

## Packages

Every package depends only on the standard library and the small packages below it, never on a project.

| Package | What it does |
|---|---|
| `apperr` | Error kinds (`Invalid`, `NotFound`, `Conflict`, `Unauthorized`, `Forbidden`, `Unavailable`, `Internal`) with a stable code, client-safe message, field errors and a hidden cause. Used by every layer. |
| `query` | **Dynamic filtering, sorting, pagination** as validated, parameterised SQL fragments (no database dependency). See below. |
| `orm` | Small, explicit, fast ORM: typed CRUD, query builder, relations (`belongs_to`, `has_one`, `has_many`, `many_to_many`) preloaded with one query each, one-statement bulk insert, soft delete, optimistic locking, offset **and keyset** pagination. See below. |
| `orm/schema`, `migrate` | **Migrations from models**: reads the live schema, diffs it against the ORM models (relations become foreign keys and join tables) and writes a goose migration with Up **and** Down. |
| `amqpx` | RabbitMQ: reconnects with backoff, re-declares topology, publisher confirms, consumers with ack / nack / requeue, bounded concurrency and graceful stop. |
| `notify` | SMS and email behind one `Notifier`: sync driver (retry, timeouts) and async driver (RabbitMQ queue + worker + DLQ, or in-memory); SMTP, Kavenegar, Twilio providers; traces, metrics, masked logs. |
| `postgres` | pgx pool that is safe behind PgBouncer transaction pooling, retry on start-up, tracing + pool metrics, `DB.WithTx` (re-entrant, repositories join through `ctx`), driver-error to `apperr` mapping, generic `Select` / `SelectOne` / `Value` / `Exec`, and `Paginate[T]`. |
| `migrate` | goose runner over an embedded FS: no session advisory locks, simple protocol, `Up` / `Down` / `Status` / `Create`. Run it as a one-shot command. |
| `auth` | Stateless JWT (optionally **revocable**, see below) (`HS256` or `Ed25519`, `kid`-based key rotation, access + refresh tokens, algorithm pinning, verify-only services) and argon2id password hashing that upgrades itself. Interfaces `TokenVerifier`, `SessionIssuer`, `PasswordHasher` for use cases. |
| `httpx` | Router on `net/http` with middleware groups, `HandlerFunc` returning errors, `Decode[T]` (strict JSON), uniform error body, request id, access log with `trace_id`, panic recovery, CORS, body limit, `Authenticate` / `RequireRoles`, `/healthz` + `/readyz`, server with graceful shutdown. |
| `telemetry` | OTLP traces + metrics following the infra contract (env driven, always-on parent-based sampling, W3C propagation, 30 s metric export, flush on shutdown). No endpoint = disabled. |
| `logging` | `slog` JSON logger that adds `trace_id` / `span_id`. |
| `config` | Typed env loader that reports every problem at once. |
| `retry`, `lifecycle` | Exponential backoff with jitter; runners + LIFO cleanup with signal handling. |
| `redisx` | Redis client that enforces the shared-Redis contract (key prefix, mandatory TTL): `Remember` (cache-aside with stampede protection, jitter, negative caching, fail-open), version-based invalidation, `ReadModel` (Redis-first views with batched `GetMany`), `Hash` / `Ranking` structures, and `Revocations` for revocable tokens. |
| `cmd/gokit` | The generator (`new`, `sync`, `jwt-key`). |

## Filtering, sorting, pagination (`query` + `postgres.Paginate`)

Client side (query string):

```
GET /v1/users?filter[role]=admin&filter[email][prefix]=ada&filter[created_at][between]=2026-01-01,2026-02-01
             &filter[id][in]=<uuid>,<uuid>&filter[deleted_at][null]=true&sort=-created_at,name&page=2&page_size=50
```

Operators: `eq ne gt gte lt lte like prefix in nin between null`; which ones apply depends on the field type
(`string int float bool time uuid`). Responses are `{"items": [...], "meta": {"page", "page_size", "has_next", "total", "total_pages"}}`.

Server side, once per resource:

```go
type userRow struct {
    ID           uuid.UUID `db:"id" json:"id"`
    Email        string    `db:"email" json:"email"`
    PasswordHash string    `db:"password_hash" json:"-" query:"-"`      // never queryable
    Notes        string    `db:"notes" query:"nofilter,nosort"`
}
schema, _ := query.NewSchemaFor[userRow]("u",                           // "u" qualifies columns: u.email
    query.WithDefaultSort(query.Sort{Field: "created_at", Desc: true}),
    query.WithTieBreaker("u.id"),                                       // stable pages
    query.WithPageSize(20, 100),
    query.WithFilterPolicy(query.DenyFields("notes")))                  // black list; query.AllowOnly(...) = white list

page, err := postgres.Paginate[userRow](ctx, db.Querier(ctx), schema, postgres.ListQuery{
    Select: "SELECT u.id, u.email", From: "FROM users u", Where: "u.tenant_id = $1", Args: []any{tenantID},
}, params)   // params come from httpx.ListParams(r)
```

* **White / black lists** are per field and separate for filtering and sorting (`WithFilterPolicy`, `WithSortPolicy`,
  `WithPolicy`). Derive stricter variants per request with `schema.Restrict(...)` (for example a narrower allow list for
  non-admins). Restrictions only accumulate; a typo in a list is an error, so a mistyped deny entry cannot expose a field.
* **Safety:** column names come only from the schema (validated), never from the request; every value is a bind
  argument (`= ANY($n)` for lists); `LIKE` wildcards in user input are escaped; an unknown and a forbidden field produce the
  same message; limits on filters, list size, sort keys, page size and offset depth.
* `Paginate` fetches `page_size + 1` rows to compute `has_next` and skips the `count(*)` when the first page already holds
  everything; `postgres.WithoutTotal()` skips counting entirely. It uses offset pagination (simple, supports page jumps and
  sorting by any field); for very deep feeds add keyset pagination in the repository.


## ORM and migrations from models (`orm`, `orm/schema`, `migrate`)

Put the row structs in the persistence adapter (never in the domain) and map them to entities there.

```go
type authorRow struct {
    ID        uuid.UUID  `db:"id" json:"id" orm:"pk;default=gen_random_uuid()"`
    Name      string     `db:"name" orm:"size=100"`
    Email     string     `db:"email" orm:"unique"`
    Secret    string     `db:"secret" json:"-" query:"-"`            // never client-queryable
    CreatedAt time.Time  `db:"created_at" orm:"created_at"`           // default now()
    UpdatedAt time.Time  `db:"updated_at" orm:"updated_at"`           // refreshed by the database on every update
    DeletedAt *time.Time `db:"deleted_at" orm:"soft_delete"`          // Delete marks, queries hide
    Version   int        `db:"version" orm:"version;default=1"`       // optimistic locking
    Posts     []postRow  `orm:"rel=has_many;fk=author_id"`
}
type postRow struct {
    ID       int64      `db:"id" orm:"pk;auto"`                       // identity column
    AuthorID uuid.UUID  `db:"author_id" orm:"index"`
    Title    string     `db:"title"`
    Author   *authorRow `orm:"rel=belongs_to;fk=author_id;ondelete=cascade"`
    Tags     []tagRow   `orm:"rel=many_to_many"`                      // join table post_tags created for you
}

authors, _ := orm.New[authorRow](db)                                  // db is *postgres.DB; joins transactions through ctx
_ = authors.Create(ctx, &a)                                            // id, timestamps, version read back (RETURNING)
_ = authors.CreateMany(ctx, list)                                      // ONE statement per 5000 rows
a, _ := authors.Get(ctx, id)
_ = authors.Update(ctx, &a, "name")                                    // only that column; stale version -> Conflict
rows, _ := authors.Query().Where(orm.Or(orm.Eq("name", "x"), orm.HasPrefix("email", "a"))).
    OrderByDesc("created_at").Limit(20).Preload("Posts.Tags").All(ctx) // 3 queries total, never one per row
page, _ := authors.Query().Paginate(ctx, params)                       // client filters/sort/page, allow & deny lists
feed, _ := authors.Query().Keyset(ctx, params, cursor)                 // cursor pagination: constant cost at any depth
```

Tags (`orm:"a;b=c"`): `pk auto default=<sql> unique index index=<name> size=<n> type=<sql> null notnull readonly
created_at updated_at soft_delete version -` and for relations `rel= fk= ref= join= joinfk= joinref= ondelete=`.
Gotchas, by design: a column with a database default is *omitted* when the Go value is zero (use `*bool` to store
`false` into a `default true` column); nullable columns need pointer types; relations need a single-column primary key.

**Relations in queries, joins, aggregates and raw SQL**

```go
// rows that HAVE a related row matching (EXISTS: never repeats rows, soft-deleted related rows do not count)
posts.Query().Where(orm.Has("Author", orm.Eq("name", "Ada")), orm.NotHas("Comments")).All(ctx)
posts.Query().Where(orm.Has("Tags", orm.In("name", []string{"go", "sql"}))).All(ctx)       // many_to_many too

// JOIN: filter and sort by the related column ("Relation.column"); LeftJoin keeps rows without a match
posts.Query().Join("Author").Where(orm.Ne("Author.name", "Bob")).OrderBy("Author.name").OrderByDesc("views").
    Paginate(ctx, params)                       // one-row joins paginate; a to-many Join + Paginate is refused (use Has)
authors.Query().Join("Posts").Distinct().Where(orm.Gt("Posts.views", 5)).All(ctx)          // to-many: add Distinct

// aggregates and joined columns in your own result struct
type stat struct{ Author string `db:"author"`; Posts int64 `db:"posts"`; Views int64 `db:"views"` }
orm.Project[stat](ctx, authors.Query().LeftJoin("Posts").GroupBy("name").Having(orm.Gt("posts", 1)).OrderByDesc("views"),
    orm.Col("name", "author"), orm.Count("Posts.id", "posts"), orm.Sum("Posts.views", "views").OrZero())

// anything else (CTEs, window functions, lateral joins, hand-tuned SQL): write it, scan it by column name
orm.Fetch[row](ctx, db, `SELECT p.title, a.name AS author, rank() OVER (PARTITION BY a.id ORDER BY p.views DESC) AS rk
                         FROM posts p JOIN authors a ON a.id = p.author_id WHERE a.name = ANY($1)`, names)
```

`Fetch` and `FetchOne` take any struct (it need not be a model) and refuse results with a column the struct has no
field for or with duplicate column names, instead of silently dropping data. `orm.Raw` embeds a trusted SQL fragment
(with `?` placeholders) in a `Where`. Reproduction of the numbers below: joining through the ORM costs about 10% over
hand-written pgx (and the database time dominates), `Has` compiles to the same plan as a hand-written `EXISTS`.

**Migrations from the models:** `migrate.Diff` creates a temporary shadow database, applies your existing migration
files to it, reads its real schema and compares it with the models, so the diff is against what your migrations
produce, not against a drifted dev database. Tables, columns (type, null, default, identity), primary keys, unique
constraints, indexes and foreign keys (from relations, including the join tables of many-to-many) are covered. The
generated file has Up and Down; risky steps carry `-- WARNING:` comments (adding a NOT NULL column without default,
type changes, new unique constraints). Nothing is dropped unless you opt in, and `migrate.Plan` (the `drift`
command) fails CI when models and migrations disagree. In a generated project: `make migrate-diff NAME=add_orders`,
review, `make migrate-up`; `make migrate-drift` in CI. Also generated:

* **renames**: `orm:"was=old_name"` on a field, `WasTable() string` on the struct. The migration is `RENAME` (data and
  constraints stay), never drop + add;
* **CHECK constraints**: `orm:"check=<sql>"` or a `Checks() map[string]string` method, matched by name (give a changed
  check a new name; expressions are not compared because PostgreSQL rewrites them);
* **non-blocking indexes**: `DiffOptions.ConcurrentIndexes` writes new indexes on existing tables as
  `CREATE INDEX CONCURRENTLY IF NOT EXISTS` into a second file marked `-- +goose NO TRANSACTION`, applied after the
  main one (a new table's indexes are created plainly, the table is empty).

Still manual: partial and expression indexes, primary-key changes, anything data-dependent (backfills).

## Performance (measured)

The kit sits under every project, so the numbers are measured, not assumed. Reproduce with `make gokit-bench`
(no database needed) and `make gokit-bench-db` (throw-away PostgreSQL, PgBouncer, Redis in Docker). Figures are from
one development machine; compare the ratios, not the absolute values.

| What | Result |
|---|---|
| ORM join vs hand-written pgx join (posts of an author, 20 rows) | 1.49 ms vs 1.34 ms (+11%, the query itself dominates); `Has` (EXISTS) 1.36 ms; `Project` aggregate 1.62 ms vs raw 1.65 ms |
| `orm.Get` by primary key | 65.8 µs, same as hand-written pgx (69.6 µs); the generic `postgres.SelectOne` helper takes 91 µs |
| List 100 rows (`Query().All`) | 2.01 ms vs raw pgx 2.04 ms vs `postgres.Select` 2.42 ms; 528 allocations vs 717 raw |
| `CreateMany` 1000 rows | **6.9 ms**, vs 507 ms for a loop of `Create` (74x) and 8.9 ms for pgx `COPY` (which cannot return generated values) |
| `Preload` 100 authors with posts | 2.9 ms (2 queries) vs 11.8 ms for N+1 (101 queries): 4x |
| `redisx.Remember` cache hit | 33.7 µs, 16 allocations (a Redis round trip plus JSON decoding) |
| 100 read-model views | `GetMany` (one MGET) 0.24 ms vs 3.0 ms for 100 single `Get`s (12.7x) |
| Page 300 of 50 rows | offset 8.6 ms vs **keyset 1.8 ms** (the gap grows with depth) |
| Named prepared statements behind PgBouncer | point lookup 107 µs -> 86 µs (-19%), 50-row list -5% (`postgres.Config.PreparedStatements`) |
| `query` filter compile (4 filters + sort) | 1.7 µs and 23 allocations (was 4.2 µs / 34 / 7.4 KB before the optimisation pass) |
| Whole HTTP middleware stack, per request | 7.0 µs, 58 allocations (was 10.9 µs / 79) including OpenTelemetry |
| JWT verify | HS256 8.5 µs, Ed25519 44.7 µs; sign + refresh pair 11.4 µs |

What keeps it fast: model metadata and CRUD SQL are built once per type; rows are scanned straight into struct
fields; `unnest()` bulk insert is one parse and one round trip; `LIMIT page_size + 1` gives `has_next` for free and
the `count(*)` is skipped when the first page already holds everything (`WithoutTotal()` skips it always); `In` is
one `= ANY($n)` argument however long the list is; relation loading is one query per relation; `SlowQueryThreshold`
logs the statements that are not.

## RabbitMQ (`amqpx`)

```go
mq, _ := amqpx.Connect(ctx, amqpx.Config{URL: os.Getenv("AMQP_URL"), Logger: log})
mq.Declare(func(ch *amqp.Channel) error {                 // runs now and again after every reconnect
    _, err := ch.QueueDeclare("orders", true, false, false, false, amqp.Table{"x-dead-letter-exchange": "dlx"})
    return err
})
err := mq.Publish(ctx, "", "orders", amqpx.Message{Body: b, ContentType: "application/json"}) // nil = broker confirmed
group.Go("orders-consumer", mq.Consume(amqpx.ConsumerConfig{Queue: "orders", Concurrency: 4}, handler))
```

A handler that returns an error rejects the message *without* requeueing (it goes to the dead-letter exchange);
`amqpx.Requeue(err)` puts it back; a panic is a rejection. RabbitMQ 4 only accepts durable (or exclusive) queues.
Delivery is at-least-once, so handlers must be idempotent. Tested against a real broker, including a severed
connection mid-flight.

## Notifications: SMS and email (`notify`)

Use cases depend on one interface, `notify.Notifier` (`SendEmail`, `SendSMS`); the driver and providers are chosen in
the composition root.

```go
smtpSender, _ := smtp.New(smtp.Config{Host: "smtp.example.com", Username: u, Password: p, From: "App <noreply@example.com>"}) // STARTTLS by default
sms, _ := kavenegar.New(kavenegar.Config{APIKey: key, Sender: "10004346"})                                                   // or twilio.New(...)

// sync driver: returns after the provider accepted the message (retries transient failures with backoff)
sender, _ := notify.NewSender(notify.Config{Email: smtpSender, SMS: sms, Logger: log})
var n notify.Notifier = sender

// async driver: returns after the queue took the message; a worker delivers it
q, _ := amqpqueue.New(mq, amqpqueue.Config{Queue: "notifications"})   // durable + <queue>.dlq; or notify.NewMemoryQueue(...)
n = notify.NewAsync(q)
group.Go("notifications", notify.NewWorker(q, sender, log))           // lifecycle.Runner, graceful stop

err := n.SendSMS(ctx, notify.SMS{To: "09123456789", Body: "کد ورود شما: 123456"})
err = n.SendEmail(ctx, notify.Email{To: []string{"ada@example.com"}, Subject: "Welcome", Text: "...", HTML: "<p>...</p>"})
```

* **Both drivers validate first** and report `apperr.KindInvalid` with field errors (header injection, bad address or
  number), so a bad message fails in the request, never in the queue. Rendering templates belongs to the project.
* **Failure model:** providers mark unfixable errors (rejected recipient, bad credentials, no credit) with
  `retry.Permanent`; everything else is retried (`Config.Retry`, default 3 attempts, `AttemptTimeout` 15s). The sender
  returns `delivery_rejected` (KindInvalid) or `delivery_failed` (KindUnavailable). In the async path a job that still
  fails is rejected to `<queue>.dlq`, a poison message too; nothing loops forever. Delivery is at-least-once, so a worker
  crash between the provider call and the ack can send a message twice (`Email.ID` / `SMS.ID` is stable across
  retries for de-duplication downstream). `MemoryQueue` is for dev/tests: it drops on crash and has no DLQ.
* **Tracing:** `notify.send <channel>` (client span) with one `notify.attempt <channel>` child per provider call and a
  `retry` event per backoff; async adds `notify.enqueue` (producer) and `notify.process` (consumer) with the W3C trace
  context carried in the job, so the worker's spans belong to the request's trace. Twilio calls also get an otelhttp span.
* **Metrics** (labels: channel, provider, outcome, nothing per-recipient): `notify.deliveries`, `notify.delivery.duration`,
  `notify.delivery.attempts`, `notify.enqueued`, `notify.jobs.processed`, `notify.queue.wait`, `notify.queue.depth`
  (memory queue). Alert on a rising `outcome=failed` / `rejected` ratio and on `notify.queue.wait` or the `.dlq` depth.
* **Privacy:** logs carry masked recipients (`0912***6789`, `a***@example.com`), message id, provider and attempts, never the
  body (OTP codes, reset links). Spans carry only the recipient count. The Kavenegar key lives in the URL path, so that
  provider strips URLs from errors and has no HTTP client span.
* **Local/tests:** `notify.NewLogSender(log)` logs instead of sending; `notifytest.Recorder` records messages and can inject
  failures. SMTP against a catcher such as Mailpit: `smtp.Config{Host: "mailpit", Port: 1025, TLS: smtp.NoTLS}`.
* Cost: the whole instrumented path adds ~6 µs per message (`BenchmarkSenderOverhead`), noise next to a network call.
  SMTP opens one connection per message; for volume run several workers (`Concurrency`) instead of one big sender.

## Caching and read models (`redisx`)

Caching is opt-in and lives in the **persistence adapter as a decorator** over a repository: use cases, domain and
handlers never know. Cache only the lookups you choose; every other method keeps going straight to the database.
The kit provides the parts that are always the same and carry no business logic; what to cache stays in the project.

| Need | Use |
|---|---|
| Cache one lookup or query result | `redisx.Remember(ctx, cache, key, ttl, loader)` |
| Throw away "everything about X" after a write (no `KEYS` on the shared Redis) | `cache.ScopedKey(ctx, "product", id)` for the key, `cache.Bump(ctx, "product")` after the write |
| Throw away one record | `cache.Delete(ctx, key)` or `readModel.Invalidate(ctx, id)` |
| A purpose-built view (card, summary, page model) served from Redis | `redisx.NewReadModel(...)`: `Get`, `Put` (write-through), `Invalidate`, `GetMany` |
| Counters, leaderboards, "top N" | `client.Hash(...)` (`Incr`), `client.Ranking(...)` (`Add`, `Incr`, `Top`, `Rank`, size cap) |

```go
// persistence adapter: a decorator, the only place that knows about caching
type cachedUsers struct {
    next  user.Repository
    cache *redisx.Cache
}

func (c *cachedUsers) GetByID(ctx context.Context, id uuid.UUID) (user.User, error) {
    return redisx.Remember(ctx, c.cache, c.cache.ScopedKey(ctx, "user", id.String()), 5*time.Minute,
        func(ctx context.Context) (user.User, error) { return c.next.GetByID(ctx, id) },
        redisx.WithNegative(30*time.Second, nil)) // optional: also remember "no such user" briefly
}

func (c *cachedUsers) UpdatePasswordHash(ctx context.Context, id uuid.UUID, h string) error {
    err := c.next.UpdatePasswordHash(ctx, id, h)
    if err == nil {
        _ = c.cache.Bump(ctx, "user") // every cached user is stale now; old keys just expire
    }
    return err
}
// other methods: embed `user.Repository` (or forward to c.next) and they are not cached.

// bootstrap: caching is on only where you wrap, and off entirely when there is no Redis
var users user.Repository = repo
if rc != nil {
    users = &cachedUsers{next: repo, cache: rc.NewCache("users")}
}
```

```go
// A read model: a denormalised view in Redis, rebuilt from the database on a miss
card := redisx.NewReadModel(rc.NewCache("views"), "product-card", time.Hour, rebuildOne)
v, _ := card.Get(ctx, id)                       // Redis first
_ = card.Put(ctx, id, fresh)                    // after a commit: write-through
cards, _ := card.GetMany(ctx, ids, loadMissing) // one MGET + ONE batched query for the misses
```

What `Remember` guarantees: concurrent misses for a key in one process run the loader once (no stampede); stored TTLs
get a random jitter so keys written together do not expire together; Redis errors are logged and the data is loaded
from the source (**a cache outage never fails a request**); a cached value that no longer decodes after a deploy is
reloaded and overwritten; loader errors are never cached; "not found" is cached only with `WithNegative`.

Rules that keep it correct on the shared Redis: every key has a TTL (enforced); the cache is `volatile-lru` and
shared, so **everything cached must be rebuildable from the database**, a read model is never the source of truth;
read models are eventually consistent, so money, stock and anything that must be exact read the database;
never cache a response that depends on the caller under a key that does not contain the caller; to bound
staleness across instances after `Bump`, the version is re-read at most every `WithVersionCache` (default 1 s).
Prefer `Bump` when writes are rare and reads many, per-record `Invalidate` when writes are frequent.

## Revocable tokens

Stateless by default (a token is valid until it expires). Give `auth.JWTConfig.Revocations` a
`redisx.Revocations` and `Verify` also asks Redis (one `MGET`): `Revoke` kills one token until its natural expiry,
`RevokeSubject` kills everything issued to a user up to now ("log out everywhere"). Every key has a TTL, so the set
never outgrows the tokens that could still be presented. If Redis is down the request fails closed (503). The generated
project enables this automatically when `REDIS_URL` is set and then exposes `POST /v1/auth/logout`.

## Auth in one paragraph

Tokens are stateless: validity = signature + issuer + audience + type + expiry. Access tokens default to 15 min and carry
roles; refresh tokens (30 d) carry identity only, so the role is re-read when they are exchanged. Because nothing is stored,
a token cannot be revoked early: keep access tokens short. Every token has a `jti` so a deny-list can be added later.
Rotate keys by putting the new key first in `auth.NewKeySet(new, old)` and dropping the old one after its tokens expire.

## Using it from a project

`make new-go-project` creates a project with `.kit/` (a vendored copy, wired by a `replace` line) so it builds before the
kit is published anywhere. To switch to a published version: push this repo, tag `gokit/v0.1.0` (the module lives in the
`gokit/` subdirectory, so the tag carries that prefix), delete `.kit/` and the `replace` line, and
`go get github.com/PouryDev/server-infrastructure/gokit@v0.1.0` (private repo: set `GOPRIVATE` in the Go container).
`gokit new --kit-mode remote` generates such a project directly.

## Working on the kit

* `go.mod` pins the dependency versions that were downloaded into the shared `gokit-cache` volume; updating one needs
  network access. If the network blocks `proxy.golang.org` downloads (it answers 403 for the zip files without a
  VPN), run the command with `HTTPS_PROXY=http://127.0.0.1:2020 make gokit-test` (any proxy URL works; `localhost`
  proxies automatically use the host network).
* New code needs tests; `make gokit-check` must stay at `0 issues`.
* Not included (by design): rate limiting (nginx does it at the edge), partial / expression indexes and data
  backfills in generated migrations.
