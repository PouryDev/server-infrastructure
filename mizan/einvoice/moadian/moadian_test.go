package moadian

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/mizan/einvoice"
)

var testNow = time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)

// testIdentity makes a throw-away key and self-signed certificate; nothing here is a real credential.
func testIdentity(t *testing.T) (*KeySigner, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test taxpayer"}, NotBefore: testNow.Add(-time.Hour), NotAfter: testNow.Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	s, err := NewKeySigner(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))
	if err != nil {
		t.Fatal(err)
	}
	return s, key
}

type builder struct{}

func (builder) Build(inv einvoice.Invoice, traceID string) ([]byte, error) {
	return json.Marshal(map[string]any{"trace": traceID, "total": inv.Total})
}

// fakeServer implements the endpoints of the document, strictly: it checks what the document says the real one checks.
type fakeServer struct {
	t        *testing.T
	srv      *httptest.Server
	encKey   *rsa.PrivateKey
	kid      string
	mu       sync.Mutex
	nonces   map[string]bool // nonce -> used
	issued   int
	invoices []map[string]any // decrypted, verified invoice payloads
	status   map[string]string
	issues   map[string][]inquiryIssue
	lastQ    map[string][]string
}

func newFakeServer(t *testing.T, clientID string) *fakeServer {
	t.Helper()
	enc, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeServer{t: t, encKey: enc, kid: "6a2bcd88-a871-4245-a393-2843eafe6e02", nonces: map[string]bool{}, status: map[string]string{}, issues: map[string][]inquiryIssue{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /requestsmanager/api/v2/nonce", f.nonce)
	mux.HandleFunc("GET /requestsmanager/api/v2/server-information", f.auth(clientID, f.serverInfo))
	mux.HandleFunc("POST /requestsmanager/api/v2/invoice", f.auth(clientID, f.invoice))
	mux.HandleFunc("GET /requestsmanager/api/v2/inquiry-by-reference-id", f.auth(clientID, f.inquiry))
	mux.HandleFunc("GET /requestsmanager/api/v2/fiscal-information", f.auth(clientID, f.fiscal))
	mux.HandleFunc("GET /requestsmanager/api/v2/taxpayer", f.auth(clientID, f.taxpayer))
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) fail(w http.ResponseWriter, status int, code, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"timestamp": 1, "requestTraceId": "x", "errors": []map[string]string{{"code": code, "message": msg}}})
}

func (f *fakeServer) nonce(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("timeToLive") != "30" {
		f.fail(w, 400, "4146", "bad ttl")
		return
	}
	f.mu.Lock()
	f.issued++
	n := "nonce-" + string(rune('a'+f.issued))
	f.nonces[n] = false
	f.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]string{"nonce": n, "expDate": "2026-11-09T10:00:30Z"})
}

// verifyJWS checks a compact JWS the way the document describes and returns its payload.
func (f *fakeServer) verifyJWS(compact string) ([]byte, map[string]any, error) {
	parts := strings.Split(compact, ".")
	if len(parts) != 3 {
		return nil, nil, errors.New("not a compact JWS")
	}
	rawHdr, err := b64.DecodeString(parts[0])
	if err != nil {
		return nil, nil, err
	}
	var hdr map[string]any
	if err := json.Unmarshal(rawHdr, &hdr); err != nil {
		return nil, nil, err
	}
	crit, _ := hdr["crit"].([]any)
	sigT, _ := hdr["sigT"].(string)
	if hdr["alg"] != "RS256" || hdr["typ"] != "jose" || hdr["cty"] != "text/plain" || len(crit) != 1 || crit[0] != "sigT" {
		return nil, nil, errors.New("wrong header")
	}
	if _, err := time.Parse(sigTLayout, sigT); err != nil {
		return nil, nil, errors.New("sigT is not yyyy-MM-dd'T'HH:mm:ss'Z'")
	}
	x5c, _ := hdr["x5c"].([]any)
	if len(x5c) != 1 {
		return nil, nil, errors.New("x5c must hold the certificate")
	}
	der, err := base64.StdEncoding.DecodeString(x5c[0].(string)) // x5c is standard base64, not base64url
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil {
		return nil, nil, err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(cert.PublicKey.(*rsa.PublicKey), crypto.SHA256, sum[:], sig); err != nil {
		return nil, nil, err
	}
	payload, err := b64.DecodeString(parts[1])
	return payload, hdr, err
}

func (f *fakeServer) auth(clientID string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			w.WriteHeader(403)
			return
		}
		payload, _, err := f.verifyJWS(tok)
		if err != nil {
			f.fail(w, 401, "4131", err.Error())
			return
		}
		var p struct{ Nonce, ClientID string }
		if err := json.Unmarshal(payload, &p); err != nil || p.ClientID != clientID {
			f.fail(w, 401, "4101", "payload must be {nonce, clientId}")
			return
		}
		f.mu.Lock()
		used, known := f.nonces[p.Nonce]
		f.nonces[p.Nonce] = true
		f.mu.Unlock()
		if !known || used { // one use only
			f.fail(w, 401, "4102", "nonce is not valid")
			return
		}
		next(w, r)
	}
}

