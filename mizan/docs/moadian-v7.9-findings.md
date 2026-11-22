# Moadian (مودیان) official papers: findings

Method: PDFs were fetched, converted with pdftotext and read as text. Persian text was normalised (NFKC).
Everything below tagged OFFICIAL was read in the official paper text itself. The PDFs are copies hosted
on third-party sites, but the cover pages carry the official document id and date.

Sources actually read:
- A. OFFICIAL RC_IITP.IS V7.9 (Tir 1405, 116 pp): https://maliyaar.ir/assets/billing_instructions/RC_IITP_IS_V7_9.pdf
  (host: maliyaar.ir; the PDF itself is the document "RC_IITP.IS _V7.9", "تیر ۱۴۰۵"). It supersedes V7.8 (Ordibehesht 1405).
- B. OFFICIAL RC_IITP.IS (113 pp, older edition, only fetched, not read): https://rima.tax/wp-content/uploads/2025/11/...pdf
- C. OFFICIAL RC_TICS.IS_v1.3 (Mehr 1402, ~60 pp): https://www.karlancer.com/api/file/1708606411-mcyv.pdf
- D. OFFICIAL RC_TICS.IS_V01 (Khordad 1401, 69 pp): https://way2pay.ir/wp-content/uploads/dastor-fani.pdf

## 0. Headline findings that matter for the Go implementation

1. The "v1.3" API as the user has implemented it (`/req/api/self-tsp/sync/...`, header `requestTraceId`/`timestamp`/`Authorization`,
   packet fields uid/packetType/retry/data/encryptionKeyId/symmetricKey/iv/fiscalId/dataSignature) is NOT what paper v1.3 describes.
   That is the OLD API from paper RC_TICS.IS_V01 (Khordad 1401). Paper v1.3 (Mehr 1402) documents the NEW "API v2"
   at `https://tp.tax.gov.ir/requestsmanager/api/v2/...` and says v1.2 API (old) stays available "until further notice" (appendix of v1.3).
   Details in section 3.
2. RC_IITP.IS latest found = V7.9 (Tir 1405). It adds fields `insr`, `nti1`, `nti2`; `indati2m` renamed. Section 1.
3. RC_TICS.IS v1.4/1.5/1.6 could NOT be obtained (not in search results; intamedia.ir and tp.tax.gov.ir time out from this
   machine). So "what changed in v1.6 vs v1.3" is unknown.
4. The IITP paper contains NO numeric worked invoice example. Appendix 9-1/9-2 are blank form templates, 9-3 is only a typed JSON skeleton.
   The only numeric samples are in the technical papers (section 4). Honest answer: the "official worked examples" are those two JSON samples only.

## 1. RC_IITP.IS V7.9 (OFFICIAL, source A)

### 1.1 Changes vs V7.8
- New header fields: `insr` (قاعده ارسال صورتحساب), `nti1`, `nti2` (یادداشت ۱/۲, optional, string max 30 each).
- `indati2m` renamed from "تاریخ و زمان ایجاد" to "تاریخ و زمان ثبت صورتحساب (میلادی)".
- `bsrn` renamed from "شماره قرارداد حق‌العمل‌کاری" to "شماره قرارداد معامله از طریق شخص ثالث".
- New section 6: invoices under Article 9 of the Store Terminals and Moadian Law (late submission).
- Reference/correction/return notes rewritten (sections 5, 5-2). Status of patterns 5 and 11 columns in table 1 changed.
- Rows removed from control tables 8 (irtaxid rules about date, and the "same inbox" rule remained in V7.9 as row 8).

### 1.2 Invoice types (inty) and patterns (inp)
- `inty`: 1 = type 1 (full seller+buyer data, buyer gets tax credit), 2 = type 2 (buyer data optional, no buyer credit, whole amount treated as cash, setm=1),
  3 = type 3 (POS/terminal payment receipt: only pay info + minimal seller data; has its own taxid, requires iinn, acn, trmn, trn, pcn, pid, pdt).
  (Paper invoice = non-electronic, not a JSON type.) Field is numeric length 1.
- `inp` (numeric, max 2 digits). Type 1 patterns:

