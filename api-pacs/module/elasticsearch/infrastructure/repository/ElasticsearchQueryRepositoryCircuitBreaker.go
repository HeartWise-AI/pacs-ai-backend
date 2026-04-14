package repository

import (
	"context"

	"github.com/afex/hystrix-go/hystrix"
	"github.com/elastic/go-elasticsearch/v8/typedapi/cat/indices"
	"github.com/elastic/go-elasticsearch/v8/typedapi/core/search"

	"api-pacs/module/elasticsearch/domain/repository"
	repositoryTypes "api-pacs/module/elasticsearch/infrastructure/repository/types"
)

// ElasticsearchQueryRepositoryCircuitBreaker is the circuit breaker for the elasticsearch query repository
type ElasticsearchQueryRepositoryCircuitBreaker struct {
	repository.ElasticsearchQueryRepositoryInterface
}

// GetAllIndices decorator pattern to get all indices from elasticsearch
func (repository *ElasticsearchQueryRepositoryCircuitBreaker) GetAllIndices() (indices.Response, error) {
	output := make(chan indices.Response, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("get_all_indices", config.Settings())
	errors := hystrix.Go("get_all_indices", func() error {
		indices, err := repository.ElasticsearchQueryRepositoryInterface.GetAllIndices()
		if err != nil {
			errChan <- err
			return nil
		}

		output <- indices
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return nil, err
	case err := <-errors:
		return nil, err
	}
}

// SearchAdminInviteLogs decorator pattern to search admin invite logs
func (repository *ElasticsearchQueryRepositoryCircuitBreaker) SearchAdminInviteLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error) {
	output := make(chan *search.Response, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("search_admin_invite_logs", config.Settings())
	errors := hystrix.Go("search_admin_invite_logs", func() error {
		adminInvite, err := repository.ElasticsearchQueryRepositoryInterface.SearchAdminInviteLogs(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- adminInvite
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return nil, err
	case err := <-errors:
		return nil, err
	}
}

// SearchAdminMemberLogs decorator pattern to search admin member logs
func (repository *ElasticsearchQueryRepositoryCircuitBreaker) SearchAdminMemberLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error) {
	output := make(chan *search.Response, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("search_admin_member_logs", config.Settings())
	errors := hystrix.Go("search_admin_member_logs", func() error {
		adminMember, err := repository.ElasticsearchQueryRepositoryInterface.SearchAdminMemberLogs(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- adminMember
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return nil, err
	case err := <-errors:
		return nil, err
	}
}

// SearchLoginLogs decorator pattern to search login logs
func (repository *ElasticsearchQueryRepositoryCircuitBreaker) SearchLoginLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error) {
	output := make(chan *search.Response, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("search_login_logs", config.Settings())
	errors := hystrix.Go("search_login_logs", func() error {
		login, err := repository.ElasticsearchQueryRepositoryInterface.SearchLoginLogs(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- login
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return nil, err
	case err := <-errors:
		return nil, err
	}
}

// SearchModalityStudyLogs decorator pattern to search modality study logs
func (repository *ElasticsearchQueryRepositoryCircuitBreaker) SearchModalityStudyLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error) {
	output := make(chan *search.Response, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("search_modality_study_logs", config.Settings())
	errors := hystrix.Go("search_modality_study_logs", func() error {
		modalityStudy, err := repository.ElasticsearchQueryRepositoryInterface.SearchModalityStudyLogs(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- modalityStudy
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return nil, err
	case err := <-errors:
		return nil, err
	}
}

// SearchPredictInferenceModelLogs decorator pattern to search predict inference model logs
func (repository *ElasticsearchQueryRepositoryCircuitBreaker) SearchPredictInferenceModelLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error) {
	output := make(chan *search.Response, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("search_predict_inference_model_logs", config.Settings())
	errors := hystrix.Go("search_predict_inference_model_logs", func() error {
		predictInferenceModel, err := repository.ElasticsearchQueryRepositoryInterface.SearchPredictInferenceModelLogs(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- predictInferenceModel
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return nil, err
	case err := <-errors:
		return nil, err
	}
}

// SearchRetrievedStudyLogs decorator pattern to search retrieved study logs
func (repository *ElasticsearchQueryRepositoryCircuitBreaker) SearchRetrievedStudyLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error) {
	output := make(chan *search.Response, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("search_retrieved_study_logs", config.Settings())
	errors := hystrix.Go("search_retrieved_study_logs", func() error {
		retrievedStudy, err := repository.ElasticsearchQueryRepositoryInterface.SearchRetrievedStudyLogs(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- retrievedStudy
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return nil, err
	case err := <-errors:
		return nil, err
	}
}

// SearchSignedConsentLogs decorator pattern to search signed consent logs
func (repository *ElasticsearchQueryRepositoryCircuitBreaker) SearchSignedConsentLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error) {
	output := make(chan *search.Response, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("search_signed_consent_logs", config.Settings())
	errors := hystrix.Go("search_signed_consent_logs", func() error {
		signedConsent, err := repository.ElasticsearchQueryRepositoryInterface.SearchSignedConsentLogs(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- signedConsent
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return nil, err
	case err := <-errors:
		return nil, err
	}
}

// SearchStoredCustomSeriesLogs decorator pattern to search stored custom series logs
func (repository *ElasticsearchQueryRepositoryCircuitBreaker) SearchStoredCustomSeriesLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error) {
	output := make(chan *search.Response, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("search_stored_custom_series_logs", config.Settings())
	errors := hystrix.Go("search_stored_custom_series_logs", func() error {
		storedCustomSeries, err := repository.ElasticsearchQueryRepositoryInterface.SearchStoredCustomSeriesLogs(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- storedCustomSeries
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return nil, err
	case err := <-errors:
		return nil, err
	}
}
