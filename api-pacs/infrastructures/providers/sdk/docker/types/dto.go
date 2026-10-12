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

type Lifecycle string

const (
	LifecycleStarting Lifecycle = "starting"
	LifecycleRunning  Lifecycle = "running"
	LifecyclePaused   Lifecycle = "paused"
	LifecycleStopping Lifecycle = "stopping"
	LifecycleStopped  Lifecycle = "stopped"
	LifecycleDegraded Lifecycle = "degraded"
	LifecycleUnknown  Lifecycle = "unknown"
)

func LifecycleFromStatus(status Status) Lifecycle {
	switch status {
	case StatusCreated, StatusRestarting:
		return LifecycleStarting
	case StatusRunning:
		return LifecycleRunning
	case StatusPaused:
		return LifecyclePaused
	case StatusRemoving:
		return LifecycleStopping
	case StatusExited:
		return LifecycleStopped
	case StatusDead:
		return LifecycleDegraded
	default:
		return LifecycleUnknown
	}
}

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

// CreateCandidateContainer describes a container that is staged beside the
// active deployment. Runtime settings are copied from ActiveContainerID while
// the image, name, and registration-owned environment are replaced.
type CreateCandidateContainer struct {
	Name              string
	Image             string
	Envs              []string
	ActiveContainerID string
}

type GetContainerInfoResult struct {
	ID         string
	Name       string
	Lifecycle  Lifecycle
	StartedAt  time.Time
	FinishedAt time.Time
}

// InspectImageResult exposes only the immutable image identity and release
// labels needed to validate a candidate. Registry credentials and the full
// image configuration are deliberately excluded.
type InspectImageResult struct {
	ID          string
	RepoDigests []string
	Labels      map[string]string
}
