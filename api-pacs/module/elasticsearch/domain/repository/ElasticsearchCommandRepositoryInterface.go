package repository

import (
	"context"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/index"

	repositoryTypes "api-pacs/module/elasticsearch/infrastructure/repository/types"
)

type ElasticsearchCommandRepositoryInterface interface {
	// InsertAdminInviteLog insert admin invite log
	InsertAdminInviteLog(ctx context.Context, data repositoryTypes.CreateAdminInviteLog) (*index.Response, error)
	// InsertAdminMemberLog insert admin member log
	InsertAdminMemberLog(ctx context.Context, data repositoryTypes.CreateAdminMemberLog) (*index.Response, error)
	// InsertGetModalityStudyLog insert get modality study log
	InsertGetModalityStudyLog(ctx context.Context, data repositoryTypes.CreateGetModalityStudyLog) (*index.Response, error)
	// InsertLoginLog insert login log
	InsertLoginLog(ctx context.Context, data repositoryTypes.CreateLoginLog) (*index.Response, error)
	// InsertPredictInferenceModelLog predict inference model log
	InsertPredictInferenceModelLog(ctx context.Context, data repositoryTypes.CreatePredictInferenceModelLog) (*index.Response, error)
	// InsertRetrieveStudyLog insert retrieved study log
	InsertRetrieveStudyLog(ctx context.Context, data repositoryTypes.CreateRetrieveStudyLog) (*index.Response, error)
	// InsertSignedConsentLog insert signed consent log
	InsertSignedConsentLog(ctx context.Context, data repositoryTypes.CreateSignedConsentLog) (*index.Response, error)
	// InsertStoredCustomSeriesLog insert stored custom series log
	InsertStoredCustomSeriesLog(ctx context.Context, data repositoryTypes.CreateStoredCustomSeriesLog) (*index.Response, error)
}
