package repository

import (
	"context"

	"github.com/afex/hystrix-go/hystrix"

	hystrix_config "api-pacs/configs/hystrix"
	"api-pacs/module/orthanc/domain/repository"
	repositoryTypes "api-pacs/module/orthanc/infrastructure/repository/types"
)

// OrthancCommandRepositoryCircuitBreaker circuit breaker for orthanc command repository
type OrthancCommandRepositoryCircuitBreaker struct {
	repository.OrthancCommandRepositoryInterface
}

var config = hystrix_config.Config{}

// UpsertDICOMModality is the decorator for the orthanc repository to upsert DICOM modality
func (repository *OrthancCommandRepositoryCircuitBreaker) UpsertDICOMModality(ctx context.Context, data repositoryTypes.UpsertDICOMModality) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("upsert_dicom_modality", config.Settings())
	errors := hystrix.Go("upsert_dicom_modality", func() error {
		err := repository.OrthancCommandRepositoryInterface.UpsertDICOMModality(ctx, data)
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
