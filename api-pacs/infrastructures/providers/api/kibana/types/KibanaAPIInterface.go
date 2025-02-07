package types

import (
	"context"
)

// KibanaAPIInterface list of implementable methods for kibana api
type KibanaAPIInterface interface {
	// CreateDataView creates a data view
	CreateDataView(ctx context.Context, requestPayload DataView) error
}