func (f *fakeServer) serverInfo(w http.ResponseWriter, _ *http.Request) {
	der, _ := x509.MarshalPKIXPublicKey(&f.encKey.PublicKey)
	_ = json.NewEncoder(w).Encode(map[string]any{"serverTime": testNow.UnixMilli(), "publicKeys": []map[string]any{
		{"key": base64.StdEncoding.EncodeToString(der), "id": f.kid, "algorithm": "RSA", "purpose": 1}}})
}

func (f *fakeServer) invoice(w http.ResponseWriter, r *http.Request) {
	var body []struct {
		Payload string
		Header  struct{ RequestTraceID, FiscalID string }
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) == 0 {
		f.fail(w, 400, "4144", "bad body")
		return
	}
	var result []map[string]any
	for _, p := range body {
		parts := strings.Split(p.Payload, ".")
		if len(parts) != 5 {
			f.fail(w, 400, "4150", "not a JWE")
			return
		}
		hdrRaw, _ := b64.DecodeString(parts[0])
		var hdr map[string]string
		_ = json.Unmarshal(hdrRaw, &hdr)
		if hdr["alg"] != "RSA-OAEP-256" || hdr["enc"] != "A256GCM" || hdr["kid"] != f.kid {
			f.fail(w, 400, "4150", "bad JWE header")
			return
		}
		ek, _ := b64.DecodeString(parts[1])
		iv, _ := b64.DecodeString(parts[2])
		ct, _ := b64.DecodeString(parts[3])
		tag, _ := b64.DecodeString(parts[4])
		cek, err := rsa.DecryptOAEP(sha256.New(), nil, f.encKey, ek, nil)
		if err != nil || len(cek) != 32 || len(iv) != 12 || len(tag) != 16 {
			f.fail(w, 400, "4150", "bad key, iv or tag size")
			return
		}
		block, _ := aes.NewCipher(cek)
		gcm, _ := cipher.NewGCM(block)
		plain, err := gcm.Open(nil, iv, append(ct, tag...), []byte(parts[0]))
		if err != nil {
			f.fail(w, 400, "4150", "cannot decrypt: "+err.Error())
			return
		}
		inv, _, err := f.verifyJWS(string(plain))
		if err != nil {
			f.fail(w, 400, "4131", err.Error())
			return
		}
		var m map[string]any
		_ = json.Unmarshal(inv, &m)
		f.mu.Lock()
		f.invoices = append(f.invoices, m)
		f.mu.Unlock()
		result = append(result, map[string]any{"uid": p.Header.RequestTraceID, "packetType": nil, "referenceNumber": "ref-" + p.Header.RequestTraceID, "data": nil})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"timestamp": 1, "result": result})
}

func (f *fakeServer) inquiry(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.lastQ = r.URL.Query()
	f.mu.Unlock()
	var rows []map[string]any
	for _, ref := range r.URL.Query()["referenceIds"] {
		st := f.status[ref]
		if st == "" {
			st = "PENDING"
		}
		data := map[string]any{"error": f.issues[ref], "warning": []any{}, "success": st == "SUCCESS"}
		if f.issues[ref] == nil {
			data["error"] = []any{}
		}
		rows = append(rows, map[string]any{"referenceNumber": ref, "uid": strings.TrimPrefix(ref, "ref-"), "status": st, "data": data, "packetType": "receive_invoice_confirm", "fiscalId": "A11216"})
	}
	_ = json.NewEncoder(w).Encode(rows)
}

