package repository

import (
	hystrix_config "api-pacs/configs/hystrix"
	"api-pacs/module/elasticsearch/domain/repository"
	repositoryTypes "api-pacs/module/elasticsearch/infrastructure/repository/types"
	"context"

	"github.com/afex/hystrix-go/hystrix"
	"github.com/elastic/go-elasticsearch/v8/typedapi/core/index"
)

// ElasticsearchCommandRepositoryCircuitBreaker circuit breaker for elasticsearch command repository
type ElasticsearchCommandRepositoryCircuitBreaker struct {
	repository.ElasticsearchCommandRepositoryInterface
}

var config = hystrix_config.Config{}

// InsertLoginLog decorator pattern to insert login log request
func (repository *ElasticsearchCommandRepositoryCircuitBreaker) InsertLoginLog(ctx context.Context, data repositoryTypes.CreateLoginLog) (*index.Response, error) {
	output := make(chan *index.Response, 1)
	hystrix.ConfigureCommand("insert_login_log_request", config.Settings())
	errors := hystrix.Go("insert_login_log_request", func() error {
		login, err := repository.ElasticsearchCommandRepositoryInterface.InsertLoginLog(ctx, data)
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

// InsertAdminMemberLog decorator pattern to insert admin member log request
func (repository *ElasticsearchCommandRepositoryCircuitBreaker) InsertAdminMemberLog(ctx context.Context, data repositoryTypes.CreateAdminMemberLog) (*index.Response, error) {
	output := make(chan *index.Response, 1)
	hystrix.ConfigureCommand("insert_admin_member_log_request", config.Settings())
	errors := hystrix.Go("insert_admin_member_log_request", func() error {
		adminMember, err := repository.ElasticsearchCommandRepositoryInterface.InsertAdminMemberLog(ctx, data)
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
