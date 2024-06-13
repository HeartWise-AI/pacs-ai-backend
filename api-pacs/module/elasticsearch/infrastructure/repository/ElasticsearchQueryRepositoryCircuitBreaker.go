package repository

import (
	"context"

	"github.com/afex/hystrix-go/hystrix"
	"github.com/elastic/go-elasticsearch/v8/typedapi/core/search"

	"api-pacs/module/elasticsearch/domain/repository"
	repositoryTypes "api-pacs/module/elasticsearch/infrastructure/repository/types"
)

// ElasticsearchQueryRepositoryCircuitBreaker is the circuit breaker for the elasticsearch query repository
type ElasticsearchQueryRepositoryCircuitBreaker struct {
	repository.ElasticsearchQueryRepositoryInterface
}

// SearchAdminMemberLogs decorator pattern to search admin member logs
func (repository *ElasticsearchQueryRepositoryCircuitBreaker) SearchAdminMemberLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error) {
	output := make(chan *search.Response, 1)
	hystrix.ConfigureCommand("search_admin_member_logs", config.Settings())
	errors := hystrix.Go("search_admin_member_logs", func() error {
		adminMember, err := repository.ElasticsearchQueryRepositoryInterface.SearchAdminMemberLogs(ctx, data)
		if err != nil {
			return err
		}

		output <- adminMember
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errors:
		return nil, err
	}
}

// SearchLoginLogs decorator pattern to search login logs
func (repository *ElasticsearchQueryRepositoryCircuitBreaker) SearchLoginLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error) {
	output := make(chan *search.Response, 1)
	hystrix.ConfigureCommand("search_login_logs", config.Settings())
	errors := hystrix.Go("search_login_logs", func() error {
		login, err := repository.ElasticsearchQueryRepositoryInterface.SearchLoginLogs(ctx, data)
		if err != nil {
			return err
		}

		output <- login
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errors:
		return nil, err
	}
}

// SearchModalityStudyLogs decorator pattern to search modality study logs
func (repository *ElasticsearchQueryRepositoryCircuitBreaker) SearchModalityStudyLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error) {
	output := make(chan *search.Response, 1)
	hystrix.ConfigureCommand("search_modality_study_logs", config.Settings())
	errors := hystrix.Go("search_modality_study_logs", func() error {
		modalityStudy, err := repository.ElasticsearchQueryRepositoryInterface.SearchModalityStudyLogs(ctx, data)
		if err != nil {
			return err
		}

		output <- modalityStudy
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errors:
		return nil, err
	}
}

// SearchRetrievedStudyLogs decorator pattern to search retrieved study logs
func (repository *ElasticsearchQueryRepositoryCircuitBreaker) SearchRetrievedStudyLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error) {
	output := make(chan *search.Response, 1)
	hystrix.ConfigureCommand("search_retrieved_study_logs", config.Settings())
	errors := hystrix.Go("search_retrieved_study_logs", func() error {
		retrievedStudy, err := repository.ElasticsearchQueryRepositoryInterface.SearchRetrievedStudyLogs(ctx, data)
		if err != nil {
			return err
		}

		output <- retrievedStudy
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errors:
		return nil, err
	}
}
