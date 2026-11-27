package repository

import (
	"context"

	"github.com/afex/hystrix-go/hystrix"

	hystrix_config "api-pacs/configs/hystrix"
	"api-pacs/module/orthanc/domain/entity"
	"api-pacs/module/orthanc/domain/repository"
	repositoryTypes "api-pacs/module/orthanc/infrastructure/repository/types"
)

// OrthancCommandRepositoryCircuitBreaker circuit breaker for orthanc command repository
type OrthancCommandRepositoryCircuitBreaker struct {
	repository.OrthancCommandRepositoryInterface
}

var config = hystrix_config.Config{}

// SelectDICOMModalityByModalityID is the decorator for the orthanc repository to select DICOM modality by modality id
func (repository *OrthancCommandRepositoryCircuitBreaker) SelectDICOMModalityByModalityID(ctx context.Context, tenantID, modalityID string) (entity.DICOMModality, error) {
	output := make(chan entity.DICOMModality, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_dicom_modality_by_modality_id", config.Settings())
	errors := hystrix.Go("select_dicom_modality_by_modality_id", func() error {
		dicomModality, err := repository.OrthancCommandRepositoryInterface.SelectDICOMModalityByModalityID(ctx, tenantID, modalityID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- dicomModality
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return entity.DICOMModality{}, err
	case err := <-errors:
		return entity.DICOMModality{}, err
	}
}

// DeleteDICOMModality is the decorator for the orthanc repository to delete DICOM modality
func (repository *OrthancCommandRepositoryCircuitBreaker) DeleteDICOMModality(ctx context.Context, tenantID, modalityID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("delete_dicom_modality", config.Settings())
	errors := hystrix.Go("delete_dicom_modality", func() error {
		err := repository.OrthancCommandRepositoryInterface.DeleteDICOMModality(ctx, tenantID, modalityID)
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
