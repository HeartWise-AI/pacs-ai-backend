package inference

import (
	"context"
)

type InferenceAPIInterface interface {
	DetectVessel(ctx context.Context, instances Instances) ([][]float64, error)
	DetectLVEF(ctx context.Context, instances Instances) ([][]float64, error)
}
