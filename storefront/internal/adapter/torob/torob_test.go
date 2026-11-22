package torob

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PouryDev/storefront/internal/domain/shop"
	"github.com/golang-jwt/jwt/v5"
)

func TestJWTSignatureAudienceTimesAndVersion(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	v, e := NewVerifier(string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), "shop.example")
	if e != nil {
		t.Fatal(e)
	}
	sign := func(aud string, nbf, exp time.Time, version int, key ed25519.PrivateKey) string {
		token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{"aud": aud, "nbf": nbf.Unix(), "exp": exp.Unix()})
		token.Header["v"] = version
		raw, _ := token.SignedString(key)
		return raw
	}
	now := time.Now()
	valid := sign("shop.example", now.Add(-time.Minute), now.Add(time.Minute), 1, key)
	if e = v.Verify(valid, "1"); e != nil {
		t.Fatal(e)
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	for _, raw := range []string{sign("other.example", now.Add(-time.Minute), now.Add(time.Minute), 1, key), sign("shop.example", now.Add(time.Minute), now.Add(2*time.Minute), 1, key), sign("shop.example", now.Add(-time.Hour), now.Add(-time.Minute), 1, key), sign("shop.example", now.Add(-time.Minute), now.Add(time.Minute), 2, key), sign("shop.example", now.Add(-time.Minute), now.Add(time.Minute), 1, other), "invalid"} {
		if v.Verify(raw, "1") == nil {
			t.Fatal("invalid token accepted")
		}
	}
	if v.Verify(valid, "2") == nil {
		t.Fatal("version")
	}
	if _, e = NewVerifier(PublicKey, "shop.example"); e != nil {
		t.Fatal("official Ed25519 key", e)
	}
}
func TestFeedMappingAndWebhook(t *testing.T) {
	p := shop.Product{ID: "p", Slug: "shirt", Name: "پیراهن", Image: "/media/main", Images: []string{"/media/main", "/media/side"}, Variants: []shop.Variant{{ID: "blue-xl", Name: "آبی", Price: 1200000, OldPrice: 1500000, Stock: 2, ColorName: "آبی", Size: "XL", Image: "/media/blue"}}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	mapped := MapProduct("https://shop.example", p)
	if mapped.Price != 120000 || mapped.OldPrice != 150000 || len(mapped.Images) != 3 || mapped.GroupID != "p" || mapped.Specs["سایز"] != "XL" || mapped.URL != "https://shop.example/products/shirt?variant=blue-xl" {
		t.Fatalf("%+v", mapped)
	}
	p.Variants[0].Stock = 0
	if MapProduct("https://shop.example", p).Price != 0 {
		t.Fatal("unavailable price")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-token" || r.Method != "POST" {
			t.Error("auth or method")
		}
		var body struct {
			Items []struct {
				URL string `json:"page_url"`
				ID  string `json:"page_unique"`
			} `json:"items"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Items) != 1 || body.Items[0].ID != "blue-xl" {
			t.Error(body, err)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	c := NewClient("https://shop.example")
	c.Endpoint = server.URL
	if e := c.Notify(context.Background(), "private-token", p); e != nil {
		t.Fatal(e)
	}
}
