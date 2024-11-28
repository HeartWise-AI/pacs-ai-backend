package types

import "context"

// DockerInferenceAPIInterface is the interface for the docker inference API
type DockerInferenceAPIInterface interface {
	// GetModelInfo gets the model info from the docker inference API
	GetModelInfo(ctx context.Context, containerName string) (GetModelInfoResponse, error)
	// GetModelFacts gets the model facts from the docker inference API
	GetModelFacts(ctx context.Context, containerName string) (GetModelFactsResponse, error)
}
