# mizan-ui

The accounting web UI of [`mizan`](../mizan/README.md): RTL, dense, Persian (Vazirmatn, self-hosted), for accountants. React 19 +
TypeScript + Vite; it talks to the real mizan HTTP API on the **same origin** (the dev server forwards `/v1` and `/auth`, so there is no CORS).
Every screen follows the approved Claude Design canvas (45 boards, `ui.css` tokens, light and dark by the same CSS variables).

**Nothing runs outside Docker.** From the repository root:

```bash
make mizan-ui-up        # PostgreSQL 18 + mizan API (migrations, full Iranian chart, sample data) + the UI  ->  http://localhost:5173
make mizan-ui-down      # stop (data is kept); `make mizan-ui-reset` also deletes the database
make mizan-ui-check     # typecheck + eslint + vitest + production build
```

First run: `scripts/mizan-ui.sh up` creates `mizan-ui/.env.dev` (git-ignored) with a random dev password and prints it; the user is `m.ahmadi`.
`npm install` needs the VPN proxy on this network (`HTTPS_PROXY=http://127.0.0.1:2020 make mizan-ui-install`); images come from the arvancloud mirror.
Other commands: `scripts/mizan-ui.sh logs [api|ui|db]`, `scripts/mizan-ui.sh npm <args>`.

## Sign-in (DEV ONLY)

This repository has no identity service, so `mizan/cmd/mizan-dev` brings a stand-in: one user from the environment, gokit's stateless JWT pair
(access 15 min, refresh 30 min, rotated on use), **httpOnly + SameSite=Strict cookies** (the page's JavaScript never sees a token, nothing is in
localStorage) and a gateway that turns the cookie into the `Authorization: Bearer` header `mizan/api` expects. Unsafe requests must carry the
custom header `X-Mizan-UI`. Five wrong passwords lock the account for 15 minutes. A production deployment mounts `api.Mount` in its own service
behind the project's identity provider; replace `/auth/*` (see `src/auth/`) and nothing else in the UI changes.

## Layout (clean architecture, dependencies point down)

```
src/shared/    digits, Jalali dates (Intl-based), idempotency keys, storage      no React, no API
src/api/       one typed module per area + http.ts (CSRF header, 401 -> one silent refresh, Money -> rials, error shape)
src/domain/    business rules as pure functions with tests: journal balance/validation/row shortcuts, account directory, cheque lifecycle
src/ui/        the design system: Button Input Field Select Badge Message Dialog DataTable FormDialog/InlineForm Toast Steps Card Stat ...
src/app/       AppShell (top bar, tabs, status bar), Nav (8 collapsible groups), Fiscal context, route table
src/pages/     one folder per area; components only wire domain + api + ui
```

Rules kept in one place: money is an integer number of rials (never a float; `fmt`/`parseAmount`), dates are typed as Jalali (`1405/08/19`,
Persian digits accepted) and converted at the edge, every money-moving POST carries an `Idempotency-Key` that is reused when the same action is
retried (and renewed when its content changes), API errors become the friendly Persian messages of the design (`api/errors.ts`, code only under "جزئیات").

## Keyboard (handled on the focused element, never globally, `preventDefault` always)

Journal form: `F3` swap debit/credit · `F4` copy the focused field from the row above · `F8` duplicate row · `Ctrl+Insert` add row ·
`Ctrl+Delete` delete row · `Alt+Shift+↓` saved descriptions (`Delete` removes one; kept in this browser). Lists: `Ctrl+F` column-filter row ·
`Ctrl+M` multi-select · `Ctrl+H` who created the record. Only these Sepidar keys are used; save/new-document keys are unverified and not bound.

## Not available (no backing API in mizan, rendered disabled or with a clear note, never faked)

See "Screens without API" in the root `CRITICAL_REMAINING.md` section **UI** (kept next to the module's other limits).
