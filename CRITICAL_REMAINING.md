# CRITICAL_REMAINING: what mizan does NOT do yet (as of 2026-11-09)

State: `mizan/` (ledger, payment + manual refunds, wallet, invoice, reconcile, outbox, API, treasury, payroll) is built and green
(`make mizan-check`, `make mizan-integration`), all UNCOMMITTED. Step (d) payout is built too (see section D). Details of what exists: `mizan/README.md`. Decisions: the memory file
`mizan-financial-module.md`. Rules: contexts never import each other, post through `books.Poster`, ledger append-only, money is int64 rials,
audit + outbox in the same tx, idempotency keys, row locks, optional coding, new migration per step (`00028_*.sql` next), tests first,
Docker only (`scripts/mizan.sh`), no gokit changes, no commit unless asked.

## DATA RULE (from the user)
Never guess laws, rates, tables, file formats or API specs. Search the web (official sources first: ssa.ir, tax.gov.ir / nta.ir, mol.ir, intamedia.ir),
cite URLs, store legal values as versioned DATA with effective dates (a table + API to add a version), say what is verified and what is not,
and ask the user once when sources conflict.

## A. State (2026-11-10, after the third batch)
DONE and gated (`make mizan-check`, `make mizan-integration` green): everything of earlier batches (VAT, income tax, official statements, two-person approval, fixed-asset gaps, inventory write-down reversal, TaxId + payload + keygen, SSA DBF writers) PLUS:
- Fixed assets, idle-asset rule of art. 6 (migration 00023): 6+ consecutive months idle in a fiscal year -> 30% depreciation, 70% of the idle time appended to the life; exemptions; text read from the official regulation PDF.
- Inventory part 2 (migration 00024): specific identification, warehouses + transfers, lots, expiry, FEFO, production costing (orders, WIP, overhead rates as versioned data, pool + settlement), standard 8 paras 9-20/25/29 read from the official PDF.
- Moadian: payload builder per RC_IITP.IS V7.9 (patterns 1/3/4/7, subjects 1-4, setm 3, payments); Iran System encoder for the SSA files (default, verified on the real sample words).
Next free migration: 00028.
STILL NOT DONE (real limits, details in the README):
- Shift/extra-shift depreciation: the regulation has no such article (nothing to build). Art. 10-11 (a change of the table's rate/life for an EXISTING asset) and art. 17 (environment/new-technology assets: half life or double rate, needs the approved list) are not modelled. BOT/leased assets (arts 15-16): only by choosing a straight-line (life = operation months) or units-of-production (capacity = expected income) category; no dedicated module.
- Inventory: no bill of materials, co-/by-products, standard-costing variances, service-business costing, borrowing costs; over-absorbed overhead is credited to the variance account (an accountant may prefer to spread it over stock and COGS); no real overhead rates are shipped (the firm's own budget).
- Moadian: only pattern 8 (waybill: transport and country/city code tables not read) and inty 3 (POS receipts) are not built (fail closed), cop/vop shares (optional); RC_TICS.IS v1.4-v1.6 unobtainable (searched intamedia.ir, tp.tax.gov.ir, maliyaar.ir, karlancer.com, way2pay.ir, GitHub mirrors: only v1.3 and V01 found) so the adapter is unchanged and must be re-checked before the first send.
- Tax-office salary list WH/WP/WK: only secondary blog mirrors of two conflicting guide versions (old 34-column WH matching the sample; new v1.7 35-column WH without the insurance column; 1404 not found): STILL fail closed. Need the current PDF from salary.tax.gov.ir (see `mizan/docs/wh-wp-layout-secondary.md`).
- Iran System: ئ at the end of a word, ۀ, ة and characters outside the table are refused (the table has no form for them); mixed Latin/bracket visual order follows python-iransystem's heuristic, not verified on a real file.
- Follow-up batch (00025-00027) DONE: bounced-cheque re-presentation, cheque endorsement, gateway fee VAT, payout partial returns / clawbacks / rejected transfers, payroll bonus + overtime cap + holiday calendar + two contracts in a month + loan write-off (secondary sources; tax treatment of a forgiven loan unknown). STILL not built there: per-bank payout transfer files (no bank format), re-run of closed years, a Persian-language report layer, two-person approval of payouts.
- Not built, legal research still needed: income-tax penalties, capital gains and the advance-payment schedule; the 1404 holiday calendar and 25 Farvardin 1405; the Social Security wage ceiling of art. 34 and the 1405 minimum-wage decree text.
- Limits of what WAS built earlier: VAT levy split 7+3 and the 1405 rate are secondary sources; income tax: no penalties, capital gains or advance-payment schedule; statements: standard 2 paras 20+ not read; approval: same-person check by user id only.

## B. Blocked on the user (needs official documents, do NOT guess)
1. Tax-office salary-list file (ماده ۸۶): tax.gov.ir says the format is in its "electronic file guide"; no field spec was reachable. Need the guide.
2. Social-security list files DSKKAR00.dbf / DSKWOR00.dbf: built from the sample structure (no official field spec found); Persian text now has a verified encoder. An official SSA guide would settle the meaning of the unconfirmed fields (DSK_RATE, TOTL vs MASH).
3. Moadian e-invoice: built (adapter per RC_TICS.IS_v1.3, TaxId, payload per RC_IITP.IS V7.9), never run against the real server. Still needed to send anything: a signing certificate + private key, the fiscal-memory id (clientId) from the Karpooshe, a sandbox/test taxpayer, and the current technical paper (v1.4-v1.6) to re-check the adapter.
4. Gateway `Reporter` implementations and real refund APIs (ZarinPal, IDPay): only with sandbox credentials/docs.

## C. Data that is UNVERIFIED and must be checked against official texts
Payroll starting data (migrations 00007, 00008; row id `00000000-0000-4000-8000-000000001405`) came from secondary sites only: 1405 minimum wage 5,541,850 rials/day,
insurance ceiling 7x per day, floor = minimum wage, employee 7%, employer 20%, unemployment 3%, tax exemption 400M rials/month then 10/15/20/25/30%
(40-80-100-120-140M toman), overtime/holiday 140%, night premium 35%, 7.33 h/day, 30-day month, severance 30 days per year (min 1 year, untaxed, uninsured).
Disputed: tax on the unused-leave payout (seeded taxable, not insurable) and the base of overtime (hence the `wage_base` allowance flag).
Also unconfirmed: `VATBasisPoints` default 1000 (10%) must be confirmed by the user. Fix by adding a corrected rule version via `POST /v1/payroll/rule-versions`.

## D. Known gaps and limits
- Fixed assets: legal table seeded (163 rows, version in `LegalTableVersion`); no newer amendment after 1396/12/28 found; no revaluation, disposal month not depreciated; tax-vs-accounting depreciation differences are not modelled; idle spans must be recorded before their months are depreciated (else void the runs).
- Inventory: no standard costing/BOM/co-products; sale and issue are separate calls (root helper `IssueStockForInvoice` links them); a transfer makes the moved part a new lot (FIFO order in the destination follows the transfer order); input VAT on receipts is booked with the `vat` field (not apportioned for mixed supplies); weighted average is the perpetual (moving) kind (standard text on periodic vs perpetual not verified).
- Full chart (`IranianCodingFull`) comes from one 1388 vendor sample, not an official standard (none found); its system-account mappings (VAT, wallets, revenue at general level 4110) are suggestions for the accountant.
- Payout: starting terms are seeded (10% commission, 7 days hold, owner's choice, from 1405/01/01); change via `POST /v1/payout/terms`. VAT on the commission follows `mizan.Config.VATBasisPoints` (default 10%, owner's decision) unless terms set `vat_bp`; charged to the seller on top of the commission.
  Not built: Moadian e-invoice of the commission, per-bank transfer files, settling the customer side of a marketplace order (sale debits `receivable.customers`; do not also invoice it).
  Commission revenue is chart code 60101 (owner decided: never 60201).
- Payroll: a service year is taken as 365 days; rule lookup uses the month's first day.
- Treasury: the bounce entry
  (Dr receivable / Cr in-collection) is convention, flagged for the accountant; statement CSV is our own column shape (banks differ).
- Gateways: ZarinPal v4 and IDPay v1.1 adapters were tested only against fixtures, never a sandbox; neither adapter calls a refund API (all refunds are manual card-to-card).
- Year-end closing: documents are split by `Config.YearEndChunk` (default 3000) with transit accounts, max 100,000 accounts per closing; closing runs immediately (no two-person approval); no tax adjustments, legal reserve or dividends in the closing (book them first); the summary account has no chart code; descriptions are English; reopen restores only the last month; unsettled payments or draft invoices of other contexts are not checked (close them first).
- Ledger: statement orders by posting order not document date; gap-free numbering serialises postings of a fiscal year on one row.
- Settlement business errors keep the money (`paid` + `settlement_error`), listed by `GET /payments/unsettled`.
- UI: built in `mizan-ui/` (see its README, `make mizan-ui-up`). Screens or parts of screens without a backing API (shown as unavailable, never faked):
  Moadian connection / key settings (the provider is set at start-up, `mizan.WithEInvoice`); the journal "تفصیلی" column, document type and reference number (the journal API takes only date, description, lines); a list of ALL wallets (only `GET /wallets?owner_ref=`); invoice-void and payment-correction approvals (approvals exist only for year close and profit appropriation); a depreciation preview; adding production overhead rates and per-pattern Moadian details in the UI; document "approval" history for journals (only created_by/created_at exist).
