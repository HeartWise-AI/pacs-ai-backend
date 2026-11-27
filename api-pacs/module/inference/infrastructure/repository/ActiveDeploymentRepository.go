package repository

import (
	"api-pacs/module/inference/domain/entity"
	"context"
)

// ListActiveModelRegistrations supplies the private service-to-service registry.
func (repository *InferenceQueryRepository) ListActiveModelRegistrations(ctx context.Context) ([]entity.InferenceModel, error) {
	client, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	var model entity.InferenceModel
	docs, err := client.Collection(model.GetModelName()).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	models := make([]entity.InferenceModel, 0, len(docs))
	for _, doc := range docs {
		var model entity.InferenceModel
		if err := doc.DataTo(&model); err != nil {
			return nil, err
		}
		model.ID = doc.Ref.ID
		models = append(models, model)
	}
	return models, nil
}
