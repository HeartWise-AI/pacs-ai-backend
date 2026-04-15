package application

import (
	"context"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/index"

	"api-pacs/module/elasticsearch/infrastructure/service/types"
)

// ElasticsearchCommandServiceInterface holds the implementable methods for the elasticsearch command service
type ElasticsearchCommandServiceInterface interface {
	// CreateAdminInviteLog add a new admin invite log
	CreateAdminInviteLog(ctx context.Context, data types.CreateAdminInviteLog) (*index.Response, error)
	// CreateAdminMemberLog add a new admin member log
	CreateAdminMemberLog(ctx context.Context, data types.CreateAdminMemberLog) (*index.Response, error)
	// CreateGetModalityStudyLog add a get modality study log
	CreateGetModalityStudyLog(ctx context.Context, data types.CreateGetModalityStudyLog) (*index.Response, error)
	// CreateLoginLog add a new login log
	CreateLoginLog(ctx context.Context, data types.CreateLoginLog) (*index.Response, error)
	// CreatePredictInferenceModelLog add a predict inference model log
	CreatePredictInferenceModelLog(ctx context.Context, data types.CreatePredictInferenceModelLog) (*index.Response, error)
	// CreateRetrievedStudyLog add a retrieved study log
	CreateRetrieveStudyLog(ctx context.Context, data types.CreateRetrieveStudyLog) (*index.Response, error)
	// CreateSignedConsentLog add a signed consent log
	CreateSignedConsentLog(ctx context.Context, data types.CreateSignedConsentLog) (*index.Response, error)
	// CreateStoredCustomSeriesLog add a stored custom series log
	CreateStoredCustomSeriesLog(ctx context.Context, data types.CreateStoredCustomSeriesLog) (*index.Response, error)
	// SyncKibanaIndices sync kibana indices
	SyncKibanaIndices(ctx context.Context) error
}
