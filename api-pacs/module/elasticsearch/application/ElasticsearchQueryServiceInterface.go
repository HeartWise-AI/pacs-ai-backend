package application

import (
	"context"

	"api-pacs/module/elasticsearch/domain/entity"
	"api-pacs/module/elasticsearch/infrastructure/service/types"
)

// ElasticsearchQueryServiceInterface holds the implementable methods for the elasticsearch query service
type ElasticsearchQueryServiceInterface interface {
	// SearchAdminMemberLogs search admin member logs
	SearchAdminMemberLogs(ctx context.Context, data types.SearchDocument) ([]entity.AdminMember, error)
	// SearchLoginLogs search login logs
	SearchLoginLogs(ctx context.Context, data types.SearchDocument) ([]entity.Login, error)
	// SearchModalityStudyLogs search modality study logs
	SearchModalityStudyLogs(ctx context.Context, data types.SearchDocument) ([]entity.ModalityStudy, error)
	// SearchPredictInferenceModelLogs search predict inference model logs
	SearchPredictInferenceModelLogs(ctx context.Context, data types.SearchDocument) ([]entity.PredictInferenceModel, error)
	// SearchRetrievedStudyLogs search retrieved study logs
	SearchRetrievedStudyLogs(ctx context.Context, data types.SearchDocument) ([]entity.RetrievedStudy, error)
	// SearchStoredCustomSeriesLogs search stored custom series logs
	SearchStoredCustomSeriesLogs(ctx context.Context, data types.SearchDocument) ([]entity.StoredCustomSeries, error)
}
