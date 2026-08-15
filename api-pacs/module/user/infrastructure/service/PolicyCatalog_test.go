package service

import (
	"testing"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/user/domain/entity"
	"api-pacs/module/user/infrastructure/service/types"
)

func TestPolicyCatalogDefaultsToCanonicalVitrinePolicies(t *testing.T) {
	for _, name := range []string{
		"POLICY_TERMS_VERSION", "POLICY_TERMS_URL", "POLICY_TERMS_EFFECTIVE_AT",
		"POLICY_PRIVACY_VERSION", "POLICY_PRIVACY_URL", "POLICY_PRIVACY_EFFECTIVE_AT",
	} {
		t.Setenv(name, "")
	}

	policies, err := PolicyCatalogFromEnvironment().CurrentPolicies("tenant-a")

	require.NoError(t, err)
	require.Equal(t, []string{entity.PolicyTermsOfService, entity.PolicyPrivacyPolicy}, []string{policies[0].PolicyKey, policies[1].PolicyKey})
	require.Equal(t, defaultTermsURL, policies[0].URL)
	require.Equal(t, defaultPrivacyURL, policies[1].URL)
	require.True(t, policies[0].Required)
	require.True(t, policies[1].Required)
}

func TestPolicyCatalogRejectsMissingAndStaleAcceptance(t *testing.T) {
	catalog := testPolicyCatalog()

	_, err := catalog.ValidateAcceptances("tenant-a", []types.PolicyAcceptanceInput{{PolicyKey: entity.PolicyTermsOfService, Version: "v1"}})
	require.EqualError(t, err, apiError.PolicyAcceptanceRequired)

	_, err = catalog.ValidateAcceptances("tenant-a", []types.PolicyAcceptanceInput{
		{PolicyKey: entity.PolicyTermsOfService, Version: "old"},
		{PolicyKey: entity.PolicyPrivacyPolicy, Version: "v1"},
	})
	require.EqualError(t, err, apiError.PolicyVersionStale)
}

func TestPolicyCatalogAcceptsExactCurrentSet(t *testing.T) {
	current, err := testPolicyCatalog().ValidateAcceptances("tenant-a", []types.PolicyAcceptanceInput{
		{PolicyKey: entity.PolicyTermsOfService, Version: "v1"},
		{PolicyKey: entity.PolicyPrivacyPolicy, Version: "v1"},
	})

	require.NoError(t, err)
	require.Len(t, current, 2)
}

func TestPolicyCatalogFailsClosedForInvalidURL(t *testing.T) {
	catalog := NewPolicyCatalog([]types.PolicyDefinition{{
		PolicyKey: entity.PolicyTermsOfService, Version: "v1", Title: "Terms", URL: "http://unsafe.example/terms",
		EffectiveAt: "2026-08-15", AcceptanceAction: "AGREE", Required: true,
	}})

	_, err := catalog.CurrentPolicies("tenant-a")
	require.EqualError(t, err, apiError.PolicyConfigurationUnavailable)
}

func testPolicyCatalog() *PolicyCatalog {
	return NewPolicyCatalog([]types.PolicyDefinition{
		{PolicyKey: entity.PolicyTermsOfService, Version: "v1", Title: "Terms", URL: "https://example.test/terms", EffectiveAt: "2026-08-15", AcceptanceAction: "AGREE", Required: true},
		{PolicyKey: entity.PolicyPrivacyPolicy, Version: "v1", Title: "Privacy", URL: "https://example.test/privacy", EffectiveAt: "2026-08-15", AcceptanceAction: "ACKNOWLEDGE", Required: true},
	})
}
