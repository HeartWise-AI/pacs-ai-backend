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
	insertedUser         repositoryTypes.CreateTenantUser
	insertCalls          int
	verifiedInvite       string
	verificationContexts chan context.Context
	verificationRelease  chan struct{}
	policyAcceptances    []entity.UserPolicyAcceptance
	policyAcceptanceErr  error
	deleteCalls          int
}

func (repository *registrationCommandRepository) InsertTenantUser(_ context.Context, data repositoryTypes.CreateTenantUser) (string, error) {
	repository.insertedUser = data
	repository.insertCalls++
	return "user-id", nil
}

func (repository *registrationCommandRepository) InsertUserPolicyAcceptances(_ context.Context, acceptances []entity.UserPolicyAcceptance) error {
	repository.policyAcceptances = append(repository.policyAcceptances, acceptances...)
	return repository.policyAcceptanceErr
}

func (repository *registrationCommandRepository) DeleteTenantUser(_ context.Context, _, _ string) error {
	repository.deleteCalls++
	return nil
}

func (repository *registrationCommandRepository) UpdateTenantUserEmailInviteVerifiedAt(_ context.Context, id string) error {
	repository.verifiedInvite = id
	return nil
}

func (repository *registrationCommandRepository) GenerateTenantUserEmailVerificationLink(ctx context.Context, _, _ string) (string, error) {
	if repository.verificationContexts != nil {
		repository.verificationContexts <- ctx
		if repository.verificationRelease != nil {
			<-repository.verificationRelease
		}
	}
	return "", errors.New("stop after capturing verification context")
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

type registrationRateLimiter struct {
	input      serviceTypes.RegistrationRateLimit
	retryAfter time.Duration
	err        error
	calls      int
}

func (limiter *registrationRateLimiter) CheckRegistrationAttempt(
	_ context.Context,
	data serviceTypes.RegistrationRateLimit,
) (time.Duration, error) {
	limiter.input = data
	limiter.calls++
	return limiter.retryAfter, limiter.err
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
		CloudflareAPIInterface:           turnstileAPI,
		RegistrationRateLimiterInterface: &registrationRateLimiter{},
		UserCommandRepositoryInterface:   commandRepository,
		UserQueryRepositoryInterface:     queryRepository,
		TenantQueryServiceInterface:      &registrationTenantQueryService{},
		PolicyCatalog:                    testPolicyCatalog(),
	}

	err := service.RegisterTenantUser(context.Background(), serviceTypes.RegisterTenantUser{
		TenantID:          "tenant-a",
		TurnstileToken:    "valid-turnstile-token",
		Name:              "Public User",
		Email:             "public.user@example.com",
		Password:          "ValidPassword!",
		LicenseNo:         "demo-license",
		Specialty:         "demo-specialty",
		Code:              &code,
		PolicyAcceptances: currentPolicyAcceptanceInputs(),
	})

	require.NoError(t, err)
	require.Equal(t, 1, turnstileAPI.calls)
	require.Equal(t, "valid-turnstile-token", turnstileAPI.token)
	require.Equal(t, iamEntity.UserRole, commandRepository.insertedUser.Role)
	require.Equal(t, "invite-id", commandRepository.verifiedInvite)
	require.True(t, commandRepository.insertedUser.IsEmailVerified)
	require.Len(t, commandRepository.policyAcceptances, 2)
	require.Equal(t, entity.PolicyAcceptanceSourceRegistration, commandRepository.policyAcceptances[0].Source)
}

