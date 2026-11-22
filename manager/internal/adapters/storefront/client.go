package storefront

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
	"io"
	"net/http"
	"time"
)

type Client struct {
	URL, Token string
	HTTP       *http.Client
}

func New(url, token string) *Client {
	return &Client{URL: url, Token: token, HTTP: &http.Client{Timeout: 15 * time.Second}}
}
func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	var b bytes.Buffer
	if in != nil {
		if e := json.NewEncoder(&b).Encode(in); e != nil {
			return e
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, c.URL+path, &b)
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	res, e := c.HTTP.Do(req)
	if e != nil {
		return fmt.Errorf("storefront runner unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var v struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&v) == nil && v.Error != "" {
			return fmt.Errorf("%s", v.Error)
		}
		return fmt.Errorf("storefront runner rejected request")
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
}
func (c *Client) Catalog(ctx context.Context) (domain.StorefrontCatalog, error) {
	var out domain.StorefrontCatalog
	e := c.call(ctx, "GET", "/catalog", nil, &out)
	return out, e
}
func (c *Client) Create(ctx context.Context, in domain.StorefrontSpec) (domain.StorefrontJob, error) {
	var out domain.StorefrontJob
	e := c.call(ctx, "POST", "/create", in, &out)
	return out, e
}
