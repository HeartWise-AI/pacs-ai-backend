package types

import "context"

// ModelUpgradeDockerSDKInterface is the narrow Docker surface used by the
// transactional upgrade service.
type ModelUpgradeDockerSDKInterface interface {
	PullImage(context.Context, string) error
	InspectImage(context.Context, string) (InspectImageResult, error)
	CreateContainer(context.Context, CreateContainer) (string, error)
	GetContainerInfo(context.Context, string) (GetContainerInfoResult, error)
	StartContainer(context.Context, string) error
	StopContainer(context.Context, string) error
	RemoveContainer(context.Context, string) error
}
