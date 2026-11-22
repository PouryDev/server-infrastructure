# Payment integration evidence

Researched 2026-11-15. This file describes external contracts, not certification or a completed live payment test. No production credentials were used. Monetary values below are integer IRR; display in toman requires exactly one conversion. Do not collect or send email.

## Readiness

| Provider | Primary-source evidence | Remaining live prerequisites |
| --- | --- | --- |
| Zibal | Official API reference and Go sample | Merchant, registered domain/IP, complete sandbox/live tests |
| Zarinpal | Official v4 documentation | Merchant UUID, registered callback, sandbox/live tests |
| SnappPay | Official self-CMS v2.1 documentation | Merchant-issued environment origin and four credentials; IP/callback allowlisting; Stage demonstration and production approval |
| TorobPay | Provider-authored WooCommerce plugin source | Merchant account, environment origin and four credentials; acceptance tests |
| DigiPay | Official UPG merchant documentation | Merchant OAuth credentials, approved features and acceptance tests |
| Tara | Provider-authored Tara360 plugin source | Username/password, service identifier, category mapping where required and acceptance tests |
| Azkivam | Official API 1.0.0 documentation, Postman collection and provider-hosted WooCommerce plugin | Merchant username/password or signing key, merchant ID and acceptance tests |

Provider credentials being absent prevents live acceptance verification; it does not prevent implementing adapters and mock-server contract tests. All seven providers now have primary-source contracts identified. A tested adapter must still not be described as production-certified without the provider's required acceptance procedure.

## Zibal

