# mizan-host

A real host application whose only purpose is to run **mizan** (the financial module of the infrastructure repo) so that mizan and its UI (`mizan-ui/`) can be
tested end to end. mizan has no authentication of its own; this project brings it, on gokit's JWT, and mounts mizan's routes behind it.
Scaffolded with `make new-go-project` (clean architecture, gokit vendored in `.kit/`), plus a vendored copy of mizan in `.mizan/`. Everything runs in Docker.

## Run it (from the infrastructure repo)

```bash
make mizan-host-up          # PostgreSQL 18 + migrations (host + mizan) + API + the UI  ->  http://localhost:8090
make mizan-host-user-create USERNAME=sara ROLE=finance NAME="Sara"      # USER_PASSWORD=... to choose the password, else a random one is printed once
make mizan-host-seed        # sample data again (the first `up` already loads it; skipped when journals exist)
make mizan-host-down        # stop, keep data         (make mizan-host-reset deletes the database)
make mizan-host-check       # gofmt, vet, golangci-lint, unit tests
make mizan-host-integration # throw-away PostgreSQL: login, JWT rules, journal through the mounted API
```

The first `up` creates the user `admin` with a random password, prints it once and stores it in `.admin-password` (git-ignored, mode 600). Nothing is hard-coded.
From this directory the same things are `make stack-up`, `make user-create USERNAME=...`, `make test`, `make test-integration`, `make lint` (see `make help`).
`make up` is the hot-reload development stack (compose.dev.yaml, `go run`, with Redis).

## URLs

| | |
|---|---|
| UI | `GET /` (the built mizan-ui; client routes fall back to index.html) |
| mizan API | `/v1/...` (mizan's prefix) **and** `/accounting/...` (alias of `/v1/accounting/...`) |
| sign-in page data | `GET /auth/options` (company + fiscal years, public) |
| browser auth | `POST /auth/login`, `POST /auth/refresh`, `POST /auth/logout`, `GET /auth/me` |
| API clients | `POST /auth/token` `{username,password}` -> `{user, tokens}`; `POST /auth/token/refresh` `{refresh_token}`; then `Authorization: Bearer <access_token>` |
| probes | `/healthz`, `/readyz` |

```bash
TOKEN=$(curl -s localhost:8090/auth/token -H 'Content-Type: application/json' -d '{"username":"admin","password":"..."}' | jq -r .tokens.access_token)
curl -s localhost:8090/accounting/journals -H "Authorization: Bearer $TOKEN"
```
Money-moving POSTs need an `Idempotency-Key` header (mizan's rule).

## Authentication design

* Stateless JWT pairs from `auth.JWT.IssuePair` (HS256 secret >= 32 chars `JWT_SECRET`, or Ed25519 `JWT_PRIVATE_KEY`; `make jwt-key`). Access 15 min, refresh 30 days
  (`JWT_ACCESS_TTL`, `JWT_REFRESH_TTL`). mizan's routes use `httpx.Authenticate(verifier)` (access tokens of this issuer/audience only) and `httpx.RequireRoles("finance","admin")`:
  a refresh token, an expired or foreign token is 401; a user with role `user` is 403.
* `users` table (host migration, goose table `goose_db_version`; mizan keeps its own table), argon2id hashes (upgraded on login when the parameters change), role per user.
  `POST /auth/refresh` and `/auth/token/refresh` read the user again, so a changed role takes effect and a deleted user cannot refresh.
* Login throttling: 5 wrong passwords lock the user for 15 minutes (stored in the database, counted atomically; the right password is refused during the lock).
  There is no per-IP limit here (nginx does that at the edge in the shared infrastructure).
* Browsers: tokens are **httpOnly, SameSite=Strict** cookies (`mz_at` on `/`, `mz_rt` on `/auth`), never in the response body or storage. A same-origin gateway
  (`internal/adapter/httpapi/gateway.go`) turns the access cookie into `Authorization: Bearer`, and every unsafe cookie request must carry the header `X-Mizan-UI`
  (CSRF guard). Requests with their own `Authorization` header (scripts) need neither.
* Optional Redis (`REDIS_URL` + `REDIS_KEY_PREFIX`) makes logout revoke the tokens; without it logout drops the cookies and tokens simply expire.
* Users: `cmd/user` (`create`, `set-password`, `list`). There is no sign-up and no user-management API.

## Layout

`cmd/{api,migrate,user}`, `internal/domain/user`, `internal/usecase/{account,users}`, `internal/adapter/{httpapi,postgres,mizanmod}`, `internal/bootstrap`, `internal/config`,
`migrations/`. `adapter/mizanmod` is the only package that imports mizan (enforced by `internal/architecture_test.go`).

## Updating the vendored copies

`make mizan-host-sync` (from the infrastructure repo) refreshes `.kit/` and `.mizan/` and runs `go mod tidy`. `web/` is the built UI (`make mizan-host-ui`).

## Not built (on purpose)

Sign-up, password reset, user-management API/UI, per-IP rate limiting, MFA, multi-company (one book, one company name from `COMPANY_NAME`), Moadian/gateway
configuration. `deploy/compose.yaml` is the untouched scaffold for the shared infrastructure; it has not been adapted for mizan-host (it would need `WEB_DIR=/web`, `COMPANY_NAME` and a `/user create` step). Plain-http local runs set
`COOKIE_SECURE=false`; behind TLS leave it on.
