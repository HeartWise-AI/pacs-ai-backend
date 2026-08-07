package repository

import (
	"context"

	"github.com/afex/hystrix-go/hystrix"

	hystrix_config "api-pacs/configs/hystrix"
	"api-pacs/module/tenant/domain/repository"
	"api-pacs/module/tenant/infrastructure/repository/types"
)

// TenantCommandRepositoryCircuitBreaker circuit breaker for tenant command repository
type TenantCommandRepositoryCircuitBreaker struct {
	repository.TenantCommandRepositoryInterface
}

var config = hystrix_config.Config{}

// DeleteOnboardingQuestionnaireAnswer is the decorator for the tenant command repository to delete onboarding questionnaire answer
func (repository *TenantCommandRepositoryCircuitBreaker) DeleteOnboardingQuestionnaireAnswer(ctx context.Context, ID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("delete_onboarding_questionnaire_answer", config.Settings())
	errors := hystrix.Go("delete_onboarding_questionnaire_answer", func() error {
		err := repository.TenantCommandRepositoryInterface.DeleteOnboardingQuestionnaireAnswer(ctx, ID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// InsertOnboardingQuestionnaireAnswer is the decorator for the tenant command repository to insert onboarding questionnaire answer
func (repository *TenantCommandRepositoryCircuitBreaker) InsertOnboardingQuestionnaireAnswer(ctx context.Context, data types.AddOnboardingQuestionnaireAnswer) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("insert_onboarding_questionnaire_answer", config.Settings())
	errors := hystrix.Go("insert_onboarding_questionnaire_answer", func() error {
		err := repository.TenantCommandRepositoryInterface.InsertOnboardingQuestionnaireAnswer(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// UpdateOnboardingEnableConsent is the decorator for the tenant command repository to update onboarding enable consent
func (repository *TenantCommandRepositoryCircuitBreaker) UpdateOnboardingEnableConsent(ctx context.Context, data types.UpdateOnboardingEnableConsent) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("update_onboarding_enable_consent", config.Settings())
	errors := hystrix.Go("update_onboarding_enable_consent", func() error {
		err := repository.TenantCommandRepositoryInterface.UpdateOnboardingEnableConsent(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// UpdateOnboardingEnableRegistration is the decorator for the tenant command repository to update onboarding enable registration
func (repository *TenantCommandRepositoryCircuitBreaker) UpdateOnboardingEnableRegistration(ctx context.Context, data types.UpdateOnboardingEnableRegistration) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("update_onboarding_enable_registration", config.Settings())
	errors := hystrix.Go("update_onboarding_enable_registration", func() error {
		err := repository.TenantCommandRepositoryInterface.UpdateOnboardingEnableRegistration(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}
