// Package moadian is the einvoice.Provider for the Iranian tax authority's Moadian system (سامانه مودیان). It follows
// the official document "دستورالعمل فنی اتصال به سامانه مودیان", id RC_TICS.IS_v1.3, Mehr 1402, which describes the second
// version of the collection web service (https://tp.tax.gov.ir/requestsmanager/api/v2):
//
//   - every call is authenticated with a JWS token over a one-time nonce (GET nonce) signed with the taxpayer's key;
//   - an invoice is signed as a JWS (RS256) and then encrypted as a JWE (RSA-OAEP-256 + A256GCM) with the public key
//     from GET server-information, and POSTed as {payload, header{requestTraceId, fiscalId}};
//   - the verdict is read with the inquiry endpoints (SUCCESS, FAILED, PENDING, NOT_FOUND).
//
// What the document does NOT define, and this package therefore does not guess: the field dictionary of the invoice JSON
// (taxid, inno, indatim, inty, ...) and the algorithm of the unique tax number (TaxId). The document points to two other
// papers for them ("دستورالعمل صدور صورتحساب الکترونیکی" and "قالب شناسه یکتا حافظه مالیاتی و شماره منحصر به فرد مالیاتی").
// They enter through PayloadBuilder, which you implement from those papers.
package moadian