| inp | Persian name | Notes |
|---|---|---|
| 1 | فروش | general sale |
| 2 | فروش ارز | only exchange houses/banks; item sstid restricted to the 3 FX service ids (banknote, remittance, remittance settlement) |
| 3 | صورتحساب طلا، جواهر و پلاتین | consfee, spro, bros, tcpbs mandatory |
| 4 | قرارداد پیمانکاری | `crn` mandatory |
| 5 | قبوض خدماتی | `billid` mandatory (max 19 digits) |
| 6 | بلیط هواپیما | `ft` mandatory; am must be 1; prdis = fee |
| 7 | صادرات | customs/FX fields; VAT rate 0 |
| 8 | بارنامه | transport; am must be 1 per row |
| 9 | فروش فرآورده‌های نفتی پالایش و پخش | only the national refining/distribution companies |
| 11 | بورس اوراق بهادار مبتنی بر کالا | commodity exchange; one item row only; VAT 0 |
| 13 | فروش خدمات بیمه‌ای | `in` mandatory |
| 14 | فروش زنجیره | chain sales per Art.17 VAT law note 5; VAT 0; buyer economic code (tinb) mandatory |

  Note: there is no 10 or 12. Type 2 invoices allow only inp 1, 3, 9, 13.
- Subject `ins` (numeric length 1): 1 = original (اصلی), 2 = correction (اصلاحی), 3 = cancel (ابطالی), 4 = return from sale (برگشت از فروش).
  For export (7), waybill (8) and commodity exchange (11) only 1, 2, 3 are allowed (no return).

### 1.3 Field dictionary: type, length, JSON location (table 2 + per-field tables)

Types: string; "numeric string" = digits 0-9 as string; double with max integer/decimal digits; Timestamp = unix integer.
All rial and foreign amounts must be computed by TRUNCATION (قطع کردن) to the stated decimals; general rule 1.

Header:
| json | meaning | type / length |
|---|---|---|
| taxid | unique tax number | string 22 (6 fiscal id + 5 date + 10 serial hex + 1 check; spec in RC_DCPS.SN) |
| indatim | issue datetime | unix millis, int 13 digits |
| indati2m | registration datetime | unix millis, int 13; only for Art.9 late invoices, else ignore |
| inty | type | int 1 |
| inno | internal serial | string 10, hex, optional, ascending |
| irtaxid | reference taxid | string 22; required iff ins in (2,3,4) |
| inp | pattern | int max 2 |
| ins | subject | int 1 |
| tins | seller economic code | numeric string 11 or 14 |
| tob | buyer person type | int 1: 1 real, 2 legal, 3 civil partnership, 4 non-Iranian |
| bid | buyer national id | numeric string: legal id 11, national code 10, civil partnership 11, foreigner code 12 |
| tinb | buyer economic code | numeric string 11 or 14 |
| sbc | seller branch code | numeric string 4 |
| bpc | buyer postal code | numeric string 10 |
| bbc | buyer branch code | numeric string 4 |
| ft | flight type | int 1: 1 domestic, 2 international (inp 6) |
| bpn | buyer passport no | string 9 |
| scln | customs licence no | numeric string max 14 |
| scc | customs office code | numeric string 5 |
| cdcn | customs cottage (کوتاژ) no | string max 14 |
| cdcd | customs cottage date | int 5 digits = unix seconds / 86400 truncated (days) |
| crn | contract no (inp 4) | numeric string max 12; must equal contract number in taxpayer portal |
| billid | subscription/bill id (inp 5) | numeric string max 19 |
| tprdis | sum prdis | double 18,0; must be > 0 |
| tdis | sum dis | double 18,0; >= 0 |
| tadis | sum adis | double 18,0; >= 0 |
| tvam | sum vam | double 18,0; >= 0 |
| todam | sum odam + olam | double 18,0; >= 0 |
| tbill | sum tsstam | double 18,0 |
| tonw | total net weight kg | double 15,8; > 0 when required |
| torv | total rial value (export) | double 18,0; > 0 |
| tocv | total FX value (export) | double 14,4; > 0 |
| setm | settlement method | int 1: 1 cash, 2 credit, 3 cash/credit |
| cap | cash amount | double 18,0 |
| insp | credit amount | double 18,0 |
| tvop | sum of vop | double 18,0; >= 0 |
| tax17 | Article 17 tax | double 18,0; 0 <= tax17 <= tvam |
| tinc | travel agency economic code | numeric string 11/14 (inp 6, optional) |
| lno, lrno | waybill no / reference waybill no | numeric string max 18 |
| ocu, oci, dco, dci | origin/destination country and city codes | tables 9-4/9-5 in the paper (country codes 3 digits e.g. 033 Iran) |
| tid, rid | sender / receiver id | numeric string |
| lt | waybill/transport type | |
| cno | fleet number | |
| did | driver national id | |
| sg[] {sgid, sgt} | carried goods (waybill) | array of id + description |
| asn, asd | commodity-exchange sale notice no / date (asd = days, 5 digits) | |
| in, an | insurance policy unique id / endorsement id | numeric string 9..12 |
| insr | submission rule | int max 2; 1 for Art.9 late invoices |
| nti1, nti2 | notes | string max 30 |

