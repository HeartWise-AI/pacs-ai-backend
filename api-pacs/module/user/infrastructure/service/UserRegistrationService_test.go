package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	cloudflareAPITypes "api-pacs/infrastructures/providers/api/cloudflare/types"
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
	insertCalls    int
	verifiedInvite string
}

func (repository *registrationCommandRepository) InsertTenantUser(_ context.Context, data repositoryTypes.CreateTenantUser) (string, error) {
	repository.insertedUser = data
	repository.insertCalls++
	return "user-id", nil
}

func (repository *registrationCommandRepository) UpdateTenantUserEmailInviteVerifiedAt(_ context.Context, id string) error {
	repository.verifiedInvite = id
	return nil
}

type registrationQueryRepository struct {
	repository.UserQueryRepositoryInterface
	invite           entity.UserEmailInvite
	selectEmailCalls int
}

func (repository *registrationQueryRepository) SelectTenantUserByEmail(context.Context, string, string) (repositoryTypes.GetTenantUser, error) {
	repository.selectEmailCalls++
	return repositoryTypes.GetTenantUser{}, errors.New(apiError.MissingRecord)
}

func (repository *registrationQueryRepository) SelectTenantUserEmailInviteByEmail(context.Context, string, string) (entity.UserEmailInvite, error) {
	return repository.invite, nil
}

type registrationTenantQueryService struct {
	tenantApplication.TenantQueryServiceInterface
	calls int
}

func (service *registrationTenantQueryService) GetTenantByID(context.Context, string) (tenantTypes.GetTenantResult, error) {
	service.calls++
	return tenantTypes.GetTenantResult{ID: "tenant-a", OnboardingEnableRegistration: true}, nil
}

type registrationTurnstileAPI struct {
	cloudflareAPITypes.CloudflareAPIInterface
	response cloudflareAPITypes.ValidateTurnstileTokenResponse
	err      error
	token    string
	calls    int
}

func (api *registrationTurnstileAPI) ValidateTurnstileToken(_ context.Context, token string) (cloudflareAPITypes.ValidateTurnstileTokenResponse, error) {
	api.token = token
	api.calls++
	return api.response, api.err
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
	turnstileAPI := &registrationTurnstileAPI{response: cloudflareAPITypes.ValidateTurnstileTokenResponse{Success: true}}
	service := UserCommandService{
		CloudflareAPIInterface:         turnstileAPI,
		UserCommandRepositoryInterface: commandRepository,
		UserQueryRepositoryInterface:   queryRepository,
		TenantQueryServiceInterface:    &registrationTenantQueryService{},
	}

	err := service.RegisterTenantUser(context.Background(), serviceTypes.RegisterTenantUser{
		TenantID:       "tenant-a",
		TurnstileToken: "valid-turnstile-token",
		Name:           "Public User",
		Email:          "public.user@example.com",
		Password:       "ValidPassword!",
		LicenseNo:      "demo-license",
		Specialty:      "demo-specialty",
		Code:           &code,
	})

	require.NoError(t, err)
	require.Equal(t, 1, turnstileAPI.calls)
	require.Equal(t, "valid-turnstile-token", turnstileAPI.token)
	require.Equal(t, iamEntity.UserRole, commandRepository.insertedUser.Role)
	require.Equal(t, "invite-id", commandRepository.verifiedInvite)
	require.True(t, commandRepository.insertedUser.IsEmailVerified)
}

func TestRegisterTenantUserRejectsTurnstileFailureBeforeAccountOperations(t *testing.T) {
	testCases := []struct {
		name        string
		response    cloudflareAPITypes.ValidateTurnstileTokenResponse
		providerErr error
		expectedErr string
	}{
		{
			name: "invalid response",
			response: cloudflareAPITypes.ValidateTurnstileTokenResponse{
				Success: false, ErrorCodes: []string{"invalid-input-response"},
			},
			expectedErr: apiError.TurnstileInvalid,
		},
		{
			name: "expired or replayed response",
			response: cloudflareAPITypes.ValidateTurnstileTokenResponse{
				Success: false, ErrorCodes: []string{"timeout-or-duplicate"},
			},
			expectedErr: apiError.TurnstileInvalid,
		},
		{
			name: "invalid server secret",
			response: cloudflareAPITypes.ValidateTurnstileTokenResponse{
				Success: false, ErrorCodes: []string{"invalid-input-secret"},
			},
			expectedErr: apiError.CloudflareAPIError,
		},
		{
			name:        "provider transport unavailable",
			providerErr: errors.New("network failure"),
			expectedErr: apiError.CloudflareAPIError,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			commandRepository := &registrationCommandRepository{}
			queryRepository := &registrationQueryRepository{}
			tenantService := &registrationTenantQueryService{}
			turnstileAPI := &registrationTurnstileAPI{response: testCase.response, err: testCase.providerErr}
			service := UserCommandService{
				CloudflareAPIInterface:         turnstileAPI,
				UserCommandRepositoryInterface: commandRepository,
				UserQueryRepositoryInterface:   queryRepository,
				TenantQueryServiceInterface:    tenantService,
			}

			err := service.RegisterTenantUser(context.Background(), serviceTypes.RegisterTenantUser{
				TenantID:       "tenant-a",
				TurnstileToken: "rejected-turnstile-token",
				Email:          "public.user@example.com",
			})

			require.EqualError(t, err, testCase.expectedErr)
			require.Equal(t, 1, turnstileAPI.calls)
			require.Equal(t, 0, tenantService.calls)
			require.Equal(t, 0, queryRepository.selectEmailCalls)
			require.Equal(t, 0, commandRepository.insertCalls)
		})
	}
}
