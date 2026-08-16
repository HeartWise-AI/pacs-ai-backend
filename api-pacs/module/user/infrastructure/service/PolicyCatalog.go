package service

import (
	"errors"
	"net/url"
	"os"
	"strings"
	"time"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/user/domain/entity"
	"api-pacs/module/user/infrastructure/service/types"
)

const (
	defaultPolicyVersion     = "2026-08-15"
	defaultPolicyEffectiveAt = "2026-08-15"
	defaultTermsURL          = "https://pacsai.co/terms-of-service"
	defaultPrivacyURL        = "https://pacsai.co/privacy-policy"
)

// PolicyCatalog is the deployment-owned source of current policy metadata.
// URLs and versions are never accepted as authoritative client input.
type PolicyCatalog struct {
	policies               []types.PolicyDefinition
	existingUserGraceUntil *time.Time
	err                    error
}

func PolicyCatalogFromEnvironment() *PolicyCatalog {
	policies := []types.PolicyDefinition{
		{
			PolicyKey: entity.PolicyTermsOfService, Title: "Terms of Service",
			Version:          envOrDefault("POLICY_TERMS_VERSION", defaultPolicyVersion),
			URL:              envOrDefault("POLICY_TERMS_URL", defaultTermsURL),
			EffectiveAt:      envOrDefault("POLICY_TERMS_EFFECTIVE_AT", defaultPolicyEffectiveAt),
			AcceptanceAction: "AGREE", Required: true,
		},
		{
			PolicyKey: entity.PolicyPrivacyPolicy, Title: "Privacy Policy",
			Version:          envOrDefault("POLICY_PRIVACY_VERSION", defaultPolicyVersion),
			URL:              envOrDefault("POLICY_PRIVACY_URL", defaultPrivacyURL),
			EffectiveAt:      envOrDefault("POLICY_PRIVACY_EFFECTIVE_AT", defaultPolicyEffectiveAt),
			AcceptanceAction: "ACKNOWLEDGE", Required: true,
		},
	}

	catalog := &PolicyCatalog{policies: policies}
	if rawGraceUntil := strings.TrimSpace(os.Getenv("POLICY_EXISTING_USER_GRACE_UNTIL")); rawGraceUntil != "" {
		graceUntil, err := time.Parse(time.RFC3339, rawGraceUntil)
		if err != nil {
			catalog.err = errors.New(apiError.PolicyConfigurationUnavailable)
		} else {
			catalog.existingUserGraceUntil = &graceUntil
		}
	}
	for _, policy := range policies {
		if err := validatePolicyDefinition(policy); err != nil {
			catalog.err = errors.New(apiError.PolicyConfigurationUnavailable)
			break
		}
	}
	return catalog
}

// EnforcementActive implements the existing-user migration policy. An empty
// grace setting means immediate enforcement; registration is always enforced.
func (catalog *PolicyCatalog) EnforcementActive(tenantID string, now time.Time) (bool, error) {
	if _, err := catalog.CurrentPolicies(tenantID); err != nil {
		return false, err
	}
	return catalog.existingUserGraceUntil == nil || !now.Before(*catalog.existingUserGraceUntil), nil
}

func NewPolicyCatalog(policies []types.PolicyDefinition) *PolicyCatalog {
	catalog := &PolicyCatalog{policies: append([]types.PolicyDefinition(nil), policies...)}
	for _, policy := range catalog.policies {
		if err := validatePolicyDefinition(policy); err != nil {
			catalog.err = errors.New(apiError.PolicyConfigurationUnavailable)
			break
		}
	}
	return catalog
}

func (catalog *PolicyCatalog) CurrentPolicies(tenantID string) ([]types.PolicyDefinition, error) {
	if catalog == nil || catalog.err != nil || strings.TrimSpace(tenantID) == "" {
		return nil, errors.New(apiError.PolicyConfigurationUnavailable)
	}
	return append([]types.PolicyDefinition(nil), catalog.policies...), nil
}

func (catalog *PolicyCatalog) ValidateAcceptances(tenantID string, submitted []types.PolicyAcceptanceInput) ([]types.PolicyDefinition, error) {
	current, err := catalog.CurrentPolicies(tenantID)
	if err != nil {
		return nil, err
	}

	byKey := make(map[string]string, len(submitted))
	for _, acceptance := range submitted {
		if acceptance.PolicyKey == "" || acceptance.Version == "" {
			return nil, errors.New(apiError.PolicyAcceptanceRequired)
		}
		if _, duplicate := byKey[acceptance.PolicyKey]; duplicate {
			return nil, errors.New(apiError.InvalidPayload)
		}
		byKey[acceptance.PolicyKey] = acceptance.Version
	}

	for _, policy := range current {
		if !policy.Required {
			continue
		}
		version, ok := byKey[policy.PolicyKey]
		if !ok {
			return nil, errors.New(apiError.PolicyAcceptanceRequired)
		}
		if version != policy.Version {
			return nil, errors.New(apiError.PolicyVersionStale)
		}
		delete(byKey, policy.PolicyKey)
	}
	if len(byKey) != 0 {
		return nil, errors.New(apiError.PolicyVersionStale)
	}
	return current, nil
}

func validatePolicyDefinition(policy types.PolicyDefinition) error {
	if strings.TrimSpace(policy.PolicyKey) == "" || strings.TrimSpace(policy.Version) == "" ||
		strings.TrimSpace(policy.Title) == "" || strings.TrimSpace(policy.EffectiveAt) == "" ||
		(policy.AcceptanceAction != "AGREE" && policy.AcceptanceAction != "ACKNOWLEDGE") {
		return errors.New("invalid policy definition")
	}
	parsed, err := url.Parse(policy.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("policy URL must be an absolute HTTPS URL")
	}
	return nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
