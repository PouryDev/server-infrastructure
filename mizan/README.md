# mizan (میزان)

The financial module of my projects: double-entry accounting that follows Iranian conventions, online payments
through gateways, invoices with VAT, wallets, reconciliation, a transactional outbox and an audit log. It is a
Go library on top of `../gokit` (PostgreSQL, httpx, amqpx, apperr, query, retry); projects embed it and get the
accounting system and the money APIs without writing them again.

**Nothing runs outside Docker.** From the repository root:

```bash
make mizan-check        # gofmt + vet + golangci-lint + unit tests
make mizan-integration  # every test, against a throw-away PostgreSQL
```

## Why one module and many packages

Everything lives in one Go module (one version, one migration set, one `mizan.New`), but each bounded context is
its own package and may not import another. `architecture_test.go` fails the build when it does.

```
money   jalali   trail   books          value types and the two narrow ports contexts use
ledger  ─ pgledger                      accounting core (the only package that knows journals)
payment ─ pgpayment, gateway/{zarinpal,idpay,gatewaytest}
wallet  ─ pgwallet          invoice ─ pginvoice          reconcile ─ pgreconcile
outbox (+amqpout)  audit  idempotency   platform: events, audit trail, replay of HTTP commands
mizan                                   composition root: wiring, settlements, cross-context use cases
mizan/api                               HTTP adapter (gokit httpx)
```

The rule that keeps it honest: **payments, wallets and invoices never talk to the ledger.** They post through
`books.Poster`; `mizan/books_adapter.go` turns that into `ledger.Draft`. A payment does not know what it is for:
when it is paid it calls the `payment.Settlement` registered for its purpose (`invoice`, `wallet_topup`, or your
own), inside the same transaction. Payroll, treasury, subscriptions and payouts are added the same way, as new
packages, without touching these.

## Using it

```go
db, _ := postgres.Connect(ctx, postgres.Config{URL: dsn})
_, _ = mizan.Migrate(ctx, dsn)                       // own goose table: never collides with your migrations

zp, _ := zarinpal.New(zarinpal.Config{MerchantID: id})
m, _ := mizan.New(ctx, db, mizan.Config{
    IranianCoding: true,                             // optional chart of accounts, see below
    VATBasisPoints: 1000,                            // 10%: it is law, keep it in config
    Payment: payment.Config{CallbackBaseURL: "https://api.example.com/v1/gateway-callbacks", AllowedReturnHosts: []string{"shop.example.com"}},
}, mizan.WithGateway(zp))

api.Mount(router, m, api.Options{Authenticate: httpx.Authenticate(verifier)})   // finance role, /me/* for customers
group.Go("outbox", m.OutboxRelay(publisher))         // payment.paid, invoice.issued, wallet.credited ... to RabbitMQ
group.Go("payments", m.PaymentSweeper())             // recovers lost callbacks, expires abandoned payments
```

## What is guaranteed (and where it is tested)

| Guarantee | How |
|---|---|
| Journals are append-only | `UPDATE`/`DELETE`/`TRUNCATE` trigger on `mz_journals` and `mz_journal_lines`; corrections are reversing journals (unique `reverses_id`: one reversal per journal) |
| Every journal balances | deferred constraint trigger, so even raw SQL cannot commit an unbalanced one |
| Document numbers are gap-free per fiscal year | counter row locked as the last step of the posting transaction; rolled-back postings give the number back (tested with 30 concurrent posts, 6 failing) |
| Retrying never books twice | idempotency key per journal with a content fingerprint (same key + different content is a conflict) |
| Periods can be closed | posting into a closed month is refused; reversals are dated today |
| Wallets cannot overdraw | the wallet *is* a non-negative ledger account, enforced under a row lock in the posting transaction |
| A callback is never trusted | the raw event is stored, then the **gateway is asked** (`Verify`); amount mismatches are not applied |
| Duplicate callbacks settle once | row lock + state machine (10 parallel callbacks → one settlement) |
| Lost callbacks are recovered | `Sweep` verifies open payments; paid ones are settled, unpaid expired ones expire |
| Events are never lost or invented | transactional outbox, single ordered relay, a failing event blocks later ones (nothing overtakes it) |

Money is `int64` rials (`money.Money`, never a float), VAT rounds half-up in integer arithmetic, dates are
Gregorian in storage and Jalali in the API (`date_jalali`, and every date parameter accepts `1405/08/18`).
The fiscal year is the Jalali year with 12 monthly periods.

## Accounting coding (کدینگ) is optional