func TestRegisterTenantUserVerificationEmailOutlivesRequestContext(t *testing.T) {
	verificationContexts := make(chan context.Context, 1)
	verificationRelease := make(chan struct{})
	commandRepository := &registrationCommandRepository{
		verificationContexts: verificationContexts,
		verificationRelease:  verificationRelease,
	}
	t.Cleanup(func() { close(verificationRelease) })
	service := UserCommandService{
		CloudflareAPIInterface:           &registrationTurnstileAPI{response: cloudflareAPITypes.ValidateTurnstileTokenResponse{Success: true}},
		RegistrationRateLimiterInterface: &registrationRateLimiter{},
		UserCommandRepositoryInterface:   commandRepository,
		UserQueryRepositoryInterface:     &registrationQueryRepository{},
		TenantQueryServiceInterface:      &registrationTenantQueryService{},
		PolicyCatalog:                    testPolicyCatalog(),
	}
	requestContext, cancelRequest := context.WithCancel(context.Background())

	err := service.RegisterTenantUser(requestContext, serviceTypes.RegisterTenantUser{
		TenantID:          "tenant-a",
		TurnstileToken:    "valid-turnstile-token",
		Name:              "Public User",
		Email:             "public.user@example.com",
		Password:          "ValidPassword!",
		LicenseNo:         "demo-license",
		Specialty:         "demo-specialty",
		PolicyAcceptances: currentPolicyAcceptanceInputs(),
	})
	require.NoError(t, err)
	cancelRequest()

	select {
	case verificationContext := <-verificationContexts:
		require.NoError(t, verificationContext.Err())
		deadline, hasDeadline := verificationContext.Deadline()
		require.True(t, hasDeadline)
		require.WithinDuration(t, time.Now().Add(registrationVerificationEmailTimeout), deadline, time.Second)
	case <-time.After(time.Second):
		t.Fatal("verification email was not started")
	}
}

func TestRegisterTenantUserRejectsThrottledAttemptBeforeTurnstileAndAccountOperations(t *testing.T) {
	limiter := &registrationRateLimiter{retryAfter: 90 * time.Second}
	turnstileAPI := &registrationTurnstileAPI{response: cloudflareAPITypes.ValidateTurnstileTokenResponse{Success: true}}
	commandRepository := &registrationCommandRepository{}
	queryRepository := &registrationQueryRepository{}
	tenantService := &registrationTenantQueryService{}
	service := UserCommandService{
		CloudflareAPIInterface:           turnstileAPI,
		RegistrationRateLimiterInterface: limiter,
		UserCommandRepositoryInterface:   commandRepository,
		UserQueryRepositoryInterface:     queryRepository,
		TenantQueryServiceInterface:      tenantService,
	}

	err := service.RegisterTenantUser(context.Background(), serviceTypes.RegisterTenantUser{
		TenantID:       "tenant-a",
		TurnstileToken: "unused-turnstile-token",
		ClientIP:       "203.0.113.10",
		Email:          "public.user@example.com",
	})

	var rateLimitError *apiError.RegistrationRateLimitError
	require.ErrorAs(t, err, &rateLimitError)
	require.Equal(t, 90, rateLimitError.RetryAfterSeconds)
	require.Equal(t, 1, limiter.calls)
	require.Equal(t, "tenant-a", limiter.input.TenantID)
	require.Equal(t, "public.user@example.com", limiter.input.Email)
	require.Equal(t, "203.0.113.10", limiter.input.ClientIP)
	require.Equal(t, 0, turnstileAPI.calls)
	require.Equal(t, 0, tenantService.calls)
	require.Equal(t, 0, queryRepository.selectEmailCalls)
	require.Equal(t, 0, commandRepository.insertCalls)
}

func TestRegisterTenantUserRejectsDuplicateAfterRateLimitAndTurnstile(t *testing.T) {
	limiter := &registrationRateLimiter{}
	turnstileAPI := &registrationTurnstileAPI{response: cloudflareAPITypes.ValidateTurnstileTokenResponse{Success: true}}
	commandRepository := &registrationCommandRepository{}
	queryRepository := &duplicateRegistrationQueryRepository{}
	service := UserCommandService{
		CloudflareAPIInterface:           turnstileAPI,
		RegistrationRateLimiterInterface: limiter,
		UserCommandRepositoryInterface:   commandRepository,
		UserQueryRepositoryInterface:     queryRepository,
		TenantQueryServiceInterface:      &registrationTenantQueryService{},
		PolicyCatalog:                    testPolicyCatalog(),
	}

	err := service.RegisterTenantUser(context.Background(), serviceTypes.RegisterTenantUser{
		TenantID:          "tenant-a",
		TurnstileToken:    "valid-turnstile-token",
		ClientIP:          "203.0.113.10",
		Email:             "existing.user@example.com",
		PolicyAcceptances: currentPolicyAcceptanceInputs(),
	})

	require.EqualError(t, err, apiError.DuplicateRecord)
	require.Equal(t, 1, limiter.calls)
	require.Equal(t, 1, turnstileAPI.calls)
	require.Equal(t, 1, queryRepository.selectEmailCalls)
	require.Equal(t, 0, commandRepository.insertCalls)
}