func (f *fakeServer) fiscal(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("memoryId") != "A11216" {
		f.fail(w, 404, "4161", "not found")
		return
	}
	_, _ = w.Write([]byte(`{"nameTrade":"A11216","fiscalStatus":"ACTIVE","saleThreshold":null,"economicCode":"14003778990"}`))
}

func (f *fakeServer) taxpayer(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("economicCode") != "14003778990" {
		f.fail(w, 404, "4170", "not found")
		return
	}
	_, _ = w.Write([]byte(`{"nameTrade":"shop","taxpayerStatus":"ACTIVE","nationalId":"14003778990"}`))
}

func newClient(t *testing.T, f *fakeServer, signer Signer) *Client {
	t.Helper()
	c, err := New(Config{BaseURL: f.srv.URL + "/requestsmanager", ClientID: "A11216", Clock: func() time.Time { return testNow }}, signer, builder{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestJWEHeaderMatchesTheDocumentsVector(t *testing.T) {
	// page 24 of RC_TICS.IS_v1.3 shows this protected header and its Base64URL form
	hdr, err := marshal(struct {
		Alg string `json:"alg"`
		Enc string `json:"enc"`
		Kid string `json:"kid"`
	}{"RSA-OAEP-256", "A256GCM", "6a2bcd88-a871-4245-a393-2843eafe6e02"})
	if err != nil {
		t.Fatal(err)
	}
	const doc = `{"alg":"RSA-OAEP-256","enc":"A256GCM","kid":"6a2bcd88-a871-4245-a393-2843eafe6e02"}`
	const want = "eyJhbGciOiJSU0EtT0FFUC0yNTYiLCJlbmMiOiJBMjU2R0NNIiwia2lkIjoiNmEyYmNkODgtYTg3MS00MjQ1LWEzOTMtMjg0M2VhZmU2ZTAyIn0"
	if string(hdr) != doc || b64.EncodeToString(hdr) != want {
		t.Fatalf("%s\n%s", hdr, b64.EncodeToString(hdr))
	}
}

func TestJWSHeaderOrderAndFormat(t *testing.T) {
	s, _ := testIdentity(t)
	jws, err := signJWS(s, []byte(`{"nonce":"n","clientId":"A11226"}`), time.Date(2023, 5, 13, 10, 44, 47, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := b64.DecodeString(strings.Split(jws, ".")[0])
	h := string(raw)
	// the document's order: alg, x5c, sigT, typ, crit, cty; sigT like 2023-05-13T10:44:47Z
	order := []string{`"alg":"RS256"`, `"x5c":[`, `"sigT":"2023-05-13T10:44:47Z"`, `"typ":"jose"`, `"crit":["sigT"]`, `"cty":"text/plain"`}
	last := -1
	for _, part := range order {
		i := strings.Index(h, part)
		if i <= last {
			t.Fatalf("%s is missing or out of order in %s", part, h)
		}
		last = i
	}
}

func TestNewKeySignerChecksTheKeyBelongsToTheCertificate(t *testing.T) {
	a, _ := testIdentity(t)
	_, otherKey := testIdentity(t)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(otherKey)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: a.Certificate()})
	if _, err := NewKeySigner(certPEM, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})); err == nil {
		t.Fatal("a certificate with someone else's key must be refused")
	}
	if _, err := NewKeySigner([]byte("junk"), []byte("junk")); err == nil {
		t.Fatal("junk must be refused")
	}
}

func TestSubmitSignsEncryptsAndSendsPerTheDocument(t *testing.T) {
	signer, _ := testIdentity(t)
	f := newFakeServer(t, "A11216")
	c := newClient(t, f, signer)
	receipts, err := c.Submit(context.Background(), []einvoice.Packet{
		{TraceID: "11111111-1111-4111-8111-111111111111", FiscalID: "A11216", Invoice: einvoice.Invoice{Total: 1100}},
		{TraceID: "22222222-2222-4222-8222-222222222222", FiscalID: "A11216", Invoice: einvoice.Invoice{Total: 2200}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 2 || receipts[0].ReferenceNumber != "ref-11111111-1111-4111-8111-111111111111" || receipts[1].TraceID != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("%+v", receipts)
	}
	if len(f.invoices) != 2 || f.invoices[0]["total"].(float64) != 1100 || f.invoices[1]["trace"] != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("the server must read back exactly what was built: %v", f.invoices)
	}
	if f.issued != 2 { // server-information and the send each used their own one-time nonce
		t.Fatalf("nonces issued: %d", f.issued)
	}
}

func TestWrongClientIDAndReplayAreRefusedWithTheDocumentsCodes(t *testing.T) {
	signer, _ := testIdentity(t)
	f := newFakeServer(t, "A11216")
	bad, err := New(Config{BaseURL: f.srv.URL + "/requestsmanager", ClientID: "OTHER", Clock: func() time.Time { return testNow }}, signer, builder{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = bad.Submit(context.Background(), []einvoice.Packet{{TraceID: "t", FiscalID: "F"}})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 401 || len(ae.Codes) == 0 || ae.Codes[0] != "4101" {
		t.Fatalf("%v", err)
	}
}

func TestInquireReadsVerdictsAndSendsTheWindow(t *testing.T) {
	signer, _ := testIdentity(t)
	f := newFakeServer(t, "A11216")
	f.status["ref-ok"] = "SUCCESS"
	f.status["ref-bad"] = "FAILED"
	f.issues["ref-bad"] = []inquiryIssue{{Code: "012802", Message: "m", ErrorType: "ERROR"}}
	f.status["ref-gone"] = "NOT_FOUND"
	c := newClient(t, f, signer)
	res, err := c.Inquire(context.Background(), []string{"ref-ok", "ref-bad", "ref-pending", "ref-gone"}, testNow.Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := []einvoice.Outcome{einvoice.OutcomeSuccess, einvoice.OutcomeFailed, einvoice.OutcomePending, einvoice.OutcomeNotFound}
	for i, r := range res {
		if r.Outcome != want[i] {
			t.Errorf("%s: %s, want %s", r.ReferenceNumber, r.Outcome, want[i])
		}
	}
	if len(res[1].Errors) != 1 || res[1].Errors[0].Code != "012802" || res[1].Errors[0].Type != "ERROR" {
		t.Fatalf("%+v", res[1])
	}
	// start and end use the document's format with the +03:30 offset; the references are repeated parameters
	if got := f.lastQ["start"][0]; got != "2026-11-09T11:29:00.000000000+03:30" {
		t.Errorf("start %q", got)
	}
	if got := f.lastQ["end"][0]; got != "2026-11-09T13:30:00.000000000+03:30" {
		t.Errorf("end %q", got)
	}
	if len(f.lastQ["referenceIds"]) != 4 {
		t.Errorf("%v", f.lastQ)
	}
	// a window longer than a week is cut to a week
	if _, err := c.Inquire(context.Background(), []string{"ref-ok"}, testNow.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := f.lastQ["start"][0]; got != "2026-11-02T13:30:00.000000000+03:30" {
		t.Errorf("start %q", got)
	}
	if _, err := c.Inquire(context.Background(), make([]string, 101), testNow); err == nil {
		t.Fatal("more than 100 references must be refused")
	}
}

func TestFiscalInformationAndTaxpayer(t *testing.T) {
	signer, _ := testIdentity(t)
	f := newFakeServer(t, "A11216")
	c := newClient(t, f, signer)
	fi, err := c.FiscalInformation(context.Background(), "A11216")
	if err != nil || fi.FiscalStatus != "ACTIVE" || fi.EconomicCode != "14003778990" || fi.SaleThreshold != nil {
		t.Fatalf("%+v %v", fi, err)
	}
	tp, err := c.Taxpayer(context.Background(), "14003778990")
	if err != nil || tp.TaxpayerStatus != "ACTIVE" {
		t.Fatalf("%+v %v", tp, err)
	}
	var ae *APIError
	if _, err := c.Taxpayer(context.Background(), "1"); !errors.As(err, &ae) || ae.Status != 404 || ae.Codes[0] != "4170" {
		t.Fatalf("%v", err)
	}
}

func TestNewValidatesConfig(t *testing.T) {
	s, _ := testIdentity(t)
	for name, cfg := range map[string]Config{"no client id": {}, "ttl too short": {ClientID: "A", NonceTTL: 5}, "ttl too long": {ClientID: "A", NonceTTL: 201}} {
		if _, err := New(cfg, s, builder{}); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	if _, err := New(Config{ClientID: "A"}, nil, builder{}); err == nil {
		t.Error("a signer is required")
	}
}