Body (per item):
| json | meaning | type |
|---|---|---|
| sstid | goods/service id | numeric string 13; must exist in stuffid.tax.gov.ir |
| sstt | description | string max 400, optional |
| am | quantity | double 18,8; > 0 |
| mu | unit code | numeric string max 8 (from RC_UMGS.ST table) |
| nw | net weight kg | double 15,8 |
| fee | unit price (rial) | double 18,8; > 0 |
| cfee | FX amount | double 14,4 |
| cut | currency | string 3, ISO 4217 |
| exr | rial-per-FX rate | double 18,8; > 0 |
| ssrv | rial value (customs) | double 18,0; > 0 |
| sscv | FX value (customs) | double 14,4; > 0 |
| prdis | amount before discount | double 18,0; > 0 |
| dis | discount | double 18,0; 0 <= dis <= prdis (write 0 if none) |
| adis | amount after discount | double 18,0; >= 0 |
| vra | VAT rate (percent, e.g. 9) | double 3,2; >= 0 (v1.3 sample uses 9, V01 sample used 0.09) |
| vam | VAT amount | double 18,0 |
| odt/odr/odam | other taxes/levies: subject (string 255)/rate (3,2)/amount | |
| olt/olr/olam | other legal charges: subject/rate/amount | |
| consfee, spro, bros, tcpbs | gold wage, seller profit, commission, total of these | double 18,0 |
| cop | cash share of payment per item | double 18,0 |
| vop | VAT share of payment per item | double 18,0 |
| bsrn | third-party-deal contract no | numeric string max 12 |
| tsstam | item total | double 18,0; >= 0 |
| cui | gold fineness (عیار) | double 4,2; 0 < cui <= 1000.00 |
| cpr | FX purchase rate (inp 2) | double 18,8; 0 < cpr < exr |
| sovat | VAT base (inp 2) | double 18,0 |
| vba | VAT base amount per litre (petroleum) | double 18,4 |

Payments array (`payments[]`, one object per transaction; mandatory fields only for inty 3, otherwise optional in patterns 1-9,13,14):
| json | meaning | type |
|---|---|---|
| iinn | payment switch no | numeric string 9 |
| acn | merchant (acceptor) no | numeric string 14 |
| trmn | terminal no | numeric string 8 |
| pmt | payment method | int max 2: 1 cheque, 2 barter/offset (تهاتر), 3 cash, 4 POS, 5 internet gateway, 6 card to card, 7 account transfer, 8 other |
| trn | tracking/reference no | numeric string max 14 |
| pcn | payer card no | numeric string 16 |
| pid | payer national id/code | numeric string max 12 |
| pdt | payment datetime | unix, precision DAY per paper (int 13 digits) |
| pv | paid amount | double 18,0; 0 <= pv <= tbill; = 0 when setm = 2 |

pv = total of payments made for the cash part, defined as cash amount + cash VAT share. One pv per transaction.
`extension[]` exists but fields "will be announced later" (V01 sample shows {key,value}).

### 1.4 Mandatory/conditional per pattern (derived from table 1, see matrix reading note)

Reading note: table 1 columns right-to-left are inp 1,2,3,4,5,6,7,8,9,11,13,14 (type 1) then type 2: 1,3,9,13. Checked against billid (only inp 5),
ft (inp 6), crn (inp 4), vba (inp 9), in (inp 13). R = mandatory, C = conditionally mandatory, o = optional, "-" = ignored (value is discarded by the server).

