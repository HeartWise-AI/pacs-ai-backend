package application

import (
	"context"

	"api-pacs/module/record/domain/entity"
	"api-pacs/module/record/infrastructure/service/types"
)

// RecordCommandServiceInterface holds the implementable methods for the record command service
type RecordCommandServiceInterface interface {
	CreateRecord(ctx context.Context, data types.CreateRecord) (entity.Record, error)
}
