// Package torob implements Torob's official v3 JWT and update-webhook contracts.
package torob

import (
	"bytes"
	"context"
	"crypto"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PouryDev/storefront/internal/domain/shop"
	"github.com/golang-jwt/jwt/v5"
)

const PublicKey = "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAt6Mu4T0pBORY11W+QeM35UsmLO3vsf+6yKpFDEImFk0=\n-----END PUBLIC KEY-----"
const WebhookURL = "https://api.torob.com/update/webhook/v1/"

type Verifier struct {
	key    crypto.PublicKey
	parser *jwt.Parser
}

func NewVerifier(pem, host string) (*Verifier, error) {
	key, err := jwt.ParseEdPublicKeyFromPEM([]byte(pem))
	if err != nil {
		return nil, err
	}
	return &Verifier{key: key, parser: jwt.NewParser(jwt.WithAudience(host), jwt.WithExpirationRequired(), jwt.WithValidMethods([]string{"EdDSA"}))}, nil
}
func (v *Verifier) Verify(token, version string) error {
	if version != "1" || token == "" || len(token) > 8192 {
		return shop.ErrForbidden
	}
	claims := &jwt.RegisteredClaims{}
	parsed, err := v.parser.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) { return v.key, nil })
	if err != nil || !parsed.Valid || claims.NotBefore == nil || parsed.Header["v"] != float64(1) {
		return shop.ErrForbidden
	}
	return nil
}

type Client struct {
	HTTP     *http.Client
	Origin   string
	Endpoint string
}

func NewClient(origin string) *Client {
	return &Client{Origin: origin, Endpoint: WebhookURL, HTTP: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func VariantURL(origin string, p shop.Product, v shop.Variant) string {
	return origin + "/products/" + p.Slug + "?variant=" + url.QueryEscape(v.ID)
}
func (c *Client) Notify(ctx context.Context, token string, p shop.Product) error {
	type item struct {
		URL string `json:"page_url"`
		ID  string `json:"page_unique"`
	}
	items := []item{}
	for _, v := range p.Variants {
		items = append(items, item{VariantURL(c.Origin, p, v), v.ID})
	}
	if len(items) == 0 {
		return nil
	}
	if len(items) > 100 {
		return shop.ErrInvalid
	}
	raw, err := json.Marshal(map[string]any{"items": items})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("torob webhook transport failed")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("torob webhook returned status %d", response.StatusCode)
	}
	return nil
}

type Product struct {
	ID          string            `json:"page_unique"`
	URL         string            `json:"page_url"`
	GroupID     string            `json:"product_group_id"`
	Title       string            `json:"title"`
	Price       int64             `json:"current_price"`
	OldPrice    int64             `json:"old_price,omitempty"`
	Available   bool              `json:"availability"`
	Category    string            `json:"category_name,omitempty"`
	Images      []string          `json:"image_links"`
	Description string            `json:"short_desc,omitempty"`
	Specs       map[string]string `json:"spec"`
	Guarantee   string            `json:"guarantee,omitempty"`
	Added       string            `json:"date_added"`
	Updated     string            `json:"date_updated"`
}

func limit(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
func MapProduct(origin string, p shop.Product) Product {
	v := p.Variants[0]
	images := []string{}
	for _, image := range append([]string{v.Image, p.Image}, p.Images...) {
		if image != "" && !contains(images, origin+image) {
			images = append(images, origin+image)
		}
	}
	specs := map[string]string{}
	for k, v := range p.Specs {
		specs[k] = v
	}
	if v.ColorName != "" {
		specs["رنگ"] = v.ColorName
	}
	if v.Size != "" {
		specs["سایز"] = v.Size
	}
	if p.Brand != "" {
		specs["برند"] = p.Brand
	}
	title := p.Name
	if v.Name != "" {
		title += " — " + v.Name
	}
	price := v.Price / 10
	if v.Stock <= 0 {
		price = 0
	}
	return Product{ID: v.ID, URL: VariantURL(origin, p, v), GroupID: p.ID, Title: limit(title, 500), Price: price, OldPrice: v.OldPrice / 10, Available: v.Stock > 0, Category: p.Category, Images: images, Description: limit(p.Description, 500), Specs: specs, Guarantee: p.Guarantee, Added: p.CreatedAt.Format(time.RFC3339), Updated: p.UpdatedAt.Format(time.RFC3339)}
}
func contains(values []string, value string) bool {
	for _, v := range values {
		if strings.EqualFold(v, value) {
			return true
		}
	}
	return false
}