Common to ALL patterns: R taxid, indatim, inty, inp, ins, tins, tvam, tbill, setm (setm is "-" for type 2/3 only if that column; type 2 columns also R), sstid, am, vra (except inp 9: C), vam, tsstam; C: indati2m (late Art.9), irtaxid (ins 2/3/4), insr (late), odt/odr/odam/olt/olr/olam (R when "other tax" applies), cap and insp (R iff setm=3).
Common optional: inno, tdis, tvop, tax17, sstt, mu, sbc, bpc, bbc, cop, vop, bsrn, iinn, acn, trmn, pmt, trn, pcn, pid, pdt, pv, nti1, nti2.

Pattern specific:
- inp 1 sale: R tob, tprdis, tadis, todam, fee, prdis, dis, adis. C: tinb. o: bid, scln, scc, crn, cfee, cut, exr.
- inp 2 FX sale: R tob, tprdis, tadis, todam, cfee, cut, exr, prdis, dis, adis, cpr, sovat (am is NOT required; mu is not used). C: bid (real/foreigner buyer), tinb (legal), bpn (foreigner without code), cap, insp.
- inp 3 gold: R tob, tprdis, tadis, todam, fee, prdis, dis, adis, consfee, spro, bros, tcpbs. o: cui, cop, vop. C: tinb.
- inp 4 contracting: R tob, crn, tprdis, tadis, todam, fee, prdis, dis, adis. C: tinb.
- inp 5 utility: R tob, billid, tprdis, tadis, todam, fee, prdis, dis, adis. o: tinb.
- inp 6 airline: R tob, ft, tprdis, tadis, todam, fee, prdis, dis, adis. C: bid, bpn. o: tinb, tinc.
- inp 7 export: R tins, tvam, todam, tbill, torv, tocv, setm, sstid, am, cut, exr, ssrv, sscv, vra, vam, tsstam. NOT required: tob, tprdis, tadis, prdis, dis, adis, fee (ignored, "-" or optional). C: tonw, nw (required when item is goods, optional for service items). o: tob, bid, tinb, bpn, scc, cdcn, cdcd, mu, fee, tvop, cop, vop. setm may be 1, 2 or 3 for export.
- inp 8 waybill: R tob, lno, ocu, dco, tid, lt, cno, sg{sgid,sgt}, tprdis, tadis, todam, fee, prdis, dis, adis. C: tinb, oci, dci, rid, did, lrno (o). am must be 1.
- inp 9 petroleum: R as sale; vra is C; C vba (for items under the 1404 budget law). inp 9 type 2 also has vra C.
- inp 11 commodity exchange: R tins, tadis, tvam, tbill, setm, sstid, am, mu, adis, vra (0), vam, tsstam, asn, asd. No prdis/dis/fee/tprdis. One item row only. o: tob, bid, tinb, cui.
- inp 13 insurance: R as sale + `in`; o: `an`.
- inp 14 chain: R as sale + tinb (buyer economic code); seller and buyer must be in the chain list.
- Type 2 (any pattern): buyer fields o/ignored; tob/tinb not required; same body requirements; all treated as cash.
- Cash-share fields `cop`, `vop` and `tvop` are optional in every pattern.

### 1.5 Subject rules: original / correction / cancel / return (sections 5, 5-x, table 10)

Terms: "reference invoice" (مرجع) = the invoice pointed to by irtaxid; "referring invoice" (ارجاعی) = correction/return/cancel invoice.
General:
- irtaxid (string 22) REQUIRED iff ins in {2,3,4}; must be a valid existing taxid; reference may not itself be a cancel invoice; reference must be in
  the same taxpayer inbox (کارپوشه); the same taxid cannot be reference of a correction and a return simultaneously; each invoice can be the reference
  of only one cancel invoice. Referring invoice issue datetime >= reference issue datetime.
- A correction/return/cancel must be within the time windows announced by the tax authority.
- Buyer data (tinb, bid, bbc, bpc, billid) and sstid, inty, inp can never be changed in a correction/return (to change them: cancel + new invoice).
  Therefore for ins 2/3/4 ALL buyer info is "out of pattern"; for utility bills billid is also out of pattern.
