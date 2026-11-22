package application

import (
	"context"
	"fmt"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
	"io"
	"regexp"
)

const MaxDeploymentUpload int64 = 2 << 30

type DeploymentRunner interface {
	DeploymentCatalog(context.Context) (domain.DeploymentCatalog, error)
	UploadDeployment(context.Context, string, io.Reader, int64) (domain.DeploymentPackage, error)
	ChangeDeployment(context.Context, string, domain.DeploymentRequest) (domain.DeploymentJob, error)
}
type Deployments struct {
	Runner DeploymentRunner
	Repo   StorefrontAudit
}

var deploymentName = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)

func (s *Deployments) Catalog(ctx context.Context) (domain.DeploymentCatalog, error) {
	return s.Runner.DeploymentCatalog(ctx)
}
func (s *Deployments) Upload(ctx context.Context, actor, name string, stream io.Reader, size int64) (domain.DeploymentPackage, error) {
	if !deploymentName.MatchString(name) || size <= 0 || size > MaxDeploymentUpload {
		return domain.DeploymentPackage{}, fmt.Errorf("invalid project name or package size")
	}
	if err := s.Repo.Record(ctx, domain.Event{Actor: actor, Project: name, Action: "deployment.upload", Outcome: "intent"}); err != nil {
		return domain.DeploymentPackage{}, err
	}
	result, err := s.Runner.UploadDeployment(ctx, name, stream, size)
	outcome := "success"
	if err != nil {
		outcome = "failure"
	}
	// Record no archive content, file names, env values or credential-derived hashes.
	if auditErr := s.Repo.Record(ctx, domain.Event{Actor: actor, Project: name, Action: "deployment.upload", Outcome: outcome}); auditErr != nil && err == nil {
		err = auditErr
	}
	return result, err
}
func (s *Deployments) Change(ctx context.Context, actor, action string, req domain.DeploymentRequest) (domain.DeploymentJob, error) {
	if !deploymentName.MatchString(req.Name) || (action != "save" && action != "deploy") {
		return domain.DeploymentJob{}, fmt.Errorf("invalid deployment request")
	}
	if err := s.Repo.Record(ctx, domain.Event{Actor: actor, Project: req.Name, Action: "deployment." + action, Outcome: "intent"}); err != nil {
		return domain.DeploymentJob{}, err
	}
	result, err := s.Runner.ChangeDeployment(ctx, action, req)
	outcome := "accepted"
	if err != nil {
		outcome = "failure"
	}
	if auditErr := s.Repo.Record(ctx, domain.Event{Actor: actor, Project: req.Name, Action: "deployment." + action, Outcome: outcome}); auditErr != nil && err == nil {
		err = auditErr
	}
	return result, err
}
