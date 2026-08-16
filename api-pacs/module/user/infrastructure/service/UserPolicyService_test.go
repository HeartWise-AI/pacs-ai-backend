package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"api-pacs/module/user/domain/entity"
	"api-pacs/module/user/domain/repository"
	"api-pacs/module/user/infrastructure/service/types"
)

type policyTestRepository struct {
	repository.UserCommandRepositoryInterface
	repository.UserQueryRepositoryInterface
	acceptances map[string]entity.UserPolicyAcceptance
}

func newPolicyTestRepository() *policyTestRepository {
	return &policyTestRepository{acceptances: make(map[string]entity.UserPolicyAcceptance)}
}

func (repository *policyTestRepository) InsertUserPolicyAcceptances(_ context.Context, acceptances []entity.UserPolicyAcceptance) error {
	for _, acceptance := range acceptances {
		if _, exists := repository.acceptances[acceptance.DocumentID()]; !exists {
			repository.acceptances[acceptance.DocumentID()] = acceptance
		}
	}
	return nil
}

func (repository *policyTestRepository) SelectUserPolicyAcceptances(_ context.Context, tenantID, userID string, policies []entity.PolicyReference) ([]entity.UserPolicyAcceptance, error) {
	result := make([]entity.UserPolicyAcceptance, 0, len(policies))
	for _, policy := range policies {
		candidate := entity.UserPolicyAcceptance{TenantID: tenantID, UserID: userID, PolicyKey: policy.PolicyKey, Version: policy.Version}
		if acceptance, exists := repository.acceptances[candidate.DocumentID()]; exists {
			result = append(result, acceptance)
		}
	}
	return result, nil
}

func TestAcceptPoliciesIsIdempotentAndStatusIsTenantScoped(t *testing.T) {
	repository := newPolicyTestRepository()
	command := &UserCommandService{UserCommandRepositoryInterface: repository, PolicyCatalog: testPolicyCatalog()}
	query := &UserQueryService{UserQueryRepositoryInterface: repository, PolicyCatalog: testPolicyCatalog()}
	request := types.AcceptPolicies{
		TenantID: "tenant-a", UserID: "user-a", Source: entity.PolicyAcceptanceSourceAuthenticated,
		Acceptances: []types.PolicyAcceptanceInput{{PolicyKey: entity.PolicyTermsOfService, Version: "v1"}, {PolicyKey: entity.PolicyPrivacyPolicy, Version: "v1"}},
	}

	require.NoError(t, command.AcceptPolicies(context.Background(), request))
	require.NoError(t, command.AcceptPolicies(context.Background(), request))
	require.Len(t, repository.acceptances, 2)

	status, err := query.GetPolicyStatus(context.Background(), "tenant-a", "user-a")
	require.NoError(t, err)
	require.False(t, status.AcceptanceRequired)
	require.True(t, status.Policies[0].Accepted)
	require.NotNil(t, status.Policies[0].AcceptedAt)

	otherTenantStatus, err := query.GetPolicyStatus(context.Background(), "tenant-b", "user-a")
	require.NoError(t, err)
	require.True(t, otherTenantStatus.AcceptanceRequired)
}

func TestPolicyVersionChangeRequiresReacceptance(t *testing.T) {
	repository := newPolicyTestRepository()
	command := &UserCommandService{UserCommandRepositoryInterface: repository, PolicyCatalog: testPolicyCatalog()}
	request := types.AcceptPolicies{
		TenantID: "tenant-a", UserID: "user-a", Source: entity.PolicyAcceptanceSourceAuthenticated,
		Acceptances: []types.PolicyAcceptanceInput{{PolicyKey: entity.PolicyTermsOfService, Version: "v1"}, {PolicyKey: entity.PolicyPrivacyPolicy, Version: "v1"}},
	}
	require.NoError(t, command.AcceptPolicies(context.Background(), request))

	updatedCatalog := NewPolicyCatalog([]types.PolicyDefinition{
		{PolicyKey: entity.PolicyTermsOfService, Version: "v2", Title: "Terms", URL: "https://example.test/terms", EffectiveAt: "2026-09-01", AcceptanceAction: "AGREE", Required: true},
		{PolicyKey: entity.PolicyPrivacyPolicy, Version: "v1", Title: "Privacy", URL: "https://example.test/privacy", EffectiveAt: "2026-08-15", AcceptanceAction: "ACKNOWLEDGE", Required: true},
	})
	query := &UserQueryService{UserQueryRepositoryInterface: repository, PolicyCatalog: updatedCatalog}

	status, err := query.GetPolicyStatus(context.Background(), "tenant-a", "user-a")
	require.NoError(t, err)
	require.True(t, status.AcceptanceRequired)
	require.False(t, status.Policies[0].Accepted)
	require.True(t, status.Policies[1].Accepted)
}