- ins 2 CORRECTION: same inty and inp as reference; carries the reference taxid; cannot change buyer data, sstid, VAT rate. Can change quantity per row:
  increasing quantity is allowed (or issue a new original instead); decreasing is allowed only when the original quantity was entered wrongly (otherwise use return).
  New items (new sstid) cannot be added in a correction.
  If the reference is itself a correction/return it must be in status confirmed/system-confirmed/no-reaction-needed and have no other non-cancelled correction/return on it.
- ins 4 RETURN: same inty and inp as the reference; contains the items sold MINUS the returned items (i.e. full remaining invoice, not a delta); quantity or number of rows must be lower
  than the reference; VAT rate not changeable; per-item amounts (fee, prices) cannot change vs the reference. If everything is returned, a CANCEL must be issued instead.
- ins 3 CANCEL: reasons: buyer data changed, all items returned, no real deal, wrong deal date. inty and inp are "out of pattern", and buyer data and all body items are
  fetched from the reference (not needed in the JSON). A cancel can be issued on any reference status, or on a referring invoice that is pending/rejected.
  If the referring invoice is confirmed, its reference counts as cancelled and cannot be used as reference again. A cancelled reference voids its unconfirmed corrections/returns.
- Export (7) with partial return and no correction invoice: penalties under Art. 9 apply. Commodity-exchange (11): an original can be corrected only if it carries the "unconfirmed" label
  (not matched to the exchange notice); if "confirmed", only a cancel can reference it. Export correction date must be >= cottage date.
- Sign of amounts: nothing in the paper makes amounts negative; all amounts stay positive (rules say prdis>0, dis>=0 etc., return = remaining items). No negative values are defined.

### 1.6 Arithmetic rules (OFFICIAL)

Notation: Es=prdis, Gs=dis, Is=adis, Ks=vam, Os=tsstam, J=vra, Ks2=odam, Ks3=olam, Xs=tbill, W=tvam, W2=todam, C=cap, Cr=insp, Vs=tadis, RV=ssrv, TAs=tcpbs.
- Default item: prdis = am * fee (truncate to 0 decimals). adis = prdis - dis. vam = adis * vra / 100.
  odam = adis * odr / 100; olam = adis * olr / 100 (when rate given, else a fixed number is allowed).
  tsstam = vam + adis + odam + olam. If VAT rate = 0 then vam, odam, olam are 0 (amount fields may be absent when item not subject to VAT).
- Totals: tprdis = sum prdis; tdis = sum dis; tadis = sum adis; tvam = sum vam; todam = sum(odam) + sum(olam); tbill = sum tsstam;
  tonw = sum nw; torv = sum ssrv; tocv = sum sscv; tvop = sum vop.
- Gold (inp 3): tcpbs = consfee + bros + spro. adis = prdis + tcpbs - dis. vam = tcpbs*10/100 + prdis*vra/100 (wage VAT always 10 percent).
  odam = tcpbs * odr / 100; olam = tcpbs * olr / 100. Constraints: consfee < prdis, each of consfee/bros/spro <= tcpbs, tcpbs >= each of them.
- Currency sale (inp 2): prdis = cfee * exr. dis <= cfee*exr. vam = sovat * vra / 100 with cpr*cfee <= ... i.e. cfee*(exr-cpr) <= sovat <= cfee*exr; sovat is computed after subtracting other taxes/levies.
  Non-FX patterns: cfee = fee / exr.
- Airline (inp 6): prdis = fee (am = 1).
- Export (inp 7): tsstam = ssrv + vam + odam + olam; tbill = torv + tvam + todam; fee (optional) = ssrv / am; odam = ssrv * odr / 100, olam = ssrv * olr / 100; vra = 0 for export, bourse and chain.
  tocv and sscv with 4 decimals, cut ISO 4217 and must match the customs declaration; cdcn optional (but needed for automatic zero-rate exemption).