func currentPolicyAcceptanceInputs() []serviceTypes.PolicyAcceptanceInput {
	return []serviceTypes.PolicyAcceptanceInput{
		{PolicyKey: entity.PolicyTermsOfService, Version: "v1"},
		{PolicyKey: entity.PolicyPrivacyPolicy, Version: "v1"},
	}
}

func TestRegisterTenantUserRollsBackAccountWhenPolicyPersistenceFails(t *testing.T) {
	commandRepository := &registrationCommandRepository{policyAcceptanceErr: errors.New(apiError.FirestoreError)}
	service := UserCommandService{
		CloudflareAPIInterface:           &registrationTurnstileAPI{response: cloudflareAPITypes.ValidateTurnstileTokenResponse{Success: true}},
		RegistrationRateLimiterInterface: &registrationRateLimiter{},
		UserCommandRepositoryInterface:   commandRepository,
		UserQueryRepositoryInterface:     &registrationQueryRepository{},
		TenantQueryServiceInterface:      &registrationTenantQueryService{},
		PolicyCatalog:                    testPolicyCatalog(),
	}

	err := service.RegisterTenantUser(context.Background(), serviceTypes.RegisterTenantUser{
		TenantID: "tenant-a", TurnstileToken: "valid-token", Name: "Public User",
		Email: "public.user@example.com", Password: "ValidPassword!", LicenseNo: "demo", Specialty: "demo",
		PolicyAcceptances: currentPolicyAcceptanceInputs(),
	})

	require.EqualError(t, err, apiError.FirestoreError)
	require.Equal(t, 1, commandRepository.insertCalls)
	require.Equal(t, 1, commandRepository.deleteCalls)
}

func TestRegisterTenantUserRejectsStalePoliciesBeforeCreatingAccount(t *testing.T) {
	commandRepository := &registrationCommandRepository{}
	service := UserCommandService{
		CloudflareAPIInterface:           &registrationTurnstileAPI{response: cloudflareAPITypes.ValidateTurnstileTokenResponse{Success: true}},
		RegistrationRateLimiterInterface: &registrationRateLimiter{},
		UserCommandRepositoryInterface:   commandRepository,
		UserQueryRepositoryInterface:     &registrationQueryRepository{},
		TenantQueryServiceInterface:      &registrationTenantQueryService{},
		PolicyCatalog:                    testPolicyCatalog(),
	}

	err := service.RegisterTenantUser(context.Background(), serviceTypes.RegisterTenantUser{
		TenantID: "tenant-a", TurnstileToken: "valid-token", Email: "public.user@example.com",
		PolicyAcceptances: []serviceTypes.PolicyAcceptanceInput{
			{PolicyKey: entity.PolicyTermsOfService, Version: "old"},
			{PolicyKey: entity.PolicyPrivacyPolicy, Version: "v1"},
		},
	})

	require.EqualError(t, err, apiError.PolicyVersionStale)
	require.Zero(t, commandRepository.insertCalls)
}

type duplicateRegistrationQueryRepository struct {
	repository.UserQueryRepositoryInterface
	selectEmailCalls int
}

func (repository *duplicateRegistrationQueryRepository) SelectTenantUserByEmail(context.Context, string, string) (repositoryTypes.GetTenantUser, error) {
	repository.selectEmailCalls++
	return repositoryTypes.GetTenantUser{Email: "existing.user@example.com"}, nil
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
				CloudflareAPIInterface:           turnstileAPI,
				RegistrationRateLimiterInterface: &registrationRateLimiter{},
				UserCommandRepositoryInterface:   commandRepository,
				UserQueryRepositoryInterface:     queryRepository,
				TenantQueryServiceInterface:      tenantService,
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
