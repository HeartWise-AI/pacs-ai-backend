package types

import "context"

// CandidateDockerSDKInterface is the narrow Docker surface used to stage and
// inspect a model image without changing the active deployment.
type CandidateDockerSDKInterface interface {
	PullImage(context.Context, string) error
	InspectImage(context.Context, string) (InspectImageResult, error)
	CreateCandidateContainer(context.Context, CreateCandidateContainer) (string, error)
	GetContainerInfo(context.Context, string) (GetContainerInfoResult, error)
	StartContainer(context.Context, string) error
	StopContainer(context.Context, string) error
	RemoveContainer(context.Context, string) error
}
