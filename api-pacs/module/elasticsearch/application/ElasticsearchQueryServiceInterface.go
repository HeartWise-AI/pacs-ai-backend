package application

import (
	"context"

	"api-pacs/module/elasticsearch/domain/entity"
	"api-pacs/module/elasticsearch/infrastructure/service/types"
)

// ElasticsearchQueryServiceInterface holds the implementable methods for the elasticsearch query service
type ElasticsearchQueryServiceInterface interface {
	SearchAdminMemberLogs(ctx context.Context, data types.SearchDocument) ([]entity.AdminMember, error)
	SearchLoginLogs(ctx context.Context, data types.SearchDocument) ([]entity.Login, error)
	SearchModalityStudyLogs(ctx context.Context, data types.SearchDocument) ([]entity.ModalityStudy, error)
	SearchRetrievedStudyLogs(ctx context.Context, data types.SearchDocument) ([]entity.RetrievedStudy, error)
}
