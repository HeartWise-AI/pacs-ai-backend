package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/user/domain/entity"
	"api-pacs/module/user/domain/repository"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
	"api-pacs/module/user/infrastructure/service/types"
)

type metadataUpsertCapture struct {
	repository.UserCommandRepositoryInterface
	upserts []repositoryTypes.UpsertUserMetadata
}

func (capture *metadataUpsertCapture) UpsertUserMetadata(_ context.Context, data repositoryTypes.UpsertUserMetadata) error {
	capture.upserts = append(capture.upserts, data)
	return nil
}

func decodeStoredMetadata(t *testing.T, raw string) map[string]interface{} {
	t.Helper()
	decoded := map[string]interface{}{}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("stored metadata is not JSON: %v", err)
	}
	return decoded
}

func TestUpdateUserMetadataMergesKeysWithStoredMetadata(t *testing.T) {
	capture := &metadataUpsertCapture{}
	service := &UserCommandService{
		UserQueryRepositoryInterface: &viewerTourQueryRepository{metadata: entity.UserMetadata{
			UserID: "user-a", Metadata: `{"tutorialProgressStep":3,"notifyAutomaticRuns":true}`,
		}},
		UserCommandRepositoryInterface: capture,
	}

	if err := service.UpdateUserMetadata(context.Background(), types.UpdateUserMetadata{
		UserID: "user-a", Metadata: map[string]interface{}{"notifyAutomaticRuns": false},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stored := decodeStoredMetadata(t, capture.upserts[0].Metadata)
	if stored["tutorialProgressStep"] != float64(3) || stored["notifyAutomaticRuns"] != false {
		t.Fatalf("metadata was not merged: %v", stored)
	}
}

func TestUpdateUserMetadataWithoutStoredDocument(t *testing.T) {
	capture := &metadataUpsertCapture{}
	service := &UserCommandService{
		UserQueryRepositoryInterface:   &viewerTourQueryRepository{err: errors.New(apiError.MissingRecord)},
		UserCommandRepositoryInterface: capture,
	}

	if err := service.UpdateUserMetadata(context.Background(), types.UpdateUserMetadata{
		UserID: "user-a", Metadata: map[string]interface{}{"tutorialProgressStep": 1},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stored := decodeStoredMetadata(t, capture.upserts[0].Metadata)
	if len(stored) != 1 || stored["tutorialProgressStep"] != float64(1) {
		t.Fatalf("unexpected metadata: %v", stored)
	}
}

func TestUpdateUserMetadataPropagatesReadErrors(t *testing.T) {
	capture := &metadataUpsertCapture{}
	service := &UserCommandService{
		UserQueryRepositoryInterface:   &viewerTourQueryRepository{err: errors.New(apiError.FirestoreError)},
		UserCommandRepositoryInterface: capture,
	}
	if err := service.UpdateUserMetadata(context.Background(), types.UpdateUserMetadata{
		UserID: "user-a", Metadata: map[string]interface{}{"x": 1},
	}); err == nil {
		t.Fatal("expected the read error")
	}
	if len(capture.upserts) != 0 {
		t.Fatal("must not overwrite metadata it could not read")
	}
}
