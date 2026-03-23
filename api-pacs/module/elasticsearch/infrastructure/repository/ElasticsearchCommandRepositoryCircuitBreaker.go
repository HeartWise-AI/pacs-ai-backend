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

// InsertAdminMemberLog decorator pattern to insert admin member log request
func (repository *ElasticsearchCommandRepositoryCircuitBreaker) InsertAdminMemberLog(ctx context.Context, data repositoryTypes.CreateAdminMemberLog) (*index.Response, error) {
	output := make(chan *index.Response, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("insert_admin_member_log", config.Settings())
	errors := hystrix.Go("insert_admin_member_log", func() error {
		adminMember, err := repository.ElasticsearchCommandRepositoryInterface.InsertAdminMemberLog(ctx, data)
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

// InsertGetModalityStudyLog decorator pattern to insert get modality study log
func (repository *ElasticsearchCommandRepositoryCircuitBreaker) InsertGetModalityStudyLog(ctx context.Context, data repositoryTypes.CreateGetModalityStudyLog) (*index.Response, error) {
	output := make(chan *index.Response, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("insert_get_modality_study_log", config.Settings())
	errors := hystrix.Go("insert_get_modality_study_log", func() error {
		modalityStudy, err := repository.ElasticsearchCommandRepositoryInterface.InsertGetModalityStudyLog(ctx, data)
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

// InsertLoginLog decorator pattern to insert login log request
func (repository *ElasticsearchCommandRepositoryCircuitBreaker) InsertLoginLog(ctx context.Context, data repositoryTypes.CreateLoginLog) (*index.Response, error) {
	output := make(chan *index.Response, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("insert_login_log", config.Settings())
	errors := hystrix.Go("insert_login_log", func() error {
		login, err := repository.ElasticsearchCommandRepositoryInterface.InsertLoginLog(ctx, data)
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

// InsertPredictInferenceModelLog decorator pattern to insert predict inference model log
func (repository *ElasticsearchCommandRepositoryCircuitBreaker) InsertPredictInferenceModelLog(ctx context.Context, data repositoryTypes.CreatePredictInferenceModelLog) (*index.Response, error) {
	output := make(chan *index.Response, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("insert_predict_inference_model_log", config.Settings())
	errors := hystrix.Go("insert_predict_inference_model_log", func() error {
		predictInferenceModel, err := repository.ElasticsearchCommandRepositoryInterface.InsertPredictInferenceModelLog(ctx, data)
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

// InsertRetrieveStudyLog decorator pattern to insert retrieved study log
func (repository *ElasticsearchCommandRepositoryCircuitBreaker) InsertRetrieveStudyLog(ctx context.Context, data repositoryTypes.CreateRetrieveStudyLog) (*index.Response, error) {
	output := make(chan *index.Response, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("insert_retrieved_study_log", config.Settings())
	errors := hystrix.Go("insert_retrieved_study_log", func() error {
		study, err := repository.ElasticsearchCommandRepositoryInterface.InsertRetrieveStudyLog(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- study
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

// InsertStoredCustomSeriesLog decorator pattern to insert stored custom series log
func (repository *ElasticsearchCommandRepositoryCircuitBreaker) InsertStoredCustomSeriesLog(ctx context.Context, data repositoryTypes.CreateStoredCustomSeriesLog) (*index.Response, error) {
	output := make(chan *index.Response, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("insert_stored_custom_series_log", config.Settings())
	errors := hystrix.Go("insert_stored_custom_series_log", func() error {
		storedCustomSeries, err := repository.ElasticsearchCommandRepositoryInterface.InsertStoredCustomSeriesLog(ctx, data)
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
