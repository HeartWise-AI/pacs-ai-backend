package service

import (
	"context"
	"errors"
	"testing"
	"time"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/user/domain/entity"
	"api-pacs/module/user/domain/repository"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
)

type viewerTourQueryRepository struct {
	repository.UserQueryRepositoryInterface
	metadata entity.UserMetadata
	err      error
}

func (repo *viewerTourQueryRepository) SelectUserMetadataByID(_ context.Context, _ string) (entity.UserMetadata, error) {
	return repo.metadata, repo.err
}

type viewerTourCommandRepository struct {
	repository.UserCommandRepositoryInterface
	updates []repositoryTypes.UpdateUserViewerTourCompletedAt
	err     error
}

func (repo *viewerTourCommandRepository) UpdateUserViewerTourCompletedAt(_ context.Context, data repositoryTypes.UpdateUserViewerTourCompletedAt) error {
	repo.updates = append(repo.updates, data)
	return repo.err
}

func int64Ptr(v int64) *int64 { return &v }

func TestGetUserViewerTourStateTreatsMissingMetadataAsNotCompleted(t *testing.T) {
	service := &UserQueryService{UserQueryRepositoryInterface: &viewerTourQueryRepository{err: errors.New(apiError.MissingRecord)}}

	state, err := service.GetUserViewerTourState(context.Background(), "user-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.UserID != "user-a" || state.ViewerTourCompletedAt != nil {
		t.Fatalf("unexpected state: %+v", state)
	}
}

func TestGetUserViewerTourStateReturnsStoredCompletion(t *testing.T) {
	service := &UserQueryService{UserQueryRepositoryInterface: &viewerTourQueryRepository{
		metadata: entity.UserMetadata{UserID: "user-a", ViewerTourCompletedAt: int64Ptr(42)},
	}}

	state, err := service.GetUserViewerTourState(context.Background(), "user-a")
	if err != nil || state.ViewerTourCompletedAt == nil || *state.ViewerTourCompletedAt != 42 {
		t.Fatalf("unexpected state %+v err %v", state, err)
	}
}

func TestGetUserViewerTourStatePropagatesStoreErrors(t *testing.T) {
	service := &UserQueryService{UserQueryRepositoryInterface: &viewerTourQueryRepository{err: errors.New(apiError.FirestoreError)}}

	if _, err := service.GetUserViewerTourState(context.Background(), "user-a"); err == nil || err.Error() != apiError.FirestoreError {
		t.Fatalf("expected firestore error, got %v", err)
	}
}

func TestCompleteUserViewerTourStoresNowWhenNotCompleted(t *testing.T) {
	previous := viewerTourNow
	viewerTourNow = func() time.Time { return time.Unix(1763100000, 0) }
	defer func() { viewerTourNow = previous }()

	commandRepo := &viewerTourCommandRepository{}
	service := &UserCommandService{
		UserQueryRepositoryInterface:   &viewerTourQueryRepository{err: errors.New(apiError.MissingRecord)},
		UserCommandRepositoryInterface: commandRepo,
	}

	state, err := service.CompleteUserViewerTour(context.Background(), "user-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(commandRepo.updates) != 1 || commandRepo.updates[0].UserID != "user-a" ||
		commandRepo.updates[0].CompletedAt == nil || *commandRepo.updates[0].CompletedAt != 1763100000 {
		t.Fatalf("unexpected updates: %+v", commandRepo.updates)
	}
	if state.ViewerTourCompletedAt == nil || *state.ViewerTourCompletedAt != 1763100000 {
		t.Fatalf("unexpected state: %+v", state)
	}
}

func TestCompleteUserViewerTourKeepsExistingCompletion(t *testing.T) {
	commandRepo := &viewerTourCommandRepository{}
	service := &UserCommandService{
		UserQueryRepositoryInterface:   &viewerTourQueryRepository{metadata: entity.UserMetadata{ViewerTourCompletedAt: int64Ptr(7)}},
		UserCommandRepositoryInterface: commandRepo,
	}

	state, err := service.CompleteUserViewerTour(context.Background(), "user-a")
	if err != nil || state.ViewerTourCompletedAt == nil || *state.ViewerTourCompletedAt != 7 {
		t.Fatalf("unexpected state %+v err %v", state, err)
	}
	if len(commandRepo.updates) != 0 {
		t.Fatalf("expected no write, got %+v", commandRepo.updates)
	}
}

func TestCompleteUserViewerTourDoesNotWriteWhenReadFails(t *testing.T) {
	commandRepo := &viewerTourCommandRepository{}
	service := &UserCommandService{
		UserQueryRepositoryInterface:   &viewerTourQueryRepository{err: errors.New(apiError.FirestoreError)},
		UserCommandRepositoryInterface: commandRepo,
	}

	if _, err := service.CompleteUserViewerTour(context.Background(), "user-a"); err == nil {
		t.Fatal("expected error")
	}
	if len(commandRepo.updates) != 0 {
		t.Fatalf("expected no write, got %+v", commandRepo.updates)
	}
}

func TestResetUserViewerTourClearsCompletion(t *testing.T) {
	commandRepo := &viewerTourCommandRepository{}
	service := &UserCommandService{UserCommandRepositoryInterface: commandRepo}

	state, err := service.ResetUserViewerTour(context.Background(), "user-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(commandRepo.updates) != 1 || commandRepo.updates[0].UserID != "user-a" || commandRepo.updates[0].CompletedAt != nil {
		t.Fatalf("unexpected updates: %+v", commandRepo.updates)
	}
	if state.UserID != "user-a" || state.ViewerTourCompletedAt != nil {
		t.Fatalf("unexpected state: %+v", state)
	}
}
