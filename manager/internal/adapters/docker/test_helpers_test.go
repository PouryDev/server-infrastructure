package docker

import "github.com/PouryDev/server-infrastructure/manager/internal/domain"

func safeContainer() domain.Container { return domain.Container{ID: "test", State: "running"} }
