package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	iamEntity "api-pacs/module/iam/domain/entity"
	tenantApplication "api-pacs/module/tenant/application"
	tenantTypes "api-pacs/module/tenant/infrastructure/service/types"
	"api-pacs/module/user/domain/entity"
	"api-pacs/module/user/domain/repository"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
	serviceTypes "api-pacs/module/user/infrastructure/service/types"
)

type registrationCommandRepository struct {
	repository.UserCommandRepositoryInterface
	insertedUser   repositoryTypes.CreateTenantUser
	verifiedInvite string
}

func (repository *registrationCommandRepository) InsertTenantUser(_ context.Context, data repositoryTypes.CreateTenantUser) (string, error) {
	repository.insertedUser = data
	return "user-id", nil
}

func (repository *registrationCommandRepository) UpdateTenantUserEmailInviteVerifiedAt(_ context.Context, id string) error {
	repository.verifiedInvite = id
	return nil
}

type registrationQueryRepository struct {
	repository.UserQueryRepositoryInterface
	invite entity.UserEmailInvite
}

func (repository *registrationQueryRepository) SelectTenantUserByEmail(context.Context, string, string) (repositoryTypes.GetTenantUser, error) {
	return repositoryTypes.GetTenantUser{}, errors.New(apiError.MissingRecord)
}

func (repository *registrationQueryRepository) SelectTenantUserEmailInviteByEmail(context.Context, string, string) (entity.UserEmailInvite, error) {
	return repository.invite, nil
}

type registrationTenantQueryService struct {
	tenantApplication.TenantQueryServiceInterface
}

func (service *registrationTenantQueryService) GetTenantByID(context.Context, string) (tenantTypes.GetTenantResult, error) {
	return tenantTypes.GetTenantResult{ID: "tenant-a", OnboardingEnableRegistration: true}, nil
}

func TestRegisterTenantUserPersistsServerOwnedUserRole(t *testing.T) {
	code := "valid-code"
	commandRepository := &registrationCommandRepository{}
	queryRepository := &registrationQueryRepository{invite: entity.UserEmailInvite{
		ID:        "invite-id",
		TenantID:  "tenant-a",
		Email:     "public.user@example.com",
		Code:      code,
		ExpiresAt: int(time.Now().Add(time.Hour).Unix()),
	}}
	service := UserCommandService{
		UserCommandRepositoryInterface: commandRepository,
		UserQueryRepositoryInterface:   queryRepository,
		TenantQueryServiceInterface:    &registrationTenantQueryService{},
	}

	err := service.RegisterTenantUser(context.Background(), serviceTypes.RegisterTenantUser{
		TenantID:  "tenant-a",
		Name:      "Public User",
		Email:     "public.user@example.com",
		Password:  "ValidPassword!",
		LicenseNo: "demo-license",
		Specialty: "demo-specialty",
		Code:      &code,
	})

	require.NoError(t, err)
	require.Equal(t, iamEntity.UserRole, commandRepository.insertedUser.Role)
	require.Equal(t, "invite-id", commandRepository.verifiedInvite)
	require.True(t, commandRepository.insertedUser.IsEmailVerified)
}
