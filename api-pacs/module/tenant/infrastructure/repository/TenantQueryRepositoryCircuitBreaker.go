package repository

import (
	"context"

	"github.com/afex/hystrix-go/hystrix"

	"api-pacs/module/tenant/domain/entity"
	"api-pacs/module/tenant/domain/repository"
	repositoryTypes "api-pacs/module/tenant/infrastructure/repository/types"
)

// TenantQueryRepositoryCircuitBreaker is the circuit breaker for the tenant query repository
type TenantQueryRepositoryCircuitBreaker struct {
	repository.TenantQueryRepositoryInterface
}

// SelectOnboardingQuestionnaireAnswers select onboarding questionnaire answers
func (repository *TenantQueryRepositoryCircuitBreaker) SelectOnboardingQuestionnaireAnswers(ctx context.Context, data repositoryTypes.GetOnboardingQuestionnaireAnswer) ([]entity.OnboardingQuestionnaireAnswer, error) {
	output := make(chan []entity.OnboardingQuestionnaireAnswer, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_onboarding_questionnaire_answers", config.Settings())
	errors := hystrix.Go("select_onboarding_questionnaire_answers", func() error {
		answers, err := repository.TenantQueryRepositoryInterface.SelectOnboardingQuestionnaireAnswers(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- answers
		return nil
	}, nil)

	select {
	case answers := <-output:
		return answers, nil
	case err := <-errChan:
		return nil, err
	case err := <-errors:
		return nil, err
	}
}

// SelectTenantByID is a decorator for the get tenant by id
func (repository *TenantQueryRepositoryCircuitBreaker) SelectTenantByID(ctx context.Context, tenantID string) (repositoryTypes.GetTenant, error) {
	output := make(chan repositoryTypes.GetTenant, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_tenant_by_id", config.Settings())
	errors := hystrix.Go("select_tenant_by_id", func() error {
		tenant, err := repository.TenantQueryRepositoryInterface.SelectTenantByID(ctx, tenantID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- tenant
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return repositoryTypes.GetTenant{}, err
	case err := <-errors:
		return repositoryTypes.GetTenant{}, err
	}
}
