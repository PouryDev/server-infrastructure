package application

import (
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
	"testing"
)

func TestShippingProgressOnlyConfirmsFinishedStages(t *testing.T) {
	plan := domain.ShipPlan{Build: &domain.BuildInfo{}, Request: domain.ShipRequest{Mode: "build", Deployment: &domain.DeploymentRoute{}}}
	job := domain.ShipJob{Steps: shippingSteps(plan)}
	updateShippingProgress(&job, domain.ShipProgress{Phase: "build"})
	if job.Steps[0].Status != "running" || job.Steps[1].Status != "pending" {
		t.Fatal(job.Steps)
	}
	updateShippingProgress(&job, domain.ShipProgress{Phase: "transfer", Progress: &domain.WorkProgress{Current: 123, Unit: "bytes"}})
	if job.Steps[0].Status != "complete" || job.Steps[2].Status != "running" || job.Progress.Current != 123 {
		t.Fatal(job)
	}
	finishShippingProgress(&job, false)
	if job.Steps[2].Status != "failed" || job.Steps[3].Status != "pending" {
		t.Fatal(job.Steps)
	}
	finishShippingProgress(&job, true)
	for _, step := range job.Steps {
		if step.Status != "complete" {
			t.Fatal(job.Steps)
		}
	}
}
