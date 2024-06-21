package types

import (
	"context"
)

// KibanaAPIInterface list of implementable methods for kibana api
type KibanaAPIInterface interface {
	CreateDataView(ctx context.Context, requestPayload DataView) error
}
