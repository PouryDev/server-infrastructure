package storefront

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
	"io"
	"net/http"
	"net/url"
)

func (c *Client) DeploymentCatalog(ctx context.Context) (domain.DeploymentCatalog, error) {
	var out domain.DeploymentCatalog
	err := c.call(ctx, "GET", "/projects/catalog", nil, &out)
	return out, err
}
func (c *Client) ChangeDeployment(ctx context.Context, action string, req domain.DeploymentRequest) (domain.DeploymentJob, error) {
	var out domain.DeploymentJob
	err := c.call(ctx, "POST", "/projects/"+action, req, &out)
	return out, err
}
func (c *Client) UploadDeployment(ctx context.Context, name string, stream io.Reader, size int64) (domain.DeploymentPackage, error) {
	var out domain.DeploymentPackage
	req, err := http.NewRequestWithContext(ctx, "POST", c.URL+"/projects/upload?project="+url.QueryEscape(name), stream)
	if err != nil {
		return out, err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/x-tar")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return out, fmt.Errorf("package upload interrupted; retry with the same package")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		var failure struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&failure) == nil && failure.Error != "" {
			return out, fmt.Errorf("%s", failure.Error)
		}
		return out, fmt.Errorf("package rejected by destination")
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out)
	return out, err
}
