package types

import (
	"context"
)

// KibanaAPIInterface list of implementable methods for kibana api
type DockerAPIInterface interface {
	GetImageFromDocker(ctx context.Context, imageBuffer []byte) error
}
