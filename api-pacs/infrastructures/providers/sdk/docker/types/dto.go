package types

import "time"

type Status string

const (
	StatusCreated    Status = "created"
	StatusRunning    Status = "running"
	StatusPaused     Status = "paused"
	StatusRestarting Status = "restarting"
	StatusExited     Status = "exited"
	StatusRemoving   Status = "removing"
	StatusDead       Status = "dead"
)

type Config struct {
	Username string
	Password string
	Network  string
}

type CreateContainer struct {
	Name  string
	Image string
	Envs  []string
}

type GetContainerInfoResult struct {
	ID              string
	Name            string
	Status          Status
	Running         bool
	StartedAt       time.Time
	FinishedAt      time.Time
	CPUPercentUsage float64 // in percent
	MemoryInBytes   uint64  // in bytes
}
