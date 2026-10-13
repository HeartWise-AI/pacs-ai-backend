package repository

import (
	"context"

	"api-pacs/module/inference/domain/entity"
)

type InferenceModelVersionRepositoryInterface interface {
	PublishInferenceModelVersion(context.Context, entity.InferenceModelVersion) (entity.InferenceModelVersion, error)
	SelectInferenceModelVersion(context.Context, string) (entity.InferenceModelVersion, error)
	ListInferenceModelVersions(context.Context, string) ([]entity.InferenceModelVersion, error)
}
