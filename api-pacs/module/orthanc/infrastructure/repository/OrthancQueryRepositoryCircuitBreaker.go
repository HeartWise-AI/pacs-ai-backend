package repository

import (
	"context"

	"github.com/afex/hystrix-go/hystrix"

	"api-pacs/module/orthanc/domain/entity"
	"api-pacs/module/orthanc/domain/repository"
)

// OrthancQueryRepositoryCircuitBreaker is the circuit breaker for the orthanc query repository
type OrthancQueryRepositoryCircuitBreaker struct {
	repository.OrthancQueryRepositoryInterface
}

// SelectDICOMModalityByModalityID is the decorator for the orthanc repository to select DICOM modality by modality id
func (repository *OrthancQueryRepositoryCircuitBreaker) SelectDICOMModalityByModalityID(ctx context.Context, tenantID, modalityID string) (entity.DICOMModality, error) {
	output := make(chan entity.DICOMModality, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_dicom_modality_by_modality_id", config.Settings())
	errors := hystrix.Go("select_dicom_modality_by_modality_id", func() error {
		dicomModality, err := repository.OrthancQueryRepositoryInterface.SelectDICOMModalityByModalityID(ctx, tenantID, modalityID)
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

// SelectLinkedDICOMModalityWithEnabledCStore is the decorator for the orthanc repository to select DICOM modality by enabled C-Store
func (repository *OrthancQueryRepositoryCircuitBreaker) SelectLinkedDICOMModalityWithEnabledCStore(ctx context.Context, tenantID, hostHash string) (entity.DICOMModality, error) {
	output := make(chan entity.DICOMModality, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_dicom_modality_by_enabled_c_store", config.Settings())
	errors := hystrix.Go("select_dicom_modality_by_enabled_c_store", func() error {
		dicomModality, err := repository.OrthancQueryRepositoryInterface.SelectLinkedDICOMModalityWithEnabledCStore(ctx, tenantID, hostHash)
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