- Petroleum (inp 9) with base amount: vam = am * vba; else vam = adis*vra/100.
- Settlement: setm 1 cash, 2 credit, 3 cash/credit. Type 2 and 3 invoices: setm = 1 always.
  For setm = 3: cap and insp are both mandatory and, per paper formulas, cap = tbill - todam - tvam - insp and insp = tbill - todam - tvam - cap,
  i.e. cap + insp = tbill - tvam - todam (the cash and credit amounts are the PRE-TAX part; taxes are settled in cash), both > 0 and each < tbill.
  Also tbill >= cap + insp (checks).  tvop = sum of per-item vop where vop = tsstam-level share: vop = vam * cap / tadis; cop = tsstam * cap / tadis
  (multiply first then divide, truncate); tvop <= ... both >= 0, cop <= tbill. If the VAT rate is 0 then vop = 0.
  Caveat: the paper's formula for cop uses Os (item total) over Vs (invoice tadis), exactly as printed; implement as written and compare against the
  system's validation errors in sandbox.
- pv per payments transaction: 0 <= pv <= tbill; pv = 0 when setm = 2; in cash/credit invoices total pv should equal the cash part (cap + tvop).
- Invoice with setm 1 (cash): paper has no "cap = tbill" statement, cap/insp are "out of pattern" (ignored) unless setm=3. The V01 sample (cash) sets cap=90000?, which does not match the V7.9 formula (see 4.2).

### 1.7 Misc constraints
- indatim: unix ms, must be <= now, >= reference invoice time, must equal the date embedded in taxid; for export >= cottage date; waybill >= waybill date; bourse >= asd.
- If time from issue to submission exceeds the authority's allowed grace period: insr = 1 and indati2m (registration time >= indatim, <= now) required (Art. 9 invoice).
  Within the grace period insr and indati2m are ignored.
- Seller economic code mandatory and matching the taxpayer; type 1 real/foreign buyer requires tinb OR (bid + bpc); legal/civil-partnership buyer needs only tinb.
- Foreigner airline passenger without residency: bpn required. Domestic flights require passenger identity.
- FX pattern: legal buyer requires tinb; foreigner without code requires bpn; real buyer requires national id.
- Customs cottage date is days since epoch (unix / 86400, 5 digits, truncated); asd and pdt likewise day precision per paper text.

## 2. Pattern summary for quick Go modelling
(see 1.2 for numbers). The JSON skeleton in appendix 9-3 is: `{"header":{...},"body":[{...}],"payments":[{...}],"extension":[{}]}`.
Field order in the paper's JSON skeleton matches table 2 order (taxid, indatim, indati2m, inty, inno, irtaxid, inp, ins, tins, tob, bid, tinb, sbc, bpc, bbc, ft, bpn, scln, scc, cdcn, cdcd(int), crn, billid, tprdis, tdis, tadis, tvam, todam, tbill, tonw, torv, tocv, setm, cap, insp, tvop, tax17, tinc, lno, lrno, ocu, oci, dco, dci, tid, rid, lt, cno, did, sg[], asn, asd, in, an, insr, nti1, nti2).

## 3. Technical papers (OFFICIAL)

### 3.1 RC_TICS.IS_V01 (Khordad 1401), the API the user implemented (source D)
- Base: `https://tp.tax.gov.ir/req/` then `api/self-tsp/...` (taxpayers sending their own invoices) or `api/tsp/...` (licensed TSP companies).
- Methods (suffix after `/api/self-tsp/`): `async/normal-enqueue/` and `async/fast-enqueue/` (send invoices; list of packets),
  `sync/GET_TOKEN/`, `sync/GET_FISCAL_INFORMATION/`, `sync/INQUIRY_BY_UID/`, `sync/INQUIRY_BY_REFERENCE_NUMBER/`, `sync/INQUIRY_BY_TIME/`,
  `sync/INQUIRY_BY_TIME_RANGE/`, `sync/GET_SERVER_INFORMATION/`, `sync/GET_SERVICE_STUFF_LIST/`, `sync/GET_ECONOMIC_CODE_INFORMATION/`.
- Request envelope: HTTP POST; headers `Authorization`, `requestTraceId` (UUID, duplicate detection), `timestamp` (client time, ms, to reject stale packets).
  Body: `{"packets":[Packet...], "signature":"...", "signatureKeyId":"..."|null}`. Sync variant sends a single `packet` instead of `packets`.
  Response: `{"timestamp", "result":[AsyncResponse...], "signature", "signatureKeyId"}` or on global error `{"timestamp","errors":[ErrorResponse], "signature","signatureKeyId"}`.
