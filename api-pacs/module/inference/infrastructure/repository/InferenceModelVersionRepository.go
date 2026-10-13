package repository

import (
	"context"
	"errors"
	"log"
	"reflect"
	"sort"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
)

type InferenceModelVersionRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}

func (repository *InferenceModelVersionRepository) PublishInferenceModelVersion(
	ctx context.Context,
	version entity.InferenceModelVersion,
) (entity.InferenceModelVersion, error) {
	version.Normalize()
	if err := version.Validate(); err != nil || version.Status != entity.InferenceModelVersionAvailable {
		return entity.InferenceModelVersion{}, errors.New(apiError.InvalidPayload)
	}
	version.ID = entity.InferenceModelVersionID(version.ModelID, version.Version)
	version.PublishedAt = time.Now().Unix()

	client, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return entity.InferenceModelVersion{}, errors.New(apiError.FirestoreError)
	}
	defer client.Close()

	document := client.Collection(version.GetModelName()).Doc(version.ID)
	published := version
	err = client.RunTransaction(ctx, func(ctx context.Context, transaction *firestore.Transaction) error {
		snapshot, getErr := transaction.Get(document)
		if status.Code(getErr) == codes.NotFound {
			return transaction.Create(document, version)
		}
		if getErr != nil {
			return getErr
		}
		var existing entity.InferenceModelVersion
		if decodeErr := snapshot.DataTo(&existing); decodeErr != nil {
			return decodeErr
		}
		existing.ID = snapshot.Ref.ID
		existing.Normalize()
		if samePublishedArtifact(existing, version) {
			published = existing
			return nil
		}
		return errors.New(apiError.InferenceUpgradeConflict)
	})
	if err != nil {
		if err.Error() == apiError.InferenceUpgradeConflict {
			return entity.InferenceModelVersion{}, err
		}
		log.Println(err)
		return entity.InferenceModelVersion{}, errors.New(apiError.FirestoreError)
	}
	return published, nil
}

func (repository *InferenceModelVersionRepository) SelectInferenceModelVersion(
	ctx context.Context,
	id string,
) (entity.InferenceModelVersion, error) {
	var model entity.InferenceModelVersion
	client, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		return model, errors.New(apiError.FirestoreError)
	}
	defer client.Close()

	snapshot, err := client.Collection(model.GetModelName()).Doc(id).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return model, errors.New(apiError.MissingRecord)
	}
	if err != nil {
		log.Println(err)
		return model, errors.New(apiError.FirestoreError)
	}
	if err = snapshot.DataTo(&model); err != nil {
		return entity.InferenceModelVersion{}, errors.New(apiError.FirestoreError)
	}
	model.ID = snapshot.Ref.ID
	model.Normalize()
	if err = model.Validate(); err != nil {
		return entity.InferenceModelVersion{}, errors.New(apiError.InvalidPayload)
	}
	return model, nil
}

func (repository *InferenceModelVersionRepository) ListInferenceModelVersions(
	ctx context.Context,
	modelID string,
) ([]entity.InferenceModelVersion, error) {
	var model entity.InferenceModelVersion
	client, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		return nil, errors.New(apiError.FirestoreError)
	}
	defer client.Close()

	documents, err := client.Collection(model.GetModelName()).Where("model_id", "==", modelID).Documents(ctx).GetAll()
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.FirestoreError)
	}
	versions := make([]entity.InferenceModelVersion, 0, len(documents))
	for _, document := range documents {
		var version entity.InferenceModelVersion
		if decodeErr := document.DataTo(&version); decodeErr != nil {
			return nil, errors.New(apiError.FirestoreError)
		}
		version.ID = document.Ref.ID
		version.Normalize()
		if validationErr := version.Validate(); validationErr != nil {
			return nil, errors.New(apiError.InvalidPayload)
		}
		versions = append(versions, version)
	}
	sort.Slice(versions, func(left, right int) bool {
		if versions[left].PublishedAt == versions[right].PublishedAt {
			return versions[left].Version > versions[right].Version
		}
		return versions[left].PublishedAt > versions[right].PublishedAt
	})
	return versions, nil
}

func samePublishedArtifact(left, right entity.InferenceModelVersion) bool {
	left.ID, right.ID = "", ""
	left.PublishedAt, right.PublishedAt = 0, 0
	return reflect.DeepEqual(left, right)
}
