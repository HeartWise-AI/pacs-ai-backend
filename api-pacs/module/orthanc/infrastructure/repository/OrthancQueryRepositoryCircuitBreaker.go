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

// SelectDICOMModalityByTenantModality is the decorator for the orthanc repository to select DICOM modality by tenant and modality
func (repository *OrthancQueryRepositoryCircuitBreaker) SelectDICOMModalityByTenantModality(ctx context.Context, tenantID, modalityID string) (entity.DICOMModality, error) {
	output := make(chan entity.DICOMModality, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_dicom_modality_by_tenant_modality", config.Settings())
	errors := hystrix.Go("select_dicom_modality_by_tenant_modality", func() error {
		dicomModality, err := repository.OrthancQueryRepositoryInterface.SelectDICOMModalityByTenantModality(ctx, tenantID, modalityID)
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
