package repository

import (
	"api-pacs/module/elasticsearch/domain/repository"
	"context"

	"github.com/afex/hystrix-go/hystrix"
	"github.com/elastic/go-elasticsearch/v8/typedapi/core/search"
	searchTypes "github.com/elastic/go-elasticsearch/v8/typedapi/types"
)

// ElasticsearchQueryRepositoryCircuitBreaker is the circuit breaker for the elasticsearch query repository
type ElasticsearchQueryRepositoryCircuitBreaker struct {
	repository.ElasticsearchQueryRepositoryInterface
}

// SearchLoginLogs decorator pattern to search login logs
func (repository *ElasticsearchQueryRepositoryCircuitBreaker) SearchLoginLogs(ctx context.Context, query map[string]searchTypes.MatchQuery) (*search.Response, error) {
	output := make(chan *search.Response, 1)
	hystrix.ConfigureCommand("search_login_logs_request", config.Settings())
	errors := hystrix.Go("search_login_logs_request", func() error {
		login, err := repository.ElasticsearchQueryRepositoryInterface.SearchLoginLogs(ctx, query)
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

// SearchAdminMemberLogs decorator pattern to search admin member logs
func (repository *ElasticsearchQueryRepositoryCircuitBreaker) SearchAdminMemberLogs(ctx context.Context, query map[string]searchTypes.MatchQuery) (*search.Response, error) {
	output := make(chan *search.Response, 1)
	hystrix.ConfigureCommand("search_admin_member_logs_request", config.Settings())
	errors := hystrix.Go("search_admin_member_logs_request", func() error {
		adminMember, err := repository.ElasticsearchQueryRepositoryInterface.SearchLoginLogs(ctx, query)
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
