package moadian

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PouryDev/server-infrastructure/mizan/einvoice"
)

// DefaultBaseURL is the production address in the document. The document names no sandbox: set Config.BaseURL for one.
const DefaultBaseURL = "https://tp.tax.gov.ir/requestsmanager"

// Limits from the document: 1000 invoices per send, 100 per inquiry.
const (
	maxSubmit  = 1000
	maxInquire = 100
	maxWindow  = 7 * 24 * time.Hour // an inquiry window is at most a week
)

// iranTime is the zone of the inquiry time format (+03:30 in the document's examples).
var iranTime = time.FixedZone("+0330", 3*3600+30*60)

const inquiryLayout = "2006-01-02T15:04:05.000000000-07:00"

// PayloadBuilder turns the neutral invoice into the authority's invoice JSON (header, body, payments), including its unique
// tax number. It is the part the connection document leaves to the invoice-issuing instructions; implement it from those.
type PayloadBuilder interface {
	Build(inv einvoice.Invoice, traceID string) ([]byte, error)
}

// Config of the client.
type Config struct {
	BaseURL string // default DefaultBaseURL
	// ClientID identifies the sender: the fiscal-memory id for a taxpayer sending its own invoices, the company id for a
	// trusted service provider.
	ClientID string
	// NonceTTL is how long a challenge lives, 10 to 200 seconds (default 30).
	NonceTTL int
	HTTP     *http.Client
	Clock    func() time.Time
}

// Client implements einvoice.Provider.
type Client struct {
	cfg     Config
	signer  Signer
	builder PayloadBuilder
}

var _ einvoice.Provider = (*Client)(nil)

// New creates the client.
func New(cfg Config, signer Signer, builder PayloadBuilder) (*Client, error) {
	switch {
	case signer == nil || builder == nil:
		return nil, errors.New("moadian: a signer and a payload builder are required")
	case strings.TrimSpace(cfg.ClientID) == "":
		return nil, errors.New("moadian: the client id is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.NonceTTL == 0 {
		cfg.NonceTTL = 30
	}
	if cfg.NonceTTL < 10 || cfg.NonceTTL > 200 {
		return nil, errors.New("moadian: the nonce lifetime must be 10 to 200 seconds")
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &Client{cfg: cfg, signer: signer, builder: builder}, nil
}

func (c *Client) Limits() einvoice.Limits {
	return einvoice.Limits{Submit: maxSubmit, Inquire: maxInquire}
}

// APIError is an error answer of the service: {"timestamp", "requestTraceId", "errors":[{code, message}]}.
type APIError struct {
	Status int
	Codes  []string
	Msgs   []string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("moadian: HTTP %d, codes %v: %s", e.Status, e.Codes, strings.Join(e.Msgs, "; "))
}

// do runs one request; token empty means unauthenticated (only the nonce call is).
func (c *Client) do(ctx context.Context, method, path string, q url.Values, token string, body, out any) error {
	u := c.cfg.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("accept", "*/*")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := c.cfg.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		ae := &APIError{Status: res.StatusCode}
		var e struct {
			Errors []struct{ Code, Message string } `json:"errors"`
		}
		if json.Unmarshal(raw, &e) == nil {
			for _, x := range e.Errors {
				ae.Codes, ae.Msgs = append(ae.Codes, x.Code), append(ae.Msgs, x.Message)
			}
		}
		return ae
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return wrap("unreadable answer", err)
	}
	return nil
}

// token runs the identity steps of the document: get a nonce, sign {"nonce", "clientId"} as a JWS.
func (c *Client) token(ctx context.Context) (string, error) {
	var n struct {
		Nonce   string `json:"nonce"`
		ExpDate string `json:"expDate"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v2/nonce", url.Values{"timeToLive": {fmt.Sprint(c.cfg.NonceTTL)}}, "", nil, &n); err != nil {
		return "", err
	}
	if n.Nonce == "" {
		return "", errors.New("moadian: the nonce answer is empty")
	}
	payload, err := marshal(struct {
		Nonce    string `json:"nonce"`
		ClientID string `json:"clientId"`
	}{n.Nonce, c.cfg.ClientID})
	if err != nil {
		return "", err
	}
	return signJWS(c.signer, payload, c.cfg.Clock())
}

// serverKey fetches the key invoices are encrypted for.
func (c *Client) serverKey(ctx context.Context, token string) (*rsa.PublicKey, string, error) {
	var info struct {
		PublicKeys []struct {
			Key       string `json:"key"`
			ID        string `json:"id"`
			Algorithm string `json:"algorithm"`
			Purpose   int    `json:"purpose"`
		} `json:"publicKeys"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v2/server-information", nil, token, nil, &info); err != nil {
		return nil, "", err
	}
	for _, k := range info.PublicKeys {
		if k.Algorithm != "RSA" || k.Purpose != 1 {
			continue
		}
		der, err := base64.StdEncoding.DecodeString(k.Key)
		if err != nil {
			return nil, "", wrap("server key", err)
		}
		pub, err := x509.ParsePKIXPublicKey(der)
		if err != nil {
			return nil, "", wrap("server key", err)
		}
		rk, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, "", errNotRSA
		}
		return rk, k.ID, nil
	}
	return nil, "", errors.New("moadian: the server published no RSA key for invoices")
}

type packetHeader struct {
	RequestTraceID string `json:"requestTraceId"`
	FiscalID       string `json:"fiscalId"`
}

type packet struct {
	Payload string       `json:"payload"`
	Header  packetHeader `json:"header"`
}