Two optional starting charts exist: the small template `ledger.IranianCoding` (`Config.IranianCoding`, `POST /accounting/coding/iranian`) and the large
`ledger.IranianCodingFull` (`Config.IranianCodingFull`, `POST /accounting/coding/iranian-full`): 12 groups, 60 general and 290 subsidiary accounts in the 2/4/6-digit
layout, with memorandum accounts, overhead absorption and variances. **No official or mandatory Iranian chart was found**; sources number groups differently.
The large one is transcribed from one published sample (https://javadhaseb.blogfa.com/post/51, a software vendor's training blog, 1388, production-oriented),
cross-checked only against the group skeleton of https://www.dadehara.com (a partial sample); four evident spelling errors were fixed. It is a sample, not a legal standard: an
accountant should adapt it. Choose one chart; system accounts are mapped to the chosen one (for example bank to 111005, retained earnings to 311301, the profit-and-loss
summary to 311401 «سود (زیان) جاری»; VAT payable has no line in this chart and is mapped to the general account 2112, wallets to 211409: suggestions to review).

Every account may carry a code of the chart of accounts. Without a chart the system works exactly the same:
`GET /accounting/trial-balance?by=coding` simply lists the accounts as `unclassified`. Enable the template with
`Config.IranianCoding` or `POST /accounting/coding/iranian`; it maps the system accounts and all wallet accounts.
Edit it freely: it is a template, not a legal standard.

## HTTP API (prefix `/v1`)

Public: `GET|POST /gateway-callbacks/{gateway}`. Customer (`/me`, subject of the token only): `wallet`,
`wallet/statement`, `wallet/topups`, `invoices`, `invoices/{id}`, `invoices/{id}/payments`, `invoices/{id}/pay-from-wallet`.
Finance role: `accounting/{journals,trial-balance,accounts,fiscal-years,periods,coding}`, `payments` (+ `refunds`, `sweep`,
`unsettled`), `invoices` (+ `issue`, `void`), `wallets` (+ `credit`, `debit`, `status`, `topups`), `wallet-transfers`,
`reconciliations` (JSON or CSV statement), `disputes`, `audit/{type}/{id}`, and treasury: `bank-accounts` (+ `statements`, `auto-match`),
`cheques` (+ `deposit`, `clear`, `bounce`, `return`), `statement-lines` (+ `suggestions`, `match`), `gateway-settlements`, `treasury/position`, and payroll: `payroll/{employees,contracts,rule-versions,payslips,runs,loans,remittances}`. Money-moving POSTs require `Idempotency-Key`.
See `mizan/api/*.go`; list endpoints use gokit's `filter[...]`, `sort`, `page` query syntax.

## Refunds

`POST /payments/{id}/refunds` records a refund with a `method`. A gateway that implements `payment.Refunder` refunds
through its API (`gateway`, books: `bank.gateway_clearing`). A gateway without one, or one answering `ErrNotSupported`,
makes it `manual_card_to_card`: a human agent transfers the money card-to-card from the company's bank account, outside
the system, and records it.

```
pending ──claim──► processing ──complete──► done        books and payment move here, and only here
   ▲                  │  │
   └──────release─────┘  └──fail──► failed      cancel (pending or processing) ──► cancelled
```

- **Claim** (`POST /refunds/{id}/claim`): the authenticated finance user becomes the agent; one wins under the row lock,
  the same agent claiming again is a no-op. The wallet/invoice check runs again, so nobody is sent to pay money a spent
  wallet top-up can no longer return. **Release** hands it back (the claimer; `{"force": true}` for a supervisor).
- **Complete** (`POST /refunds/{id}/complete`, `Idempotency-Key` required, only by the claimer): body
  `tracking_number` (the bank's شماره پیگیری, 4-40 characters), `paid_at` (RFC 3339, or a date, Gregorian or Jalali, meaning
  its start), `destination_card_masked` (16 characters, at most the first 6 and last 4 digits: `603799******1234`) and an optional
  `note`. Persian digits are accepted. Repeating it with the same tracking number is a no-op; the same tracking number on
  another refund is a `409` (unique `(method, tracking_number)`).
- **Fail** (`/fail`, only from processing) and **cancel** (`/cancel`) take a `reason`, need an `Idempotency-Key`, and release the
  reserved amount. Pending and processing refunds reserve their amount against the payment (and, together, against the
  wallet or invoice).
- **Queue**: `GET /refunds?filter[method]=manual_card_to_card&filter[status][in]=pending,processing` (oldest first;
  also `filter[claimed_by]`, `filter[payment_id]`, `sort`, `page`). `GET /refunds/{id}`.
- **Accounting**: a manual refund credits `bank.main`, a gateway refund `bank.gateway_clearing` (the settlement reads
  `Refund.Method`). Nothing is booked at request or claim.
- **Events** (outbox): `refund.manual_requested`, `refund.claimed`, `refund.released`, `payment.refunded`, `refund.failed`
  (also for cancel, `status` says which). **Audit** (`GET /audit/refund/{id}`): every step with the agent id.
- **No card data**: the system never receives, stores or logs a full card number or CVV. The agent types the card in the bank's
  app; the masked form is enforced in code and by a `CHECK` constraint. A note, reason or tracking number that looks like a
  card number (13-19 digits passing the Luhn check) is refused.

## Treasury (خزانه‌داری)

Package `treasury`; it posts through `books.Poster` like the others and knows no other context.

- **Bank accounts** (`POST /bank-accounts`): each is an asset account of the ledger (`ledger_code`; the system `bank.main` is registered
  at start-up). The IBAN (شبا) is optional and checked (`IR` + 24 digits, ISO 13616 mod 97). `GET /treasury/position` is the cash
  position: bank balances, cheques not yet cleared (in and out), gateway money not yet paid out, and the projected total.
- **Cheques** (`POST /cheques`, `Idempotency-Key`): the Sayad id (شناسه صیادی, 16 digits) is optional and unique per direction; only its
  shape is checked, no checksum is invented. Lifecycle and books:

  | received (دریافتنی) | books |
  |---|---|
  | register → `in_hand` | Dr `cheques.receivable_in_hand` / Cr customer receivable (`receivable.customers`) |
  | `deposit` → `deposited` | Dr `cheques.receivable_in_collection` / Cr in hand |
  | `clear` → `cleared` | Dr the bank account / Cr in collection |
  | `bounce` → `bounced` (+ optional bank fee) | Dr customer receivable / Cr in collection; fee: Dr `expense.bank_fees` / Cr bank |
  | `return` (only from in hand) → `returned` | reversal of the receipt |

  An **issued** cheque (پرداختنی, drawn on one of our accounts): register Dr supplier payable (`payable.suppliers`) / Cr `cheques.payable`;
  `clear` Dr `cheques.payable` / Cr bank; `bounce` only records the status and the fee (we still owe the supplier), and it can still
  `clear` later; `return` reverses the issue. Every step is idempotent, takes the cheque's row lock (a cheque clears or bounces, never both),
  is audited with the actor and announced (`treasury.cheque.<status>`). **Verify the bounce entry with your accountant**: the
  sources found describe receipt, deposit and clearing; the entry of a bounce (back to the customer's receivable) is the usual
  convention, not a quoted standard.
- **Gateway payouts** (`POST /gateway-settlements`): Dr bank (net) / Dr `expense.gateway_fees` (fee) / Cr `bank.gateway_clearing` (gross),
  idempotent per `(gateway, ref)`; a payout larger than the clearing balance is refused (a payment is missing from the books).
  The fee is booked as given: whether the gateway's invoice carries VAT is not modelled.
- **Bank statements** (`POST /bank-accounts/{id}/statements`, CSV or JSON): CSV columns `date, description, reference, balance` and either
  `amount` (signed) or `credit` + `debit`; dates Gregorian or Jalali, Persian digits and thousand separators accepted. Banks export
  differently, so convert a bank's file to this shape. Importing again, or an overlapping statement, adds only new lines.
  **Matching:** `GET /statement-lines/{id}/suggestions` lists cheques and payouts with exactly the same amount; one whose Sayad id or payout
  reference is in the line's text is `high_confidence`. `POST /bank-accounts/{id}/auto-match` applies only a single high-confidence
  candidate (the amount alone never moves money); everything else is `POST /statement-lines/{id}/match` with
  `kind` = `cheque` (clears it on the line's date), `settlement` (confirms a payout), `fee` (books a bank charge) or `ignored` (needs a note).

## Payroll (حقوق و دستمزد)

Package `payroll` (+ `pgpayroll`), `m.Payroll`, API under `/v1/payroll`. Employees, contracts (monthly base, allowances, insured or not,
start/end; contracts of an employee never overlap), monthly payslips by **Jalali** year/month, and a payment run.

**Legal numbers are data, never code.** A *rule version* (`mz_pay_rule_versions`, `POST /payroll/rule-versions`) holds: employee, employer and
unemployment shares (basis points), the insurable pay ceiling and floor **per worked day** (so 30/31-day and part months need no special case),
whether the employee's share is deducted from taxable income, and the progressive tax brackets (monthly amounts, the first 0% bracket is the
exemption; the last bracket has no limit). A version is valid from its `effective_from` (Gregorian or Jalali) until the next one; the month's
rules are those in force on the month's first day. Versions are immutable: a change is a new version; an unused one can be deleted
(`DELETE /payroll/rule-versions/{id}`), a used one cannot. Each payslip stores its figures and the version it used, so old months never change.
`GET /payroll/rule-versions/at?date=` shows what applies on a day. Allowances carry `taxable` and `insurable` flags the accountant sets per item.

**Calculation** (`payroll.Calculate`, pure, unit tested): pay of a part month is prorated by days (contract start/end or an explicit `days`);
`extras` (bonus, overtime) are paid in full. Insurable base = sum of insurable items clamped to `[floor, ceiling] x worked days`; shares round half up.
Taxable = taxable items - employee share (if the rule says so); tax is the progressive sum rounded once. Net = gross - employee share - tax.

**Books.** `POST /payroll/payslips` (idempotent per employee and month, a retry returns the same payslip; a month that has not started is refused):
Dr `payroll.salary_expense` (gross), Dr `payroll.employer_insurance_expense` (employer + unemployment shares); Cr `payroll.salaries_payable` (net),
Cr `payroll.tax_payable`, Cr `payroll.insurance_payable` (all three shares). Dated at the month's last day, or today if the month is not over. Coding:
70201 / 70201 / 30401 / 30302 / 30303 (the template has no separate employer-insurance expense code). `POST /payroll/runs` (needs `Idempotency-Key`) pays the
issued payslips of a month, or the listed ones, from a bank account: Dr salaries payable, Cr the bank; payslips are row-locked so concurrent runs pay each once.
`POST /payroll/payslips/{id}/void` reverses an issued payslip with a reversing journal (then the month can be issued again); a paid one cannot be voided.
`POST /payroll/payslips/preview` computes without booking.

**Starting data (migration 00007) is NOT verified against official texts.** It is the 1405 version compiled on 2026-11-09 from secondary sites (no ssa.ir,
nta.ir or mol.ir page was reachable through search); the user confirmed the numbers, but confirm them against the official circulars and add a corrected version if they differ:
daily minimum wage 5,541,850 rials (+60%, Supreme Labour Council, 24 Esfand 1404); insurance ceiling = 7 x that per day (1,163,788,500 for a 30-day month);
floor = the daily minimum wage; employee 7%, employer 20%, unemployment 3% (paid by the employer); tax exemption 40M toman/month (400M rials), then 10/15/20/25/30%
on 40-80, 80-100, 100-120, 120-140, above 140M toman. Sources: sepidarsystem.com, shahravand.ir, mohaseban.org, kartaban.com, mozdban.com, nabzgheymat.ir.
**Overtime, holiday and night work, unused-leave payout, severance** are paid from the rule version's `terms` (data, like the rest): hours per day (x100),
days per month, a multiplier per component and whether each is taxable / insurable, and severance days per year with a minimum service. A payslip request
carries `hours` (`overtime_minutes`, `holiday_minutes`, `night_minutes`), `leave_days` and `severance: true` (the contract must end in that month; service runs
from the employee's first contract, a year is 365 days). The wage they are computed on is the base salary plus the allowances flagged `wage_base`. A version
whose terms are empty refuses to pay them (`terms_not_configured`) rather than guess. Starting data (UNVERIFIED, from secondary sites): overtime and holiday work 140%
of the hourly wage (art. 59), night premium 35%, hourly wage = daily wage / 7.33, a month = 30 days, severance 30 days per year from 1 year of service, not taxable or
insurable at the end of service. Sources disagree on the **base** of overtime (some include only base wage and seniority, welfare items such as housing excluded: hence the flag)
and on the **tax of the leave payout** (seeded as taxable, not insurable; confirm it and add a version if you disagree). Sources: sepidarsystem.com, mehrnews.com, bidbarg.net,
bimeh.com, ravihesab.com, dingontime.com, pooyesh.ir, accamj.com.

**Loans and advances** (`POST /employees/{id}/loans`, `Idempotency-Key`): Dr `payroll.employee_loans` (coding 10203), Cr the bank. From the start month each payslip deducts the
installment (the last one is what remains) after tax and never below a zero net: Cr `payroll.employee_loans`, so net pay falls and the receivable shrinks; the loan closes at zero.
Voiding a payslip gives its installment back (the reversing journal restores the receivable).

**Remitting withheld tax and social security** (`POST /payroll/remittances`, `Idempotency-Key`, `kind` = `tax` or `insurance`, optional Jalali `period_*` and the authority's
`reference`): Dr tax / insurance payable, Cr the bank account. It cannot pay more than the payable account holds; payments of one kind are serialised (advisory lock), so concurrent
requests never overpay.

**Not built:** the authorities' file formats. The tax office says its upload needs "the format in its electronic-file guide" (tax.gov.ir/action/do/show/16) and the social-security list
is a DBF pair (DSKKAR00 workshop / DSKWOR00 insured persons), but no official field specification was reachable, so no exporter exists: it needs the official guides from you.
Also not built: two contracts in one month (refused; make changes start on the first day of a Jalali month), cancelling or writing off a loan, overtime limits (4 h/day) and holiday
calendars, prorated yearly bonus (عیدی).

## Marketplace payout (تسویه با فروشندگان)

Package `payout` (+ `pgpayout`), `m.Payout`, API under `/v1/payout`, migration `00009`. No law is involved here: the commission and the
hold period are the platform's own business terms, so nothing in this section needs an official source. They are still data, not code.

- **Seller**: a code, a name, an optional IBAN (شبا, shape and ISO 13616 check digits are validated) and an account `payout.seller.<id>`, a liability that cannot go
  negative (so a seller is never paid more than is owed) and is mapped to chart code 30101 (trade payables) when the Iranian chart is on.
- **Terms (versioned data)**: `POST /payout/terms` adds a version `{scope: default|category|seller, scope_key, effective_from (Gregorian or Jalali), rate_bp, vat_bp, hold_days}`.
  A seller's terms beat the category's, which beat the default; inside a scope the latest version started on or before the sale date applies. Versions are never edited;
  an unused one can be deleted, one a sale used cannot. `GET /payout/terms/at?seller_id=&category=&date=` shows what a sale would get. A starting default is **seeded** by the owner's choice (not a legal figure): 10% commission, 7 days hold, VAT follows the accounting setting, from 1405/01/01;
  add a newer version to change it. Sales dated before the first version answer 409 `no_terms`.
- **Sale** (`POST /payout/sales`, `Idempotency-Key`): `Dr receivable.customers (gross)`, `Cr payout.seller.<id> (gross - commission - VAT)`, `Cr revenue.marketplace_commission`
  (coding 60101), `Cr vat.output` (only when the terms' `vat_bp` > 0). Commission = gross x rate rounded half up; the seller gets the remainder. The rate and hold days are copied onto the sale,
  so later term changes never touch it. Same key and same data returns the same sale; same key and different data is 409.
- **VAT on the commission**: the tax base is the commission, not the sale. The rate follows the accounting setting (`mizan.Config.VATBasisPoints`, default 1000 = 10%, the same one invoices use),
  unless the terms set their own `vat_bp` (nullable; `0` = exempt). The VAT is rounded half up and added on top of the commission, as on any invoice to the buyer of a service (the seller), so the
  seller receives gross - commission - VAT; the VAT on the goods themselves is the seller's invoice and is inside `gross`. A commission and VAT larger than the sale is refused (409 `fees_exceed_gross`).
  Sources (secondary; none is the statute text): the general rate is 10% in the 1405 reports ([asriran](https://www.asriran.com/fa/news/1018203/%DB%B1%DB%B0-%D8%AF%D8%B1%D8%B5%D8%AF-%D9%85%DB%8C%D8%B2%D8%A7%D9%86-%D9%85%D8%A7%D9%84%DB%8C%D8%A7%D8%AA-%D8%A8%D8%B1-%D8%A7%D8%B1%D8%B2%D8%B4-%D8%A7%D9%81%D8%B2%D9%88%D8%AF%D9%87-%D8%AF%D8%B1-%D8%B3%D8%A7%D9%84-%D8%A2%DB%8C%D9%86%D8%AF%D9%87));
  services are within the VAT law ([rc.majlis.ir](https://rc.majlis.ir/fa/law/show/134203), 1387 text; the 1400 law was not checked); commission earners pay VAT on the commission
  ([hamyarantax](https://hamyarantax.com/value-added-tax/)); the base is the commission ([ekhtebar](https://www.ekhtebar.ir/%D9%86%D8%AD%D9%88%D9%87-%D8%AD%D8%B3%D8%A7%D8%A8%D8%B1%D8%B3%DB%8C-%D9%BE%D8%B1%D9%88%D9%86%D8%AF%D9%87-%D9%85%D8%A7%D9%84%DB%8C%D8%A7%D8%AA%DB%8C-%D9%85%D8%B4%D8%A7%D8%BA%D9%84-%D9%86%D9%85%D8%A7/)).
  The owner states VAT is always 10% unless changed in the accounting settings. **Unverified:** the e-invoice for the commission (Moadian adapter not built yet).
- **Hold and release**: release day = delivery day + hold days. A sale with no delivery date waits (`awaiting_delivery`); `POST /payout/sales/{id}/deliver` sets it
  (today when empty, never in the future, never before the sale). `phase` is derived: `awaiting_delivery`, `held`, `available`, `paid`, `returned`. Finance can release a
  delivered sale early (`/release`); an undelivered one never.
- **Payout batch** (`POST /payout/batches`, `Idempotency-Key`, optional `bank_account_id`, `date`, `seller_ids`): locks (`FOR UPDATE`) every held, released sale of an active
  seller, books ONE journal (`Dr` each seller's account, `Cr` the bank) and returns who gets how much and to which IBAN (the IBAN is snapshotted). `GET /payout/batches/{id}/csv` exports it as plain CSV (seller_code, seller_name, sheba, amount, currency, sale_count) for any bank tool or spreadsheet. Nothing due is 409 `nothing_to_pay`
  (and leaves no trace). Concurrent batches never pay a sale twice; the same key returns the stored batch. Sales whose seller share is zero (100% commission) are never payable.
- **Return** (`POST /payout/sales/{id}/return`, reason required): reversing journal, status `returned`; idempotent; refused with 409 `sale_paid_out` once the seller was paid.
- **Block** (`/sellers/{id}/block` with a reason, `/unblock`): withholds payouts only. Sales are still booked and returns still work, so the books keep telling the truth.
- `GET /payout/sellers/{id}/balance`: the ledger balance plus awaiting-delivery, held, available and paid-out totals.

Verified by tests (integration on PostgreSQL): split and rounding, idempotency and key reuse, chart mapping and no-chart operation, terms precedence and snapshots, hold/release,
payout totals and ledger effect, return, block, 100%/0% commission, concurrent batches (exactly one wins), concurrent retries of one key, concurrent return vs payout (never both), HTTP API.
**Not verified:** against a real bank transfer. The commission is revenue 60101 (never "other revenue" 60201, by the owner's decision).

**Not built (ask if you need them):** the Moadian e-invoice for the commission, partial returns, a bank-rejected transfer (undoing one seller's part of a
batch), clawback after payout (negative seller balance), a file in a specific bank's own format (the CSV is generic; no bank spec was supplied),
payment of the customer's side (the sale debits `receivable.customers`; settle it with the existing receipt flows, and do not also issue an invoice for the same order or revenue is counted twice).

## E-invoice (سامانه مودیان)

Package `einvoice` (+ `pgeinvoice`, `moadian`, `einvoicetest`), `m.EInvoice`, API under `/v1/einvoices` and `/v1/einvoice-requests/invoice/{id}`, migration `00010`.
The context is **vendor neutral**: invoices are queued as a neutral `einvoice.Invoice`, a `Provider` sends them and reports verdicts, and the root maps `invoice.Invoice` into the neutral
form (`m.EnqueueInvoice`, idempotent: an invoice has one e-invoice). Statuses: `queued -> sent -> success | failed` (`failed` can be re-queued under a new trace id).
`m.EInvoiceSender()` is the background runner (send + follow verdicts every minute); `POST /einvoices/send` and `/einvoices/refresh` run one round on demand. Concurrent senders
claim documents with `FOR UPDATE SKIP LOCKED`, so an invoice is handed to the provider once; a failed provider call keeps documents queued with the error and attempt count.
Without a provider (`mizan.WithEInvoice(provider, fiscalID, sellerParty)`) invoices only queue.

`einvoice/moadian` follows the official document **RC_TICS.IS_v1.3, "دستورالعمل فنی اتصال به سامانه مودیان", Mehr 1402** (read in full, 46 pages; it states that it describes the second
version of the APIs, `https://tp.tax.gov.ir/requestsmanager/api/v2`): nonce -> JWS token (RS256, header alg/x5c/sigT/typ/crit/cty), server-information -> invoice signed as JWS and
encrypted as JWE (RSA-OAEP-256 + A256GCM, AAD = encoded header), POST `/invoice` (up to 1000 per call), inquiry by reference number (up to 100, window at most a week, +03:30 times),
fiscal-information and taxpayer lookups, and the documented error codes as `APIError`. The signing key and certificate (`NewKeySigner(certPEM, keyPEM)`) and the client id come from your
deployment's secrets; nothing secret is in the repository (tests generate throw-away keys). The base URL is configurable because the document names no sandbox.

**Verified:** the code matches the document's described protocol, tested against a strict fake server that checks what the document says the real one checks (JWS header and signature,
one-time nonce, key/IV/tag sizes, AAD, JWE decryption, repeated `referenceIds`, window format), and the JWE header encodes to the document's own Base64URL example.
**Not verified:** never run against the real servers (no certificate, no fiscal-memory id, no sandbox exists yet); the document is of Mehr 1402, newer versions may differ.
**Not built, on purpose:** the invoice JSON itself. The document shows a sample (`taxid`, `inno`, `indatim`, `inty`, `inp`, `ins`, `tins`, `tob`, `bid`, `tprdis`, `tdis`, `tadis`, `tvam`, `todam`, `tbill`,
`setm`; body `sstid`, `sstt`, `mu`, `am`, `fee`, `prdis`, `dis`, `adis`, `vra`, `vam`, `tsstam`) but not what the fields and their codes mean, nor the algorithm of the unique tax number
(`taxid`). It points to two other papers for them: **«دستورالعمل صدور صورتحساب الکترونیکی»** and **«قالب شناسه یکتا حافظه مالیاتی و شماره منحصر به فرد مالیاتی»**. Get them from the tax
authority's portal (the Moadian page of tp.tax.gov.ir / the taxpayer's Karpooshe) or your trusted service provider, then implement `moadian.PayloadBuilder` (one method) from them.
Cancel, correct and return invoices and the sales-threshold handling are not modelled. Commission invoices of the marketplace are not mapped yet (a seller has no national/economic id stored).

## Coupons (کوپن تخفیف)

Package `coupon` (+ `pgcoupon`), `m.Coupons`, API `/v1/coupons`, `/v1/coupon-actions/{preview,redeem}`, `/v1/coupon-redemptions/{id}/release`, migration `00011`.
A coupon is a percentage (`percent_bp`, rounded half up) or a fixed amount (never more than the amount it is applied to) with limits that are all data: valid from/until (inclusive days, Gregorian or
Jalali), a minimum amount, a total redemption limit and a per-customer limit (each optional). Limits can be changed with `PUT /coupons/{id}` (`clear_*` removes one; the total cannot be lowered below the
redemptions in force); the kind and value never change, so a code never changes meaning. Codes are upper-cased.
`redeem` needs an `Idempotency-Key`: the same key returns the same redemption and counts once (the same key with other data is 409). The coupon row is locked (`FOR UPDATE`) while the limits are
checked and the counter is raised, so concurrent redemptions never exceed the total or per-customer limit (tested with 20 and 10 concurrent workers). `release` gives a redemption back (voided invoice,
cancelled order) and frees the limits; it is idempotent. `preview` shows the discount without using the coupon; only `redeem` is binding.

## Subscriptions and recurring billing (اشتراک)

Package `subscription` (+ `pgsubscription`), `m.Subscribe`, API `/v1/subscription-plans`, `/v1/subscriptions`, billing in the root (`m.BillSubscriptions`, runner `m.SubscriptionBiller()`, `POST /subscriptions/bill`).
- **Plan**: price per interval (before VAT), interval `day|month|year` x count (months and years count in the **Jalali** calendar, keeping the day of month, shortened in shorter months: the 31st
  becomes the 30th, every period start is computed from the anchor so nothing drifts), trial days, optional `tax_bp` (null follows the accounting VAT setting, default 10%). A price change applies to
  subscriptions started afterwards; a running subscription keeps the terms it copied at the start.
- **Billing in advance**: a period is billed on its first day. `CreateDuePeriods` creates the periods that have started (unique per subscription and number, `SKIP LOCKED`), then each pending period is
  turned into an invoice in its own transaction under a row lock: `Invoices.Create` under the key `subscription:<id>:period:<n>` + `Issue` (gap-free number, books posted) + mark the period. A retry or
  several concurrent instances therefore never bill a period twice (tested with 6 concurrent runs). A failing period is counted (`attempts`, `last_error`, visible at `GET /subscriptions/{id}/periods`),
  retried up to 10 times and never blocks the others. If the system was down for several periods, the missed ones are created and billed one run after another (catch-up).
- **Trial** (no invoice until it ends), **pause** (no billing; **resume** starts a new period today, the paused time is not made up), **cancel** now or `at_period_end` (the period already billed stands; the
  next run ends the subscription without an invoice).
- **Coupon**: `coupon_code` on a subscription is redeemed when its first period is invoiced and discounts that period only (the discount goes on the invoice line before VAT, so VAT is on the
  discounted price). If the coupon is no longer good at that moment (expired, used up) the customer is billed in full instead of not billed.
Verified by tests (integration on PostgreSQL + unit): Jalali period arithmetic (31st, leap Esfand, year change, re-anchor), billing once per period, concurrent runs, trial/pause/resume/cancel, price
snapshot, coupon on the first invoice, VAT 10% on the invoice, the HTTP API.
**Not built:** collecting the money automatically (a subscription invoice is a normal invoice; pay it through the existing payment flows or wallets: no card-on-file or auto-charge), dunning (reminders, suspending
an unpaid subscription), proration on plan change (change the plan by cancelling and subscribing), per-period coupons (repeating discounts), usage-based billing, billing in arrears, pushing the invoice to the
customer (use the outbox event `subscription.invoiced`).

## Fiscal-year closing and opening (بستن حساب‌ها، سند اختتامیه و افتتاحیه)

`POST /v1/accounting/fiscal-years/{id}/close` (finance role, `Idempotency-Key` required), preview `GET .../close-preview`, undo
`POST .../reopen`, report `GET /v1/accounting/income-statement` (`?include_closing=true` to show the closing documents). Code: `ledger/yearend.go`
(`PlanYearEnd` is pure and unit-tested), migration `00012_year_closing.sql`. One transaction does all of it, under a lock on the year:

1. **Temporary accounts:** every revenue and expense account with a balance is closed into the temporary account `equity.profit_loss_summary`
   (حساب خلاصه سود و زیان). Journal kind `closing`, dated the last day of the year.
2. **Result:** the summary (profit or loss) moves to retained earnings (`equity.retained_earnings`, coding 50202 «سود (زیان) انباشته» when the Iranian
   chart is on; a plain system account when it is off; `retained_account` in the request picks another equity account).
3. **Balance-sheet accounts (سند اختتامیه):** every asset, liability and equity account with a balance is closed on the side it has (a credit-balance
   asset such as accumulated depreciation is closed with a debit). Kind `closing`.
4. **Opening (سند افتتاحیه):** in the next fiscal year (created if missing) one `opening` journal, dated its first day, is the exact mirror of step 3.
   Revenue and expense start at zero. Because the closing and opening pair cancel, the cumulative balance of every account is always the true balance
   (wallet `non_negative` balances too) and the trial balance stays balanced before and after.
5. All periods and the year are set to `closed` (a database trigger also refuses any journal into a closed year); the result is recorded in
   `mz_year_closings`, an audit entry `fiscal_year.close` and the event `ledger.fiscal_year_closed` are written in the same transaction.

Refused (409 with the list of reasons; `close-preview` returns them as `blockers` without posting): the year is not over yet (today must be after its last
day); an earlier fiscal year is not closed; the next year is closed; the retained-earnings account is missing, not equity or inactive; an account with a
balance is inactive or not in the books' currency (one currency only); more than 100,000 accounts with a balance. Zero balances are skipped. Closing a year with no
revenue, expense or balances posts nothing but still locks it. Closing is idempotent per year: a closed year answers 200 `already_closed` with the same
documents, so concurrent requests produce exactly one set (tested with 6 goroutines).

**Big charts (no line limit):** a document is split once it passes `Config.YearEndChunk` lines (default 3000: the database checks a journal's balance per
inserted line, so a journal's cost grows with the square of its size). Every part is balanced against a transit account that nets to zero over all parts:
`equity.profit_loss_summary` for the temporary accounts, `equity.closing_balance` (تراز اختتامیه) and `equity.opening_balance` (تراز افتتاحیه) for the others
(sources above describe this transit-account variant as a normal practice). A normal-size chart still gets exactly one closing and one opening document.
The result and the API list the documents (`closing`, `opening`, `temporary_closing` are arrays). Reopen reverses every part.

**No approval step:** the preview lets an accountant check everything first, but `close` executes immediately when called (the "auto accept" mode). If you
want a two-person rule (one prepares, another approves), that is a separate workflow that is not built.

**Reports:** closing journals must not distort a year's result, so `TrialBalanceQuery.ExcludeClosing` (and `IncomeStatement`, default) leaves out journals of
kind `closing` and the reversals of them. Use it for the closed year's income statement and final balance sheet.

**Reopen (undo):** only while the next year holds nothing but the opening document and is not closed. It reverses the opening, then the closing
documents, on their own dates (reversing journals only; the originals stay), reopens the year and its last month (reopen earlier months yourself with
`/periods/{id}/open` if the correction needs them). Closing again uses generation 2 (new keys, new documents).

**Verified vs not:**
- Verified (Persian accounting-software and teaching sites, all secondary; no law or standard prescribes the technical sequence): temporary accounts are closed
  through «خلاصه سود و زیان», its result goes to a permanent equity account (سود (زیان) انباشته), permanent accounts are closed in the سند اختتامیه, and the سند افتتاحیه is
  its mirror image with the same balances. Sources: https://www.sepidarsystem.com/blog/closing-document-in-sepidar/ , https://www.sepidarsystem.com/blog/opening-document/ ,
  https://www.mahaksoft.com/104704/closing-document , https://bamdadsoft.com/blog/accounting/closing-account/ , https://sarayehesabdar.ir/closing-the-document/ .
  One source also describes a variant with closing/opening transit accounts (تراز اختتامیه/افتتاحیه); we use the direct mirror, which the sources also describe.
- NOT verified / not built: tax adjustments (تعدیلات مالیاتی, tax expense, legal reserve اندوخته قانونی, dividends) are NOT part of the closing: book them as normal
  journals before closing. The template chart has no code for the summary account, so it is unmapped (none was invented). Accountant must check: the descriptions
  (English), that retained earnings is the account they want, and the order against their audit workflow.

## VAT return (اظهارنامه مالیات بر ارزش افزوده)

Package `vat` (+ `pgvat`, migrations `00016_vat.sql` and `00017_vat_law_data.sql`, API `/v1/vat/*`, `m.VAT`).

### Source of the law and decision log
The CURRENT law is the VAT law approved 1400/03/02 (قانون مالیات بر ارزش افزوده, 57 articles; NOT the 1387 law, whose numbering differs: e.g. input credit is art. 8 now, exemptions art. 9, penalty art. 37).
`rc.majlis.ir` and `intamedia.ir` could not be reached (socket closed), so the consolidated per-article text on **thdorsan.com** (`https://thdorsan.com/law-doc/قانون-مالیات-بر-ارزش-افزوده-ماده-N`, a legal-database site that
shows the amendment history with dates) was read for arts. 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 21, 23, 26, 36, 37. It is a secondary host of the official text: the amendment entries it shows are
cited below; an accountant should compare with the Official Gazette.
- **Rate (art. 7)**: 9% in the law; the budget laws of 1403 (clause 6 ر, 1402/12/21) and 1404 (clause 1 خ, 1403/11/08) each add one point as the state share: total 10%. Verified from the text. **1405**: the budget law text was NOT read; asriran,
  mehrnews, zharfban (secondary) say the proposed 12% was not passed and the rate stays 10%: stored with `verified=false`. Conflict: the older secondary note "9% in 1404" lost to the budget-law text (10%).
- **Municipal levy part**: the text says "tax and levies" (مالیات و عوارض) with a total rate; the budget law says the extra point is the "state share". The split 7% state + 3% levies is from secondary sources only: stored as
  `levy_bp=300`, flagged unverified (the `verified` column describes the rate; the levy value is secondary in every row).
- **Payment/filing deadline (art. 4)**: pay by the END OF THE MONTH AFTER the tax period (the old 1387 "15 days" lost). The period length is not in art. 4; the circulars (200/1404/68 "summer 1404", 200/1405/55) work per season: 3 months.
- **Late penalty (art. 37)**: 2% per month of the unpaid tax and levies from the earlier of the return deadline and the payment due date (with art. 191 of the direct-tax law, not read). How a part of a month counts is not said: every
  started month is counted (`PenaltyEstimate`, `GET /vat/returns/{id}/penalty`), an upper figure; the forgiveness circulars (e.g. 200/1403/501) are not modelled.
- **Art. 36** penalties (10,000,000 rials or twice the unpaid tax for not filing; twice the tax for concealment, three times if repeated within two years) are tax-office assessments: not computed.
- **Input credit (art. 8)** and its notes 1-8; **exemptions (art. 9)** and zero-rate exports (art. 10); art. 13 says until the Moadian system is live the 1387 rules for registration and returns continue.
- **Art. 16 and the old 1387 articles the first version cited (10, 17, 21, 23) were replaced** by the numbering above; the 1387 text had been read from vakilik.com.
- Special rates read (art. 26): petrol/jet fuel 30%, gas oil/kerosene/fuel oil/gas 15%, gold and jewellery fee 9% (10% in 1404 by the budget law), sugary drinks 16% domestic / 36% imported, medicines imported with a shortage 3% (art. 9 note 1).
  Tobacco (art. 26 ت) is NOT seeded (it rises 5 points a year up to caps; the year schedule was not read).

### What exists
- Rules (`POST/GET /vat/rules`): period length, deadline (days or month end), late-penalty bp, `source` mandatory, immutable; migration 00017 seeds the first (effective 1400/03/02: season, month end, 200 bp).
- Rates by category (`GET/POST /vat/rates`, `GET /vat/compute?category=&base=&date=`): versioned, with `verified`, `levy_bp` and the split of the computed tax into state share and levy when known. 8+ rows seeded.
- Exemptions (`GET/POST /vat/exemptions?on=`): art. 9 goods and services as short Persian descriptions (28 rows) + exports; versioned with `effective_to`. They are a reference list: invoices are not classified against it automatically.
- `GET /vat/preview?year=&period=` reads output tax, deductible input tax and the opening settlement balance from the books. `POST /vat/returns` (Idempotency-Key) files a FINISHED period once (advisory lock, concurrency test): Dr `vat.output`, Cr `vat.input`, difference to `vat.settlement`; dated the period's last day.
- **Amended return** (`POST /vat/returns/amend`, Idempotency-Key): whatever was posted inside a filed period after filing (the filing settled the accounts, so the residual IS the difference) is booked as revision n+1 (`delta_output/delta_input`; signed, a credit note reverses); the return rows keep cumulative figures; 4 concurrent amenders yield one. 409 `not_filed` / `nothing_to_amend`.
- **Apportionment of input tax** (`POST/GET /vat/apportionments`, before filing, once per period): art. 8 notes 2-5. The user classifies the period's input tax into taxable-only, exempt-only, production-line machinery (always deductible, note 4) and shared; the shared part is split by the taxable/exempt TURNOVER ratio (rounded down for the deductible part). The law gives no formula for shared inputs: **the turnover ratio is this module's assumption**. The non-deductible part is booked Dr `expense.vat_nondeductible`, Cr `vat.input` (note 5: an accepted expense); the parts must add up to the period's input tax (409 `input_mismatch`).
- `GET /vat/position`, `POST /vat/payments`, `POST /vat/refunds` as before (never above what is owed / the credit held). Input VAT on inventory receipts and asset acquisitions goes to `vat.input` as before.
- NOT built: imports and customs, tobacco rates, per-branch returns (the 1400 text registers taxpayers in the Moadian system; no per-branch return found), the Moadian monthly-summary filing, and the electronic filing itself; invoices are not matched to the exemption list.

## Corporate income tax (مالیات بر درآمد اشخاص حقوقی)

Package `incometax` (+ `pgincometax`, migration `00018_income_tax.sql`, API `/v1/incometax/*`, `m.IncomeTax`, root helpers `m.IncomeTaxFacts`/`m.DeclareIncomeTax`).
Law read (Direct Taxes Law, consolidated text on thdorsan.com, `https://thdorsan.com/law-doc/قانون-مالیات-های-مستقیم-ماده-N`): **art. 105** (text dated 1402/12/21): 25% rate on the income of companies
after losses and exemptions; note 3 taxes paid before are deducted and the excess is refundable; note 4 no other tax on dividends received; note 6 ordinary cooperatives get a 25% discount (effective 18.75%); note 7 one point
off per 10% growth of the declared income (up to five points) on condition of paying last year's debt and filing on time (the 1404 budget law, clause د, adds "above 35%" to it for the 1403 and 1404 performance years); the
1404 budget law clause ک cuts the rate 5 points for licensed producers in 1403 only; notes 8-14 (capital gains, added by the speculation-tax law of 1404/04/08) are NOT modelled (the regulation was not read). **Art. 106** (taxable
income of a legal person follows profitability and arts. 94, 95, 97). **Art. 110**: return, balance sheet and profit-and-loss statement within four months after the tax year, with the tax paid (the site's "current" text still carries wording
that the 1380 amendment replaced; the four months are unchanged). **Art. 147** (accepted cost: documented, normal, solely for earning the income; note 2: costs of exempt/zero-rate/flat-rate income are not accepted; note 3: payments
of 200,000,000 rials and more must go through the bank; a 1404/09/18 cabinet decision on adjusting such thresholds is listed but was NOT read). **Art. 148** lists the accepted costs (29 items; no list of non-deductible items exists;
item 12: a loss shown by the books is set against the income of the following year or years, the page shows NO five-year limit: the 5-year rule from memory/secondary sources is NOT applied; the rule has a `loss_carry_years` data field, empty).
Arts. 150-154 and 151 (depreciation schedule/coefficients) are repealed on the site; depreciation: use the fixed-asset module's legal table.
- Rules (`GET/POST /incometax/rules`): `rate_bp`, `cooperative_discount_bp`, `loss_carry_years` (null = no limit), `filing_months`, `source`; seeded 2500 / 2500 / null / 4 from 2002-02-16 (1380/11/27).
- The taxable income is NOT derived from the chart: accounting profit (income statement of the fiscal year, tax expense not booked) + explicit adjustments the accountant supplies WITH LABELS (`add_backs`, `deductions`, `depreciation_adj`
  = accounting minus tax depreciation), `cooperative`, `rate_reduction_bp` (note 7, the 1403 production cut: you state the facts), `credits` (taxes already paid/withheld, note 3). Older tax losses are set off oldest first.
- `POST /incometax/preview/{fiscal_year_id}` (body = adjustments) computes with the full breakdown; `POST /incometax/assessments/{fiscal_year_id}` (Idempotency-Key) declares once per fiscal year (advisory lock, concurrency test):
  Dr `expense.income_tax`, Cr `payable.income_tax` (tax - credits), Cr `prepaid.income_tax` (credits); dated the year's last day while the year is open, today when it is already closed. A tax loss is stored and later set off; `GET /incometax/losses`.
  `POST /incometax/assessments/{id}/payments` (Idempotency-Key) pays, never above what is owed. `due_on` = four months after the year end (month end), `overdue` shown.
- Coding: expense and (template chart) payable/prepaid are unmapped (the template has no income-tax codes, none invented); the full chart maps payable 211201 and prepaid 111909.
- NOT built: tax at separate rates, capital gains, advance-payment instalments (علی‌الحساب) as a schedule, late-filing/late-payment penalties (arts. 169, 186, 190, 191 not read), the cabinet deadline extensions (enter as data if needed), the exemptions of arts. 132 ff. (give the exempt income as a deduction), the tax return form.

## Profit appropriation: legal reserve and dividends (اندوخته قانونی و تقسیم سود)

Package `appropriation` (+ `pgappropriation`, migration `00015_appropriation.sql`, root `appropriation.go`, API `/v1/accounting/profit-appropriation*`, `m.Appropriate`). It works on a CLOSED fiscal year
(profit is appropriated after the accounts are approved). The rules are from the FULL TEXTS of the Commercial Code (لایحه قانونی اصلاح قسمتی از قانون تجارت مصوب ۱۳۴۷/۱۲/۲۴, read at
https://vakilik.com/laws/book/MzM0): art. 140 and 238 (one twentieth of the net profit after earlier losses is set aside as the legal reserve every year; optional once it reaches one tenth of
the capital, and it resumes after a capital increase), art. 239 (distributable profit = net profit - earlier losses - legal reserve - other reserves + undistributed profit of earlier years), art. 240 (the general meeting
fixes the amount; payment within EIGHT months of its decision), art. 90 (when profit exists distributing 10% of the annual net profit is mandatory), art. 241 as amended 1395/02/20 (directors' bonus at most 3% of the
profit payable to shareholders in a public company, 6% in a private one: reported as `max_directors_bonus_basis_points_of_dividend`, not booked).
- `GET /profit-appropriation/preview/{fiscal_year_id}?public_company=` reads the year's net profit (income statement without closing documents), the retained earnings carried into the year, the capital
  (`equity.capital`) and the legal reserve (`equity.legal_reserve`) from the books and shows the legal reserve, the distributable profit and the mandatory minimum dividend.
- `POST /profit-appropriation` (Idempotency-Key, `{fiscal_year_id, dividend, optional_reserve, decided_on, public_company}`): refuses a dividend below the mandatory minimum (409) or above the distributable
  profit, then books ONE journal Dr retained earnings, Cr legal reserve (the amount the law requires, computed, not typed), Cr other reserves, Cr dividends payable. One decision per fiscal year
  (unique key: 5 concurrent attempts produce one). `POST /{id}/payments` pays the dividend (Dr dividends payable, Cr bank), idempotent, never above what is owed; `overdue` shows when part is still owed after the 8 months.
- The net profit is whatever the books show: book the income tax expense BEFORE closing, or the profit is overstated. The retained earnings carried in are read from the balance on the year's first day, so any other
  posting dated that day counts too.
- NOT built: the withholding tax on dividends, booking of the directors' bonus, reserves prescribed by a company's articles of association, rules for companies that are not joint-stock, and capital increases from reserves.
  The sources are one legal database (vakilik.com) reproducing the articles; an accountant or lawyer must confirm them against the official gazette text.

## Inventory and cost of goods sold (موجودی کالا و بهای تمام‌شده)

Package `inventory` (+ `pginventory`, migration `00014_inventory.sql`, API `/v1/inventory/*`, `m.Inventory`).

- **Items** have a base unit (quantities are whole numbers of it: use grams instead of kilograms when you need fractions) and a cost formula: `fifo` (اولین صادره از اولین وارده) or
  `weighted_average` (میانگین موزون متحرک: the average is recomputed on every receipt, an issue takes its share of the total value). LIFO is not offered.
- **Receipt** (`POST /receipts`): Dr `inventory.goods`, Cr supplier payables (or the bank / any account you name) for the WHOLE cost of the receipt (price plus freight, duties and other costs of bringing it to its place).
- **Issue** (`POST /issues`): Dr `expense.cogs` (or another expense account), Cr inventory, at the formula's cost. Rounding never drifts: the last unit takes the remaining value, a used-up FIFO layer gives
  all its value. An issue above the stock is refused (409), so stock never goes negative; every movement locks the item row (`FOR UPDATE`) and is idempotent by `Idempotency-Key` (tested with 6 concurrent issues
  on 100 units: exactly 3 of 30 succeed).
- **Count** (`POST /counts`): a shortage is issued at cost to `expense.inventory_adjustment`; a surplus comes in at the item's average cost and is credited to the same account (an item with no stock has no
  cost for a surplus: receive it with its cost). **Write-down** (`POST /write-downs {net_realizable_value}`): lowers the carrying value to the net realizable value (Dr adjustment, Cr inventory); FIFO layers follow.
- `GET /valuation` lists stock per item; its total always equals the balance of the inventory account (tested after every operation kind), and the books stay balanced.

**Verified vs not.** From several consistent secondary summaries of accounting standard no. 8 (not the standard's own text): FIFO, weighted average and specific identification are the allowed formulas, LIFO
is not, inventory is carried at the lower of cost and net realizable value per item, cost includes purchase and conversion costs (sources: https://yassystem.com , https://tadbirtp.ir , https://vina.ir ,
https://www.hamrahansoft.ir). Whether weighted average must be periodic or may be perpetual is not stated in what was read: we use the perpetual (moving) average. NOT built: specific identification, standard
costing, production and conversion costs (work in progress, finished goods), warehouses and locations, lots and expiry, reversing a write-down when value recovers, customer returns at original cost, input VAT on
receipts (now: pass `vat` on the receipt), and linking an invoice to an issue (the contexts are independent: call Issue when you sell). The sale price and the revenue are the invoice's business, not inventory's.

## Fixed assets and depreciation (دارایی ثابت و استهلاک)

Package `fixedasset` (+ `pgfixedasset`, migration `00013_fixed_assets.sql`, API `/v1/fixed-assets/*`, `m.FixedAssets`).

- **Categories** (`POST /fixed-assets/categories`) carry the method (`straight_line` with `life_months`, or `declining_balance` with an annual `rate_bp` of the book
  value), an optional salvage share and a mandatory `source` (where the rate or life comes from). Creating one opens two ledger accounts,
  `fixedasset.<code>.cost` and `fixedasset.<code>.accdep` (a contra asset, so its natural balance is negative); `cost_coding` / `accumulated_depreciation_coding` map them to your chart.
  An asset copies the terms when it is acquired, so changing a category never rewrites an asset.
- **Acquire** (`POST /assets`, Idempotency-Key): Dr cost account, Cr bank (or `paid_from`). `already_booked` registers an asset whose cost is already in the books, optionally with
  `opening_accumulated` and `months_done` (not booked again).
- **Run** (`POST /depreciation-runs {jalali_year, jalali_month}`): only for a finished month, in order (an asset that is behind blocks the run and is named). One journal, Dr
  `expense.depreciation`, Cr each category's accumulated depreciation, dated the month's last day. Idempotent: rows are unique per (asset, month), assets are locked
  `FOR UPDATE`, and a repeat or a concurrent run returns the stored run (tested with 4 goroutines).
- **Maths** (`fixedasset/depreciation.go`, unit-tested): straight line = (cost - salvage) / life, every cumulative share rounded once so the months add up exactly with no drift
  and the last month takes the remainder (big-integer maths, no overflow); declining balance = annual rate x book value at the start of the Jalali year, spread over the months of that year,
  never below salvage. `GET /assets/{id}/schedule` projects the rest, `/history` lists what was booked.
- **Dispose** (`POST /assets/{id}/dispose`, Idempotency-Key): Dr accumulated depreciation, Dr proceeds account, Cr cost; the difference goes to `expense.asset_disposal`
  (debit = loss, credit = gain). Depreciation must be booked up to the month before the disposal; the disposal month itself is not depreciated.

**Legal table (built in, `GET /fixed-assets/legal-table[?group=]`, `POST /fixed-assets/legal-table-categories {row_id, code?, name?}`).** The newest version found is
the table of article 149 of the direct-tax law as approved on 1394/04/31 (regulation "مقررات و ضوابط اجرایی هزینه استهلاک دارایی‌ها و هزینه‌های تأسیس", signed by the
Minister of Economic Affairs and Finance, proposal 200/16199 of 1395/10/11, 25 groups) **as amended by circular 200/96/174 of 1396/12/28** of the National Tax Administration (applies
from fiscal years starting 1396/01/01; it adds rows 4-2b refrigerators 10 y, 8-1c containers 10 y, 9-4 security systems 5 y, 10-2b phones 5 y and widens some wording). 163 rows
are in `fixedasset/legaltable.go` (`LegalTableVersion` names the source); a category made from a row stores the citation as its `source`. These two documents were READ FROM THE OFFICIAL
PDFs themselves (images/typed PDFs hosted on sarmayegan.com and static.accpress.com; tax.gov.ir could not be reached), not from blog summaries. Descriptions are SHORTENED (each row lists
many more machines): group, row, method and number are exact; the official text is the authority. Rows that need a decision the table does not make (dam life 35-100 years set by an
engineer, intangible assets by units of production, by contract term or expensed in the year) are listed with `selectable: false` and a note; make those categories by hand.
Verified from the regulation text: depreciation starts at the beginning of the month after the start of use (article 6, what we do); straight-line, declining-balance and units-of-production are the
accepted methods (article 9); in declining balance, once the book value is below 5% of the full cost all of it is depreciated in the next year (implemented and tested); a company may depreciate more
slowly than the table if it declares the list of assets by the deadline of the return of the acquisition year (article 19, not applicable in an exemption period; you do this by choosing your own category);
second-hand assets use the table's lives (article 13); a loss on sale is deductible in that year (article 14).
NOT built: units of production, expensing of assets below 10% of the small-transaction threshold (article 12, needs the threshold), separate components of an asset (article 5), idle-asset
reduction (article 6 remark), shifts, leased-asset and BOT rules (articles 15-16), rows for establishment costs and rule-specific deductions, and a newer amendment, if one exists after
1396/12/28 (none was found; one site lists a "1404 table" without an official source). Accounting standard no. 11 was seen only through summaries. An accountant must confirm the row for each asset.

## Balance sheet (ترازنامه)

`GET /v1/accounting/balance-sheet?as_of=` (default today), code `ledger/reports.go`. Assets, liabilities and equity are listed per account in their natural
direction (a contra account such as accumulated depreciation is negative), plus the **current result** (سود (زیان) جاری): revenue minus expense not yet moved to
retained earnings by a closing document. `balanced` is true when assets = liabilities + equity + current result. A date inside a closed year shows that year's
real position (its own closing documents are left out, earlier years' closing and opening pairs stay in), tested for the running year, the closed year's last
day, the next year's first day and today.

Current vs non-current (جاری / غیرجاری): the criteria are the accounting standard's (turned into cash or settled within the operating cycle or twelve months;
sources: https://tehranhesab.com , https://www.accpress.com/news/1397/12/ , secondary summaries of standard no. 1, not the standard text). The split is taken
from the title of an account's top group in the chart ("غیرجاری"), so it is only as good as the mapping of accounts to the chart; accounts without a coding
code count as current and are listed in `unclassified_accounts`. **Not built / not verified:** the exact official layout of the balance sheet and of the
multi-step income statement (gross profit, operating profit, net) and the cash-flow statement: the sources found did not give the layout, so the
income statement stays a revenue/expense list. An accountant must supply or approve the layout.

## Official layouts of the statements (صورت‌های مالی طبق استانداردهای حسابداری)

`ledger/statements.go`, API `GET /v1/accounting/official/{balance-sheet?as_of=, income-statement?from=&to=, changes-in-equity?from=&to=, cash-flow?from=&to=}`. The older `/accounting/balance-sheet` and `/income-statement` stay.
**Source (official PDFs read page by page, revised 1397)**: standard 1 `https://thdorsan.com/storage/content/421-s1p.pdf` (ارائه صورت‌های مالی) and standard 2 `.../421-s2p.pdf` (صورت جریان‌های نقدی); audit.org.ir itself returned 504.
- VERIFIED from the text: the complete set of statements (std 1 para. 9); the minimum items of the statement of financial position (para. 55) and of profit and loss (para. 83: operating revenue, finance costs, equity-method share, tax expense,
  discontinued operations, profit); no mandatory order or format (para. 59); current/non-current criteria (paras. 68, 71: operating cycle or twelve months, held for trading, cash); sub-totals are allowed if made of recognised items (paras. 86-88).
  **So the "multi-step" layout (gross profit, operating profit, profit before tax) is NOT mandated by the revised standard**: those three lines are offered as optional sub-totals, labelled so in the response.
  Standard 2: three classes only (operating, investing, financing; this revision has no separate "returns on investment" or "tax" sections), indirect method per paras. 17-19.
- Mapping: by ledger account code through the data rules in `statements.go` (`LayoutVersion`); accounts it does not know are listed in `unclassified_accounts` and fall into "other" lines (balance sheet: by the chart's current/non-current group, as before).
  NOT read: std 2 paras. 20 ff. (where interest, dividends and tax are classified), the sample formats, the statement of comprehensive income. The choices made (dividends and capital = financing; income tax in operating; unknown non-current assets = investing, unknown
  non-current liabilities = financing, other unknown = operating) are the module's, to be confirmed by an accountant.
- Changes in equity: columns capital, reserves, retained earnings (+ other); retained earnings include the cumulative result, taken from the revenue/expense accounts (closing documents are left out of all four statements).
- Cash flow: derived from the balance of every account at both ends (the identity change in cash = change of liabilities and equity + profit - change of other assets), with `reconciled` telling that the three classes add up to the change of the cash accounts
  (`bank.*`). Depreciation and disposal results are added back and their cash effect is investing. Notes to the statements, comparatives and the direct method are not built.

## Two-person approval (تأیید دو نفره)

Package `approval` (+ `pgapproval`, migration `00019_approvals.sql`, API `/v1/approvals`, `m.Approvals`, root `approval.go`). For year-end closing (`year_close`) and profit appropriation (`appropriation`).
- `Config.RequireApproval` (default OFF): when ON the direct endpoints (`POST /accounting/fiscal-years/{id}/close`, `POST /accounting/profit-appropriation`) answer 409 `approval_required`; when OFF they work as before and the workflow is optional.
- `POST /approvals {kind, payload}` (Idempotency-Key) files a request by the preparer (one pending request per kind and fiscal year, 409 `already_pending`); `POST /approvals/{id}/approve` runs the action as the approver inside the
  approval's own transaction (the same person is refused: 409 `same_person`; a failing action leaves the request pending; concurrent approvers run it once; approving twice returns the same result); `POST /approvals/{id}/reject {reason}`
  (anyone with the finance role, including the preparer withdrawing); `GET /approvals?status=`, `GET /approvals/{id}`. Request, execution and rejection write audit + outbox events. The action's audit actor is the approver.
- "Different person" means a different user id from the token; it does not know roles beyond finance, so two admins with the same login are one person.

## Fixed assets, part 2 (migration 00020)
- **Units of production** (regulation art. 9-c): method `units_of_production`; `life_months` holds the TOTAL CAPACITY in units for this method (a reused column, documented). `POST /fixed-assets/assets/{id}/usage {jalali_year, jalali_month, units}` records a month (before it is depreciated);
  the run takes (cost - salvage) x cumulative units / capacity minus what was taken (one rounding per cumulative share, no drift; use beyond capacity stops). No schedule can be projected.
- **Void a run** (`POST /fixed-assets/depreciation-runs/void`): reversing journal dated today, assets' accumulated depreciation and counters restored, run marked voided (history kept), re-run allowed; refused when an asset was depreciated again or disposed after the run.
- **Partial disposal** (`POST /fixed-assets/assets/{id}/partial-disposal`, Idempotency-Key): `portion_bp` of cost, accumulated depreciation and salvage leaves; gain/loss on that share; the asset stays active.
- **Components** (art. 5, `POST /fixed-assets/component-assets`): a composite asset as parts with their own categories; every part except the main body must reach `Config.ComponentMinBP` (default 20%) of the total cost and have different terms than the main body; VAT split by cost.
- **Art. 12 small assets**: the limit is versioned DATA (`mz_fa_thresholds`, `GET/POST /fixed-assets/thresholds`): seeded 1404 = 2,850,000,000 and 1405 = 4,230,000,000 rials (small-transaction limit; cabinet decision 20877/t25634 of 1405/03/11 per mefa.ir/news/741085 — only the headline of that page was read; irib, tasnim, mahaksoft report the same number; one source dates it 1405/02/27;
  both rows are `verified=false`). The expensing limit is `Config.ExpenseFractionBP` (default 10%, as told by the owner) of it: `GET /fixed-assets/expense-limit?date=` = 423,000,000 rials in 1405. `POST /fixed-assets/small-assets` books Dr `expense.small_assets` (+ input VAT) / Cr paid-from, refused at or above the limit.
- NOT built: the idle-asset rule (art. 6 remark; the text was not re-read), shift/extra-shift rules, leased assets and BOT (arts. 15-16), revaluation, tax-vs-accounting depreciation differences (use the income-tax `depreciation_adj`).

## Inventory, part 2 (migration 00021)
- **Write-down reversal** (`POST /inventory/write-down-reversals`): standard 8 para. 46 was READ from the official PDF (`.../421-s8p.pdf`): a rise of the net realizable value is recognised, at most up to the earlier reduction. The item keeps `writedown_balance` (grows with each write-down, shrinks in proportion when stock leaves by issue or count loss);
  the reversal raises the value by min(new NRV - carrying value, that balance), Dr inventory / Cr `expense.inventory_adjustment`. FIFO layers get the raise by quantity (layer costs are not tracked separately: an approximation, the cap is on the item's total).
- **Invoice sale -> stock issue**: `m.IssueStockForInvoice(invoiceID, lines, actor)` in the root package (contexts stay independent): issues the given items for an issued invoice, key `invoice-stock:<invoice>:<item>`, idempotent.
- (The items formerly listed here as not built are built in "Inventory, part 2" below.)

## Fixed assets, part 3: idle assets (migration 00023)
**Source (OFFICIAL text read):** the 21 articles + 4 remarks of «مقررات و ضوابط اجرایی هزینه استهلاک دارایی‌ها و هزینه‌های تأسیس» (approved 1394/04/31, circular 200/95/78 of 1395/11/04; PDF http://static.accpress.com/@A.pdf, 4 pages read as images).
- **Art. 6 remark**: a firm MAY (می‌توانند) take 30% of the depreciation for the months an asset is idle or temporarily unused for MORE than six consecutive months in one financial period; for a time-based method 70% of the idle time is added to the remaining life of the table; it does NOT apply to buildings, building installations and intangible assets with a definite life. The text says nothing about reverting: after the span the asset simply depreciates normally.
- Built: `POST/GET/DELETE /fixed-assets/assets/{id}/idle-spans` (a closed range of Jalali months inside ONE Jalali year, recorded BEFORE those months are depreciated: later it is 409 `month_depreciated`, void the runs first). `fixedasset.IdleApplies` merges touching spans, clips them to the fiscal (= Jalali) year and applies the rule when the merged run is longer than `Config.IdleMinMonths` (default 6). The idle months take `Config.IdleRateBP` (default 3000 = 30%) of a normal month; straight line keeps a life PROGRESS counter in thousandths of a month (`progress_milli`), an idle month adds 300, so the unused 70% is appended to the remaining life exactly as the text says; declining balance takes 30% of the normal monthly amount (the rest stays in the book value). Units-of-production assets have no idle months (record zero units). Voiding a run restores the progress.
- Exempt: categories flagged `idle_exempt` (set automatically for legal-table groups 8 buildings and 25 intangibles; building INSTALLATIONS of other groups are not told apart by the table: flag the category by hand). 6 months exactly does not qualify. Thresholds are Config defaults (sourced above), not a versioned table: the text gives no dated amendments.
- **Shift / extra-shift rules**: NOT built: the 21 articles and 4 remarks contain none (the old 1366 tables had shift coefficients; the 1394 regulation has no such article), and the table notes read earlier carry none.
- **Leased assets (art. 15) and BOT / buy-back (art. 16)**: the text is two sentences. Art. 15: the lessor depreciates leased fixed assets under the accounting standards and the table (nothing new to build: keep the asset in its own category). Art. 16: assets of BOT and buy-back contracts (booked as project costs) are depreciated "in proportion to the yearly income or to the years of the operation period, as the case may be": use a straight-line category whose life is the operation period in months, or a units-of-production category whose capacity is the total expected income and whose monthly "units" are the income of each month (both exist). No dedicated BOT module: the expected-income schedule is a contract fact the text does not define.
- Other articles read and already covered: art. 5 components (done), 9 methods, 10-11 table changes (rate or life change of an existing asset: NOT modelled), 12 small assets, 14 loss on sale of unusable assets, 17 environment/new-technology assets (half the life or double the rate when listed and approved: NOT modelled, needs the approved list), 19 optional slower depreciation, 20-21.

## Inventory, part 2 (migration 00024)
**Source (OFFICIAL text read):** accounting standard no. 8 «حسابداری موجودی مواد و کالا», paras 4-37 (https://thdorsan.com/storage/content/421-s8p.pdf, pages 173-182).
- **Specific identification** (`method: specific_identification`, para 25: items attributed to a specific order/purchase, not interchangeable items): every receipt is a lot (the receipt returns `lot_id`); every issue and transfer MUST name `lot_id`, the cost is that lot's (a partly used lot gives its share). A count shortage of such an item is refused (issue it from its lot). Para 29 allows only specific identification, FIFO and weighted average: LIFO, base stock etc. are not offered.
- **Warehouses** (`/inventory/warehouses`, `/inventory/items/{id}/stock`, `POST /inventory/transfers`): stock is tracked per item and warehouse; the main warehouse `MAIN` (id `00000000-0000-4000-8000-000000003001`) holds everything that existed before and is the default of receipts, issues and counts (`warehouse_id`). An issue or transfer larger than the warehouse's quantity is 409 even when the item has stock elsewhere; counts are per warehouse. A transfer has no journal (one inventory account): lots keep their cost, lot number and expiry; the moved part becomes a new lot in the destination (FIFO order there follows the transfer order); average items keep the item's average. Row lock per item serialises concurrent moves (tested).
- **Lots and expiry** (`lot_no`, `expires_on` on receipts of fifo/specific items; a lot on a weighted-average item is refused): `fefo: true` on a FIFO item issues the lot that expires first (lots without a date last); `GET /inventory/expiring-lots?on_or_before=` lists open lots (the stock to sell, write down or scrap).
- **Production costing** (package `inventory`, `/production/*`, accounts `inventory.wip`, `production.overhead`, `expense.production_variance`): paras 9-20. An order collects materials (`/orders/{id}/materials`: issued from stock at cost, Dr WIP) and direct labour (`/labor`: Dr WIP, Cr payable or the account you name); `/complete` applies the overhead at the rate in force on the date, expenses an ABNORMAL loss (para 18: waste beyond normal; admin overhead and selling costs never enter an order, para 18-19) and receives the finished goods at the remaining cost as a lot (Dr inventory, Cr WIP). WIP after completion is zero (tested); `GET /production/work-in-process` = the WIP account.
  **Overhead allocation basis is versioned data** (`/production/overhead-rates`, effective-dated, immutable): basis = direct labour cost | material cost (rate in basis points of the base) | machine units | output units (rials per unit), separate FIXED and VARIABLE rates. Para 11: fixed overhead is allocated on the NORMAL capacity: give `budget_fixed` and `normal_capacity` and the fixed rate is derived (`DeriveFixedRate`); variable overhead follows actual use. Completing with no rate in force is 409 (`no_overhead_rate`) unless `no_overhead` is set: overhead must not be forgotten silently. NO real rates are shipped: they are the firm's own budget (nothing official exists); tests use invented numbers.
  Actual overhead goes to the pool (`POST /production/overhead`: Dr pool, Cr payable); `POST /production/overhead-settlements` expenses actual minus applied to `expense.production_variance` (the cost of unused capacity, para 11) and credits back an over-absorption; an over-absorption could instead be spread over stock and COGS: accountant's choice, flagged. All money-moving calls are idempotent by key; orders are row-locked.
  NOT built: bills of materials, co-products/by-products (para 14), standard costing variances (paras 31-35), service-business costing (para 21), service cost centres allocation (para 16), borrowing costs in cost (para 20), agricultural produce at net realizable value (21 bis).

## Moadian e-invoice completion
- **TaxId** (`moadian.TaxID`, `ValidTaxID`, `VerhoeffDigit`): per RC_DCPS.SN_V1.3 (Khordad 1401), text from its verbatim repost on maliatman.com (the PDFs on intamedia.ir were unreachable): MEMORYID(6) + DATE(5 hex, days since 1970-01-01 UTC) + SERIAL(10 hex) + Verhoeff digit over decimal(memory id: letters as ASCII codes) + days(6) + serial(12). The THREE official worked examples of the paper are unit tests and pass
  (open point: UTC or Tehran-local day; two third-party implementations use UTC).
- **PayloadBuilder** (`moadian.InvoiceBuilder`): now per the OFFICIAL paper **RC_IITP.IS V7.9 (Tir 1405, 116 pages, newer than the V7.8 the earlier session knew)**, read as text from a copy on maliyaar.ir (intamedia.ir / tp.tax.gov.ir time out from here); the field dictionary, per-pattern requirements, subject rules and formulas are summarised in `docs/moadian-v7.9-findings.md`. Rules: lowercase keys, whole rials, every derived amount TRUNCATED (prdis = am x fee, adis = prdis - dis, vam = adis x vra/100, tsstam = adis + vam), header totals as sums.
  **Built now**: patterns 1 sale, 3 gold/jewellery/platinum (tcpbs = consfee + bros + spro, adis = prdis + tcpbs - dis, vam = 10% of tcpbs + vra% of prdis, truncated once: the paper does not say whether each part is truncated), 4 contracting (crn, at most 12 digits), 7 export (cut, exr, ssrv, sscv, torv/tocv, VAT 0, customs cottage/office/licence); subjects 1-4 (`ins`; irtaxid REQUIRED and checked with `ValidTaxID` for 2-4; return refused for export as the paper says); settlement 1/2/3 (setm 3: cap given, insp = tbill - todam - tvam - cap, both positive); the payments array (pmt 1-8, pv between 0 and tbill, 0 for a credit sale). Neutral fields: `einvoice.Invoice.{Pattern,Subject,RefTaxID,Settlement,CashAmount,ContractNo,Payments,Customs}`, `Line.{WageFee,SellerProfit,Commission,Export}`, `Party.Kind` (tob); API `POST /einvoice-requests/invoice/{id}` takes an optional JSON body with them and a correction/cancellation/return is its OWN idempotent document (source id `<invoice>:s<subject>:<irtaxid>`), `m.EnqueueInvoiceWith`.
  **Tests**: the ONLY numeric worked example in the official papers is the pattern-1 credit sale of RC_TICS.IS_v1.3 (tax id A1121604C220002F095011 and every figure; it reproduces EXACTLY, a second independent check of the TaxId algorithm); V7.9 itself contains no numeric example, so gold/export/setm 3 vectors are computed from the paper's formulas (not official numbers). The V01 (1401) sample is NOT used: its cap/cop/vop contradict V7.9.
  **Added in the follow-up**: patterns 2 currency sale (prdis = cfee x exr truncated, sovat between cfee x (exr - cpr) and cfee x exr, vam = sovat x vra), 5 utility bill (billid <= 19 digits), 6 airline (ft 1/2, quantity 1, national id or passport `bpn`, optional `tinc`), 9 petroleum (vam = quantity x vba when a per-litre base is given), 11 commodity exchange (one row, no prdis/dis/fee, asn + asd, VAT 0, no return), 13 insurance (`in` 9-12 digits, optional `an`), 14 chain (tinb required, VAT 0); other taxes/levies (odt/odr, olt/olr: odam = base x rate, base = amount after discount, tcpbs for gold, rial value for export); late Art. 9 invoices (`RegisteredAt` -> `insr` 1 + `indati2m`). `POST /einvoice-requests/invoice/{id}` takes a `details` object (bill id, flight type, policy, exchange notice, payments, customs, buyer kind/passport, registered_at and per-line extras such as wage_fee, export, fx, other_tax_*, petro_base_e4).
  **Still NOT built (fails closed)**: pattern 8 (waybill: needs the transport-type code and country/city code tables the paper lists in separate tables that were not transcribed) and inty 3 (POS payment receipts: the paper's field set for it was only partly read); cop/vop per-item cash shares (optional in the paper); the `inno` the paper's own sample prints as a DECIMAL (49321217) while its field table says a 10-character hex string: the builder follows the table; the paper's `pdt` "day precision" is probably a typo, ms are sent. Unverified: inty/tob inference for buyers (type 1 now REQUIRES the person type: `Party.Kind` or a national id), the cancel invoice (the server takes its body from the reference; we still send the given lines).
- **Technical paper / adapter**: the adapter (client.go, jose.go) implements the API v2 of **RC_TICS.IS_v1.3 (Mehr 1402)**: nonce JWS token, server-information, JWS+JWE invoice packets, inquiry endpoints: confirmed again against the code (an automated reading claimed the adapter follows the OLD 1401 API; that is wrong: the old `/req/api/self-tsp/` paths are not used). RC_TICS.IS v1.4-v1.6 could NOT be obtained from any reachable host, so there is no v1.3-vs-v1.6 diff and nothing was changed in the adapter: **re-check before the first real send**. Never run against the real server; the owner has no test taxpayer.
- **Key and CSR tool**: `moadian.GenerateKeyAndCSR` and `go run ./cmd/moadian-keygen -cn ... -out ./keys` (stdlib only, RSA >= 2048, SHA256-RSA PKCS#10, key as PKCS#8, written 0600). What subject fields the portal demands was not found in an official text: fill them from the portal's guide. The private key belongs in a secret manager or an encrypted file, never in the database, logs or repository.

## Payroll official files (migration 00022)
- **Social-security lists** `payroll/ssafile` + `m.SSAFiles` + `GET /payroll/ssa-files?year=&month=` (base64 JSON): dBASE III writers for DSKKAR00.DBF (workshop) and DSKWOR00.DBF (insured persons), field names/types/widths read from the owner's REAL sample files (structure only; record lengths 287 and 417 are tests). No OFFICIAL description was found
  (ssa.ir, tamin.ir, and every search only show the list program), so: only identities confirmed on the sample are enforced (workshop totals = sums of the worker rows, count = workers, MAH = ROOZ x DD, MASH = MAH + MAZ); everything else is explicit input, no defaults; the build FAILS CLOSED (no workshop data, no profile, missing national id or insurance number, insurable pay not a whole multiple of the days, text longer than its field).
  **Persian text** (`ssafile.IranSystem`, now the DEFAULT encoder; `ssafile.ASCIIOnly` is the strict alternative): the files store the Iran System code page in VISUAL order. Table = Roozbeh Pournader's "IRAN SYSTEM to Unicode" v1.0 (archived copy of the Leisher-hosted IRANSYSTEM.TXT), cross-checked byte for byte with Wikipedia (en) and the independent python-iransystem 0.5.1 (same on all 128 upper bytes and on the positional form of all 35 letters; the three sources and the rule are in `docs/iransystem-table.md`). The encoder shapes every letter into its positional form (lam-alef = 0xF2, ZWNJ breaks joining), reverses the line, keeps digit/Latin runs left-to-right, mirrors brackets; Arabic yeh/kaf are normalised.
  **VERIFIED against the owner's real sample**: all eight distinct job-title/sex/nationality values (مرد، زن، ایرانی، کارمند، رئیس هیئت مدیره، مدیرعامل، کارگر ساده، مسئول فنی) decode to the right words AND re-encode to exactly the stored bytes (they are the test vectors; they hold no personal data). NOT verified / refused (fail closed): ئ at the end of a word (the table has only a joining form), ء-only forms beyond 0x8F, ۀ ة, any character outside the table; visual order of mixed Latin text and brackets follows python-iransystem's documented heuristic, not a real file.
- Employee profile (`PUT/GET /payroll/employees/{id}/profile`: father's name, id number/place/date, birth date, sex, nationality, occupation, job code, start/end dates) and workshop data (`PUT/GET /payroll/workshop`: code, name, employer, address, kind, list number, payment month, rates, the employer/unemployment shares in basis points) are stored as typed JSON documents.
  Mapping of payslips (module's reading of the field names): DD = days, MAH = MASH = insurable pay, MAZ = 0, TOTL = gross, BIME = the employee's share; TKOSO/BIC = employer/unemployment shares of the total insurable pay.
- **Tax-office salary list (ماده ۸۶, WH/WP/WK)**: STILL NOT exported (fail closed). What the second search found (`docs/wh-wp-layout-secondary.md`, from accounting-blog MIRRORS of the tax office's «راهنمای تولید فایل الکترونیکی مالیات بر درآمد حقوق»; tax.gov.ir, salary.tax.gov.ir and intamedia.ir never came up): files `WH/WP/WKyyyymm.TXT`, comma separated, no header, CRLF, dates YYYYMMDD, whole rials.
  TWO guide versions exist and they conflict: the OLD one (about 1395: WH 34 columns, WP 23, WK 14) matches the owner's sample on WH (34 columns, column 1 national code, column 19 the employee insurance share) but its WP has 23 columns while the sample has 22; the NEW one (v1.7.0.0 of 1403/02/27: WH 35 columns without any insurance-share column, WP 23 columns in a different order, no WK) does not match the sample, and a 1404 version (a secondary source says it adds a field for incentive/welfare benefits taxed at 10%) could not be fetched. Neither is the official PDF read from an official host, the lookup-code tables could not be confirmed, so no exporter is written: a wrong layout would be rejected by the portal or, worse, accepted with wrong tax. To build it: get the current PDF from the salary portal and implement `WH`/`WP` strictly from it.

## Follow-up batch: treasury, payout, payroll (migrations 00025-00027)
- **Cheques (00025)**: a BOUNCED received cheque can be presented again (`POST /cheques/{id}/deposit`: Dr in collection, Cr the customer's receivable, because the bounce put the claim back there; every presentation has its own journals) or returned (the customer pays another way: no second reversal). A received cheque in hand can be ENDORSED to a supplier (`/cheques/{id}/endorse`: Dr the supplier's payable, Cr cheques in hand) and taken back (`/take-back`: reverses that journal; it can be endorsed again).
- **Gateway fee VAT**: `fee_vat` on a settlement (the VAT part of `fee`): booked to `vat.input` instead of the fee expense. Not persisted as a column (the journal holds it).
- **Payout (00026)**: PARTIAL return (`POST /payout/sales/{id}/partial-return`, Idempotency-Key): commission, its VAT and the seller's share are recomputed on the reduced gross at the sale's own rates and the differences are booked (Dr commission, VAT, seller payable, Cr receivable); after the payout the seller's share becomes a CLAWBACK (account `receivable.sellers`). `POST /payout/sales/{id}/return-paid` returns a whole paid sale the same way. `POST /payout/clawbacks/{id}/recover` takes the money back in cash or OFFSETS it against what the platform owes the seller (never below zero; concurrent recoveries cannot exceed the amount, tested). `POST /payout/batches/{id}/reject-item` handles a transfer the bank rejected: Dr the pay-from account, Cr the seller's payable, sales payable again in the next batch; refused when a sale of the item was changed afterwards. Per-bank transfer FILES are not built (no bank format is known); a fully returned paid sale leaves its batch (the clawback row keeps the link).
- **Payroll (00027)**, all from SECONDARY sources (no official host could be read; notes in `docs/payroll-law-findings.md`): **annual bonus (عیدی)**: `bonus_days` on a payslip (365 = full year): wage/30 x 60 days x days/365, capped at 90 days of the minimum daily wage (the rule's `floor_daily`, prorated), not insurable; income tax: the part up to the 0% bracket of the monthly table (= 1/12 of the annual exemption, art. 91 item 10) is exempt, the excess taxable (two payslip lines). **Overtime cap** 240 minutes per worked day (Labour Law art. 59) refuses more with `overtime_over_cap`. **Holiday calendar** (`/payroll/holidays`, versioned data, 1405 seeded: 25 official days + Workers' Day; 25 Farvardin is disputed between sources and NOT seeded): holiday work is checked against the month's rest days (Fridays + official holidays); a year without a calendar only counts Fridays and skips the check. **Two contracts in a month**: a mid-month salary change is paid by segments (each prorated by its days, summed into ONE payslip, insurance on the combined base); not combinable with a worked-days override. **Loan write-off** (`POST /payroll/loans/{id}/write-off`): Dr `expense.employee_loan_writeoff`, Cr employee loans, loan closed; no source states whether a forgiven loan is taxable or insurable: nothing is added to a payslip (the accountant adds it as an extra if needed). The seeded 1405 rule version's terms were UPDATED by the migration with the bonus and the cap (unverified, flagged in its source).
- Sanavat (severance) kept at 30 days/year with a 365-day year (no 360 convention found in any source).

## Known limits (honest list)

- **Not built yet:** a UI, and a two-person approval of the year-end closing. Year-end closing handles up to 100,000 accounts with a balance in one transaction.
- Neither gateway adapter calls a refund API (ZarinPal's needs a session token and IDPay has none), so every refund is a
  manual card-to-card payout today; see "Refunds" for the workflow.
- `Reporter` (listing a gateway's transactions) is not implemented by the adapters: reconciliation takes the
  statement you download from the gateway panel (CSV or JSON).
- The ledger statement orders by posting order, not by document date, so a back-dated entry appears where it was posted.
- Gap-free numbering serialises postings of one fiscal year on one row. That is what the law asks for and it is
  fine for thousands of postings per second; if you ever need more, number in a batch step instead.
- Settlement failures that are business errors (the invoice was voided while the payer paid) keep the money:
  the payment is `paid` with a `settlement_error` and listed by `GET /payments/unsettled` for staff to refund.
- The ZarinPal v4 and IDPay v1.1 adapters are tested against fixtures written from the providers' public docs, not
  against their sandboxes (no network was involved); run one sandbox payment per gateway before going live.