- Packet: `{uid, packetType (e.g. "INVOICE.V01"), retry (bool, true for resend after FAILED), data, encryptionKeyId, symmetricKey, iv, fiscalId, dataSignature}`.
  `fiscalId` mandatory when the sender is a TSP company; encryption fields empty when data is plain.
- Signature: JSON invoice is NORMALISED to a `#`-joined string of its field values in a fixed order (null -> literal "null"? sample shows empties as empty between #), then RSA2048 + SHA256 signature
  placed in `dataSignature` (appendix 3-1 of V01).
- Encryption: AES-256 key + 128-bit IV (V01 text) with AES/GCM/NoPadding (the paper also describes an XOR step with the symmetric key first, sample shows IV hex),
  symmetric key encrypted with RSA-OAEP-SHA256 using org public key (4096 bit, obtained through GET_SERVER_INFORMATION; `encryptionKeyId` = key id).
- The sample in V01 shows `"vra": 0.09` (fraction) while V7.9 and v1.3 use percent (9).

### 3.2 RC_TICS.IS_v1.3 (Mehr 1402), "API v2" (source C)
- Base: `https://tp.tax.gov.ir/requestsmanager/api/v2/...`. Per the paper, v1.2 (old) still works "until further notice" and is documented in an appendix.
- Endpoints (all need `Authorization: Bearer <JWT>` except nonce):
  - GET `/nonce?timeToLive=N` (10..200 s, default 30) -> `{"nonce","expDate"}` (no auth).
  - GET `/server-information` -> `{"serverTime"(ms),"publicKeys":[{"key"(base64 X.509 RSA 4096),"id"(uuid),"algorithm":"RSA","purpose":1}]}`.
  - GET `/fiscal-information?memoryId=A11216` -> `{nameTrade, fiscalStatus, saleThreshold, economicCode}`.
  - GET `/taxpayer?economicCode=...` -> `{nameTrade, taxpayerStatus, taxpayerType, postalcodeTaxpayer, addressTaxpayer}`.
  - POST `/invoice` : body = JSON array (max 1000) of `{"header":{"requestTraceId":uuid,"fiscalId":"A11216"},"payload":"<JWE compact string>"}`.
    Response `{"timestamp":ms,"result":[{"uid":<requestTraceId>,"packetType":null,"referenceNumber":uuid,"data":null}]}`.
    requestTraceId must be unique within one request (else the request fails); a TSP may mix many fiscal ids in one request.
  - GET `/inquiry-by-reference-id?referenceIds=..&referenceIds=..&start=..&end=..` (start/end optional, default last 24h, max window one week; ISO with nanos e.g. `2023-05-14T00:00:00.000000000+03:30`).
  - GET `/inquiry-by-uid?uidList=..&fiscalId=..&start=..&end=..`.
  - GET `/inquiry?start=..&end=..&pageNumber=1&pageSize=10`.
  - Inquiry result item: `{referenceNumber, uid, status (SUCCESS|FAILED|PENDING|NOT_FOUND), data (errors/warnings list), packetType, fiscalId}`.
- Auth token (per request, single use): fetch nonce, build compact JWS with header
  `{"alg":"RS256","x5c":[<base64 DER cert>],"sigT":"yyyy-MM-dd'T'HH:mm:ss'Z'" (UTC; local form like 2023-05-13T14:14:47Z+0330 permitted),"typ":"jose","crit":["sigT"],"cty":"text/plain"}`
  and payload `{"nonce":"...","clientId":"<fiscal id for taxpayers, TSP id for companies>"}`; RSASSA-PKCS1-v1_5 with SHA-256; send `Authorization: Bearer <jws>`.
  Certificate national/legal id (in SUBJECT SERIALNUMBER) must match the clientId owner; server checks chain, expiry, CRL, OCSP. Key >= 2048-bit.
- Invoice packet: JWS (same header/format as the token, payload = invoice JSON {header,body,payments}) then JWE:
  header `{"alg":"RSA-OAEP-256","enc":"A256GCM","kid":<server key id>}`; random 256-bit AES key (encrypted key = 512 bytes base64url, RSA-4096 OAEP SHA-256/MGF1 SHA-256), 96-bit IV (16 base64url chars),
  AAD = ASCII(base64url(header)), content = the JWS compact string; compact JWE = 5 parts (header.encKey.iv.ciphertext.tag).
- Signed JWS invoice sample was generated with jose4j 0.9.3 (Java) and jose-jwt/JWT (.NET).
- Error tables exist (auth errors, send errors, inquiry errors, other) at the end of the paper (codes not transcribed here).
- Note: no `retry`, `symmetricKey`, `iv`, `dataSignature`, `signatureKeyId` fields exist in v1.3 packets; no `requestTraceId`/`timestamp` HTTP headers (requestTraceId moves into each packet header).

### 3.3 v1.4 - v1.6
NOT FOUND (see section 6).

## 4. Official numeric samples
### 4.1 v1.3 sample invoice (source C, pattern 1, type 1, original)
header: taxid A1121604C220002F095011, inno "49321217", indatim 1683997837988, inty 1, inp 1, ins 1, tins "14003778990", tob 2, bid "10100302746", tinb "10100302746",
tprdis 20000, tdis 500, tadis 19500, tvam 1755, todam 0, tbill 21255, setm 2.
body[0]: sstid "2710000138624", mu "164", am 2, fee 10000, prdis 20000, dis 500, adis 19500, vra 9, vam 1755, tsstam 21255. payments: [].
Arithmetic: 2*10000=20000; 20000-500=19500; 19500*9/100=1755; 19500+1755=21255. A credit sale (setm 2) with no cap/insp, consistent with the rules (cap/insp only for setm 3).
### 4.2 V01 sample (source D, pattern 1, cash)
tprdis 1000000, tdis 0, tadis 1000000, tvam 90000, todam 0, tbill 1090000, setm 1, cap 90000, insp 0, tvop 90000; body: am 1, fee 1000000, vra 0.09, vam 90000, tsstam 1090000, cop 90000, vop 90000.
Declared by the paper as fake/test data and its cap/cop values (90000) do not follow the V7.9 formula (cap = tbill - todam - tvam - insp = 1000000). Do NOT use as a test oracle for cap/cop/vop.

## 5. Recommended Go test vectors (derived strictly from the V7.9 formulas, truncation)
- Sale: am 3, fee 12345, dis 100, vra 9: prdis 37035, adis 36935, vam 3324 (36935*9/100=3324.15), tsstam 40259.
- Gold: prdis 10_000_000, consfee 500_000, bros 0, spro 300_000 -> tcpbs 800_000; dis 0; vra 9; adis 10_800_000; vam = 800_000*10/100 + 10_000_000*9/100 = 80_000 + 900_000 = 980_000; tsstam = 11_780_000.
- Mixed setm 3: tbill 1_090_000, tvam 90_000, todam 0, cap 600_000 -> insp 400_000 (cap + insp = tbill - tvam - todam = 1_000_000).
- Export: ssrv 5_000_000, am 10, vra 0, vam 0, odr 1 -> odam 50_000, tsstam = ssrv + vam + odam + olam = 5_050_000.
(These are computed examples, not from the paper.)

## 6. NOT found / uncertain
- RC_TICS.IS v1.4, v1.5, v1.6: not found anywhere reachable. No diff vs v1.3 available. intamedia.ir and tp.tax.gov.ir are unreachable (timeouts) from this host.
- The V7.8 PDF itself was not fetched (only its change log relative to V7.9). The rima.tax 113-page edition was downloaded but not read (older edition).
- No official numeric worked invoice example exists in the IITP paper (only blank templates).
- Not transcribed: country/city code tables (9-4, 9-5), error code tables of the technical papers, details of waybill/insurance/chain/petroleum special rules beyond the lines above,
  V01 normalisation order of fields and appendix code (needed only if the old API is kept), v1.2 appendix of the v1.3 paper.
- Open items to double check against the sandbox: exact semantics of cop/vop for items with different tax rates, rounding of cop/vop shares, whether setm 1 requires cap/insp = ignore,
  and whether "pdt day precision" in the paper is a typo (indatim is ms).