Sources: [official API reference](https://github.com/zibalco/zibal-payment-skill/blob/main/references/api.md), [official result codes](https://github.com/zibalco/zibal-payment-skill/blob/main/references/result-codes.md), [official Go sample](https://github.com/zibalco/gateway-golang-sample).

Base `https://gateway.zibal.ir`; JSON POST, authentication in body. Sandbox uses merchant `zibal` on the same host.

```json
POST /v1/request
{"merchant":"<merchant>","amount":100000,"callbackUrl":"https://shop.example/payments/zibal/callback","orderId":"order-123","description":"Order 123","mobile":"09123456789"}
```

Accept request only with `result=100` and valid positive `trackId`. Redirect to `/start/{trackId}`; preserve browser Referer. Standard callback is GET with `trackId`, `success`, `status`, optionally `orderId`; callback alone cannot establish payment.

```json
POST /v1/verify
{"merchant":"<merchant>","trackId":123456789}
```

Verify response: `result`, `status`, `amount`, `refNumber`, `orderId`, `paidAt`. For `result=100`, compare saved amount/order and require successful state before fulfillment. For `201` (already verified), POST `/v1/inquiry` with the same body and recheck amount/order/status. `202` means unpaid/failed; `203` invalid track ID. HTTP 200 is not business success. Never mix standard endpoints with lazy `/verify`.

## Zarinpal

Sources: [connection](https://www.zarinpal.com/docs/paymentGateway/connectToGateway), [sandbox](https://www.zarinpal.com/docs/paymentGateway/sandBox), [reverse](https://www.zarinpal.com/docs/paymentGateway/moreFeatures/reverse).

Production base `https://payment.zarinpal.com`; sandbox `https://sandbox.zarinpal.com`. JSON POST:

```json
POST /pg/v4/payment/request.json
{"merchant_id":"<uuid>","amount":100000,"currency":"IRR","description":"Order 123","callback_url":"https://shop.example/payments/zarinpal/callback","metadata":{"mobile":"09123456789","order_id":"order-123"}}
```

Require `data.code=100` and valid `data.authority`; persist it. Redirect to `/pg/StartPay/{authority}`. Callback GET supplies `Authority` and `Status=OK|NOK`.

```json
POST /pg/v4/payment/verify.json
{"merchant_id":"<uuid>","amount":100000,"authority":"<saved-authority>"}
```

Use saved amount/authority. `data.code=100` is first verification; `101` means previously verified. Both require local payment binding and idempotent fulfillment. Store `data.ref_id`; do not rely on HTTP status or redirect alone. Errors may be objects while successful `errors` is an array.

Reverse: POST `/pg/v4/payment/reverse.json` with `merchant_id`, `authority`; successful `data.code=100`. This is limited to 30 minutes and requires registered server IP; it is not a general refund API. Sandbox permits an arbitrary UUID merchant and authorities begin with `S`.

## SnappPay

Source: [official self-CMS documentation](https://academy.snapppay.ir/self-cms/). Origin is merchant-issued, not guessed.

OAuth POST `/api/online/v1/oauth/token`: Basic `client_id:client_secret`; URL-encoded `grant_type=password`, `scope=online-merchant`, `username`, `password`. Subsequent requests use Bearer. Eligibility GET `/api/online/offer/v1/eligible?amount=<IRR>` returns `successful`, `response.eligible`, `title_message`, `description`; display those dynamic texts unchanged and refresh after price changes.

Token POST `/api/online/payment/v1/token`:

```json
{"amount":100000,"discountAmount":0,"externalSourceAmount":0,"mobile":"09123456789","returnURL":"https://shop.example/payments/snapppay/callback","transactionId":"order-a123","cartList":[{"cartId":123,"totalAmount":100000,"cartItems":[{"id":1,"name":"Product","category":"Category","count":1,"amount":100000,"commissionType":100}],"isShipmentIncluded":true,"isTaxIncluded":true,"shippingAmount":0,"taxAmount":0}]}
```

Success returns `response.paymentToken`, `paymentPageUrl`. Callback POST: `transactionId`, `state`, `amount`. POST `verify`, then mandatory `settle`, each under `/api/online/payment/v1/`, body `paymentToken`. On timeout query GET `status?paymentToken=...`: `VERIFY` permits settle; `PENDING` permits verify retry; only `SETTLE` is final. Cancel and cart Update are mandatory operations. Stage technical approval precedes production credentials.

## TorobPay

Sources: [provider plugin identity](https://wordpress.org/plugins/torobpay-for-woocommerce/), [API source](https://plugins.svn.wordpress.org/torobpay-for-woocommerce/trunk/class-torobpay-api.php), [checkout source](https://plugins.svn.wordpress.org/torobpay-for-woocommerce/trunk/class-wc-gateway-torobpay.php).

Default base `https://cpg.torobpay.com/`. OAuth is POST `api/online/v1/oauth/token`, Basic client ID/secret and URL-encoded username/password, grant type `password`, scope `online-merchant`. Response `access_token`, `expires_in`; subsequent requests Bearer.

Eligibility GET `api/online/offer/v1/eligible` with IRR amount. Token POST `api/online/payment/v1/token` includes `amount`, `paymentMethodTypeDto=INSTALLMENT`, `returnURL`, unique `transactionId`, customer mobile/address and `cartList` with real line items, shipping/tax, `discountAmount`. Token response uses `successful` and nested `response`; retain payment token and returned payment URL.

POST `api/online/payment/v1/verify`, `settle`, `cancel` use `{"paymentToken":"..."}`. GET `status` takes `paymentToken`. Update sends revised amount/cart/discount and paymentToken. Official checkout explicitly calls Settle and can recover already-settled status; do not assume automatic settlement based on third-party SDK descriptions. Recheck ambiguous state before minting another token. The provider's current plugin also checks returned amount before redirecting.

## DigiPay

Source: [official merchant UPG documentation](https://www.mydigipay.com/developers/docs/upg/). At research time urllib could retrieve it although the browser search tool could not.

Production base `https://api.mydigipay.com/digipay/api`; Stage `https://uat.mydigipay.info/digipay/api`. OAuth POST `/oauth/token`: Basic client ID/secret, form `username`, `password`, `grant_type=password`; refresh uses `grant_type=refresh_token`, `refresh_token`. Bearer thereafter.

POST `/tickets/business?type=11`, headers `Agent: WEB`, `Digipay-Version: <implementation yyyy-MM-dd>`, JSON:

```json
{"cellNumber":"09123456789","amount":100000,"providerId":"order-123","callbackUrl":"https://shop.example/payments/digipay/callback","basketDetailsDto":{"basketId":"basket-123","items":[{"productCode":"SKU-1","brand":"Brand","productType":1,"count":1}]}}
```

Require `result.status=0`, ticket and redirectUrl. Callback normally POST (cancel may GET empty): `providerId`, `trackingCode`, `amount`, `result`, `type`. Actual BNPL type is `13`, credit `5`; initial UPG type `11` is not the purchase type. POST `/purchases/verify?type=<callback-type>` with trackingCode/providerId, matching response amount/providerId. Verification is idempotent; unverified successful payments reverse after three minutes.

On actual delivery, CREDIT/BNPL require POST `/purchases/deliver?type=...` with deliveryDate epoch milliseconds, invoiceNumber, trackingCode, products. Refund and refund inquiry are separate operations; retain their own idempotency identifier.

## Tara

Sources: [provider plugin](https://wordpress.org/plugins/tara360-gateway/), [API source](https://plugins.svn.wordpress.org/tara360-gateway/trunk/includes/class-wc-gateway-tara360-api.php), [checkout source](https://plugins.svn.wordpress.org/tara360-gateway/trunk/includes/class-wc-gateway-tara360.php), [base/redirect source](https://plugins.svn.wordpress.org/tara360-gateway/trunk/tara360-gateway.php).

Base **`https://pay.tara360.ir/pay`**. POST `/api/v2/authenticate`, JSON username/password; require HTTP 200 and `accessToken`. Bearer requests thereafter.

POST `/api/getToken`:

```json
{"additionalData":"Order 123","mobile":"09123456789","callBackUrl":"https://shop.example/payments/tara/callback","amount":100000,"vat":0,"serviceAmountList":[{"serviceId":"<merchant-service>","amount":100000}],"taraInvoiceItemList":[{"name":"Product","code":"SKU-1","count":1,"unit":5,"fee":100000,"data":"Product details"}],"ip":"<visitor-ip>"}
```

Response contains `token`. Redirect is a browser **POST form** to `https://pay.tara360.ir/pay/api/ipgPurchase` with `username`, `token`. Callback POST carries `token`, `result`, `channelRefNumber`; require order/token binding. For callback result `0`, POST `/api/purchaseVerify` body token/ip. Success strictly HTTP 200, body result `0`, nonempty rrn. Refresh authentication on 401. The examined official plugin does not establish a general refund endpoint; do not invent one.

## Azkivam

Sources: [official documentation](https://api-docs.azkivam.com/), [authentication](https://api-docs.azkivam.com/sec/authentication.html), [request flow](https://api-docs.azkivam.com/sec/flow.html), [API reference](https://api-docs.azkivam.com/api.html), [Postman collection](https://api-docs.azkivam.com/merchant-api.json), [provider-hosted WooCommerce plugin](https://api-docs.azkivam.com/plugins/wp/azkivam-woocommerce-payment-gateway.zip).

The public documentation is noindex and loads its sections with JavaScript; inspect the linked section pages when a search fetch returns only the navigation shell. [Getting started](https://api-docs.azkivam.com/sec/get-started.html) identifies API version 1.0.0 and the current base **`https://api.azkivam.com`**.

Authentication POST `/auth/authenticate`, JSON username/password, returns `accessToken`; use Bearer on payment requests. The documented alternative is MerchantId plus Signature: hex-encoded AES-CBC/PKCS7 ciphertext, zero 16-byte IV, hex-decoded ApiKey as key. Plaintext combines relative path, epoch timestamp, uppercase method, ApiKey with `#` separators. Implement one explicitly selected authentication method; do not probe guessed endpoints or switch modes silently.

Purchase POST `/payment/purchase`:

```json
{"amount":150000,"redirect_uri":"https://shop.example/payments/azkivam/callback","fallback_uri":"https://shop.example/payments/azkivam/callback","provider_id":127519873045,"mobile_number":"09123456789","merchant_id":"<merchant-id>","items":[{"name":"Product","count":3,"amount":50000,"url":"https://shop.example/products/product"}]}
```

`provider_id` must be a random numeric identifier, retained for the attempt; a prefixed order UUID is incompatible. Amount is IRR and the sum of item quantities times unit amounts must equal it. Require outer `rsCode=0`, nonempty `result.ticket_id` and `result.payment_uri`; save the ticket and redirect to the returned URI.

The official plugin shows callback GET `status=Done`, `ticketId`; verify actual state server-side. POST `/payment/status` with `{"ticket_id":"<saved-ticket>"}` returns outer `rsCode`, then `result.rsCode`, `result.status`, `result.ticket_id`. Statuses: 1 Created, 2 Verified, 3 Reversed, 4 Failed, 5 Canceled, 6 Settled, 7 Expired, 8 Done, 9 Settle Queue. Created tickets expire after ten minutes. Done payments require verification; lack of verification eventually reverses them.

POST `/payment/verify` with the same ticket_id body. Success example is outer `rsCode=0`, inner `result.rsCode=0`, matching ticket_id, status `2`. A subsequent verify may return `rsCode=28` for an invalid verification state; recover by querying status rather than treating every `28` as success. Successful final states must be tied to the saved attempt. These response examples do not include amount, so do not invent an amount comparison field.

Reverse POST `/payment/reverse` with ticket_id and the saved numeric provider_id. Authentication docs additionally demonstrate `reason=regret`. First success has outer rsCode `0` and ticket_id; repeat reverse may have inner rsCode `16`. Cancel POST `/payment/cancel` uses **camelCase** `{"ticketId":"<saved-ticket>"}`; success returns `result.fallbackUri`. Unknown tickets use HTTP 404/rsCode `2`. Done and Verified can reverse; Settled cannot. Do not report a local cancellation as completed refund without provider confirmation.

The older generated API examples still mention api.azkiloan.com and inconsistent Basic/text-plain sample headers. Prefer the explicit current getting-started and authentication sections above; keep those inconsistencies recorded instead of copying the obsolete examples.

## Cross-provider implementation requirements

- Bind every provider token to an immutable local attempt, order, amount, currency and gateway configuration revision. Never use callback price or a browser success message as truth.
- Persist unknown/verification/settlement stages. Network timeout is an unknown outcome, not proof of failure; avoid creating another charge until reconciled.
- Fulfill once using a database transaction/unique constraint. Repeated callbacks and retries must not duplicate stock movements, coupon redemption or analytics purchases.
- Evaluate merchant BNPL allow/deny rules against every product/variant before provider eligibility. Recheck after cart, shipping or discounts change. Explain unavailable methods and offer usable alternatives.
- Merchant and developer admin may configure gateways; secrets remain server-side and masked on reads. Demo payments must be clearly labeled and cannot be enabled in production implicitly.
- Credentials, IP allowlisting, public callback routing and provider approval are external prerequisites. `shoptest.localhost` verifies the local shop flow; it is not a provider acceptance environment reachable from the public internet.

## Provider-specific arithmetic

SnappPay cart totals are gross item totals plus separately charged shipping/tax; the root payable amount subtracts global discounts. TorobPay's current official WooCommerce source instead sends net cart/root totals alongside gross item subtotals and a global discount; its payload is deliberately distinct. Tara invoice fees include line discounts and shipping as a separate row; integer remainder allocation must make invoice item sums exactly equal payable IRR. DigiPay's redirectUrl already includes the ticket and is used unchanged.
