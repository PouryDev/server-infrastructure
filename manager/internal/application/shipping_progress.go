package application

import "github.com/PouryDev/server-infrastructure/manager/internal/domain"

func shippingSteps(plan domain.ShipPlan) []domain.ShipStep {
	var steps []domain.ShipStep
	add := func(phase string) { steps = append(steps, domain.ShipStep{Phase: phase, Status: "pending"}) }
	if plan.Build != nil {
		add("build")
	}
	add("preflight")
	if len(plan.Images) > 0 || plan.Build != nil {
		add("transfer")
		add("verified")
	}
	if plan.Request.Mode != "image" {
		if plan.Request.Deployment != nil {
			add("provisioning")
		}
		add("deploy")
	}
	return steps
}

func updateShippingProgress(job *domain.ShipJob, p domain.ShipProgress) {
	phase := p.Phase
	switch phase {
	case "configuring":
		phase = "provisioning"
	case "migrating", "preserving-limits", "starting", "routing", "healthy", "rollback":
		phase = "deploy"
	}
	index := -1
	for i, step := range job.Steps {
		if step.Phase == phase {
			index = i
			break
		}
	}
	if index >= 0 {
		for i := range job.Steps {
			if i < index {
				job.Steps[i].Status = "complete"
			}
			if i == index {
				job.Steps[i].Status = "running"
			}
		}
	}
	job.Progress = p.Progress
}

func finishShippingProgress(job *domain.ShipJob, success bool) {
	for i := range job.Steps {
		if success {
			job.Steps[i].Status = "complete"
		} else if job.Steps[i].Status == "running" {
			job.Steps[i].Status = "failed"
		}
	}
}