// Submit signs, encrypts and sends the invoices in one request (the service takes up to 1000).
func (c *Client) Submit(ctx context.Context, packets []einvoice.Packet) ([]einvoice.Receipt, error) {
	if len(packets) == 0 {
		return nil, nil
	}
	if len(packets) > maxSubmit {
		return nil, fmt.Errorf("moadian: at most %d invoices per request", maxSubmit)
	}
	tok, err := c.token(ctx)
	if err != nil {
		return nil, err
	}
	pub, kid, err := c.serverKey(ctx, tok)
	if err != nil {
		return nil, err
	}
	body := make([]packet, 0, len(packets))
	for _, p := range packets {
		inv, err := c.builder.Build(p.Invoice, p.TraceID)
		if err != nil {
			return nil, wrap("building invoice "+p.TraceID, err)
		}
		jws, err := signJWS(c.signer, inv, c.cfg.Clock())
		if err != nil {
			return nil, err
		}
		jwe, err := encryptJWE([]byte(jws), pub, kid)
		if err != nil {
			return nil, err
		}
		body = append(body, packet{Payload: jwe, Header: packetHeader{RequestTraceID: p.TraceID, FiscalID: p.FiscalID}})
	}
	// every call needs its own one-time nonce, so the send gets a fresh token
	tok, err = c.token(ctx)
	if err != nil {
		return nil, err
	}
	var res struct {
		Result []struct {
			UID             string `json:"uid"`
			ReferenceNumber string `json:"referenceNumber"`
		} `json:"result"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v2/invoice", nil, tok, body, &res); err != nil {
		return nil, err
	}
	out := make([]einvoice.Receipt, 0, len(res.Result))
	for _, r := range res.Result {
		out = append(out, einvoice.Receipt{TraceID: r.UID, ReferenceNumber: r.ReferenceNumber})
	}
	return out, nil
}

type inquiryRow struct {
	ReferenceNumber string `json:"referenceNumber"`
	UID             string `json:"uid"`
	Status          string `json:"status"`
	Data            *struct {
		Error   []inquiryIssue `json:"error"`
		Warning []inquiryIssue `json:"warning"`
	} `json:"data"`
}

type inquiryIssue struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	ErrorType string `json:"errorType"`
}

func toIssues(in []inquiryIssue) []einvoice.Issue {
	out := make([]einvoice.Issue, len(in))
	for i, x := range in {
		out[i] = einvoice.Issue{Code: x.Code, Message: x.Message, Type: x.ErrorType}
	}
	return out
}

// Inquire reads the verdicts by reference number (up to 100 per request). Without a window the service looks only at the
// last 24 hours, so the window starts at sentSince and is at most a week (older invoices come back NOT_FOUND).
func (c *Client) Inquire(ctx context.Context, refs []string, sentSince time.Time) ([]einvoice.Result, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if len(refs) > maxInquire {
		return nil, fmt.Errorf("moadian: at most %d references per inquiry", maxInquire)
	}
	now := c.cfg.Clock()
	start := sentSince.Add(-time.Minute)
	if now.Sub(start) > maxWindow {
		start = now.Add(-maxWindow)
	}
	q := url.Values{"referenceIds": refs, "start": {start.In(iranTime).Format(inquiryLayout)}, "end": {now.In(iranTime).Format(inquiryLayout)}}
	tok, err := c.token(ctx)
	if err != nil {
		return nil, err
	}
	var rows []inquiryRow
	if err := c.do(ctx, http.MethodGet, "/api/v2/inquiry-by-reference-id", q, tok, nil, &rows); err != nil {
		return nil, err
	}
	out := make([]einvoice.Result, 0, len(rows))
	for _, r := range rows {
		res := einvoice.Result{ReferenceNumber: r.ReferenceNumber, TraceID: r.UID, Outcome: einvoice.OutcomePending}
		switch r.Status {
		case "SUCCESS":
			res.Outcome = einvoice.OutcomeSuccess
		case "FAILED":
			res.Outcome = einvoice.OutcomeFailed
		case "NOT_FOUND":
			res.Outcome = einvoice.OutcomeNotFound
		}
		if r.Data != nil {
			res.Errors, res.Warnings = toIssues(r.Data.Error), toIssues(r.Data.Warning)
		}
		out = append(out, res)
	}
	return out, nil
}

// FiscalInformation is the answer of GET fiscal-information.
type FiscalInformation struct {
	NameTrade     string  `json:"nameTrade"`
	FiscalStatus  string  `json:"fiscalStatus"`
	SaleThreshold *string `json:"saleThreshold"`
	EconomicCode  string  `json:"economicCode"`
}

// FiscalInformation reads the state of a fiscal memory (active or not, sales limit, economic code).
func (c *Client) FiscalInformation(ctx context.Context, memoryID string) (FiscalInformation, error) {
	var out FiscalInformation
	tok, err := c.token(ctx)
	if err != nil {
		return out, err
	}
	err = c.do(ctx, http.MethodGet, "/api/v2/fiscal-information", url.Values{"memoryId": {memoryID}}, tok, nil, &out)
	return out, err
}

// Taxpayer is the answer of GET taxpayer.
type Taxpayer struct {
	NameTrade      string `json:"nameTrade"`
	TaxpayerStatus string `json:"taxpayerStatus"` // NOT_ALLOCATED, ACTIVE, DEACTIVATED, TEMPORARY_UNAUTHORIZE, PERMANENT_UNAUTHORIZE, ARTICLE_2_SUBJECT
	NationalID     string `json:"nationalId"`
}

// Taxpayer reads a taxpayer's file by economic code.
func (c *Client) Taxpayer(ctx context.Context, economicCode string) (Taxpayer, error) {
	var out Taxpayer
	tok, err := c.token(ctx)
	if err != nil {
		return out, err
	}
	err = c.do(ctx, http.MethodGet, "/api/v2/taxpayer", url.Values{"economicCode": {economicCode}}, tok, nil, &out)
	return out, err
}
