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
	Username           string
	Password           string
	Network            string
	GPUDefaultUUID     string
	GPUAssignmentsJSON string
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
	GPUUUID           string
	Name              string
	Image             string
	Envs              []string
	ActiveContainerID string
	// KeepActiveConfiguration recreates the active container as it runs now:
	// its environment, labels, command, entrypoint, restart policy and extra
	// network aliases are kept and Envs is ignored. Only the GPU device request
	// and the container's own identity (name, ID aliases, host ports) change.
	KeepActiveConfiguration bool
	// ExpectedImageID, when set with KeepActiveConfiguration, must be the local
	// image the active container runs; otherwise nothing is created.
	ExpectedImageID string
}

type GetContainerInfoResult struct {
	PlacementName string
	GPUUUID       string
	ID            string
	ImageID       string
	// ImageReference is the reference the container was created with. It is a
	// bare image ID for containers created from an immutable local image.
	ImageReference string
	Name           string
	Lifecycle      Lifecycle
	StartedAt      time.Time
	FinishedAt     time.Time
}

// InspectImageResult exposes only the immutable image identity and release
// labels needed to validate a candidate. Registry credentials and the full
// image configuration are deliberately excluded.
type InspectImageResult struct {
	ID          string
	RepoDigests []string
	RepoTags    []string
	Labels      map[string]string
}
