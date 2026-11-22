package application

import (
	"context"
	"fmt"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
	"regexp"
	"strings"
)

type StorefrontRunner interface {
	Catalog(context.Context) (domain.StorefrontCatalog, error)
	Create(context.Context, domain.StorefrontSpec) (domain.StorefrontJob, error)
}
type StorefrontAudit interface {
	Record(context.Context, domain.Event) error
}
type Storefront struct {
	Runner StorefrontRunner
	Repo   StorefrontAudit
}

func (s *Storefront) Catalog(ctx context.Context) (domain.StorefrontCatalog, error) {
	return s.Runner.Catalog(ctx)
}
func (s *Storefront) Create(ctx context.Context, actor string, in domain.StorefrontSpec) (domain.StorefrontJob, error) {
	var zero domain.StorefrontJob
	if in.Accent != "" && !regexp.MustCompile(`^#[0-9a-fA-F]{6}$`).MatchString(in.Accent) {
		return zero, fmt.Errorf("invalid brand color")
	}
	if !in.Demo && (in.SMSKey == "" || in.SMSTemplate == "") {
		return zero, fmt.Errorf("configure SMS credentials before creating a live store")
	}
	if len(in.SMSKey) > 1000 || len(in.SMSTemplate) > 100 || strings.ContainsAny(in.SMSKey+in.SMSTemplate, "\r\n") {
		return zero, fmt.Errorf("invalid SMS configuration")
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`).MatchString(in.Name) || !regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`).MatchString(in.Domain) || !regexp.MustCompile(`^[a-f0-9-]{36}$`).MatchString(in.Key) || len(in.Title) > 150 || strings.ContainsAny(in.Title, "\r\n") {
		return zero, fmt.Errorf("invalid storefront identity")
	}
	phone := regexp.MustCompile(`^\+989[0-9]{9}$`)
	if !phone.MatchString(in.OwnerPhone) || !phone.MatchString(in.AdminPhone) || in.OwnerPhone == in.AdminPhone {
		return zero, fmt.Errorf("enter distinct Iranian owner and admin phone numbers")
	}
	if in.Demo && !strings.HasSuffix(in.Domain, ".localhost") {
		return zero, fmt.Errorf("demo stores require a .localhost domain")
	}
	catalog, e := s.Runner.Catalog(ctx)
	if e != nil {
		return zero, e
	}
	image, theme := false, false
	for _, v := range catalog.Images {
		if v == in.Image {
			image = true
		}
	}
	for _, v := range catalog.Themes {
		if v.ID == in.Theme {
			theme = true
		}
	}
	if !image || !theme {
		return zero, fmt.Errorf("choose an available storefront image and theme")
	}
	for _, m := range in.Modules {
		found := false
		for _, v := range catalog.Modules {
			if v.ID == m {
				found = true
			}
		}
		if !found {
			return zero, fmt.Errorf("unknown storefront module")
		}
	}
	if e = s.Repo.Record(ctx, domain.Event{Actor: actor, Project: in.Name, Action: "storefront.create", Outcome: "intent", Detail: "domain=" + in.Domain}); e != nil {
		return zero, e
	}
	return s.Runner.Create(ctx, in)
}
