package service

import (
	"context"
	"errors"
	"log"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/orthanc/domain/entity"
)

type dicomModalitySelector interface {
	SelectDICOMModalityByModalityID(ctx context.Context, tenantID, modalityID string) (entity.DICOMModality, error)
}

// authorizeDICOMModality returns the tenant's record of a caller-supplied
// modality, or MissingRecord. Every tenant shares one Orthanc, so each Orthanc
// call and record write must use the ModalityID of the record returned here,
// looked up once, never the caller's input or a later lookup.
func authorizeDICOMModality(ctx context.Context, selector dicomModalitySelector, tenantID, modalityID string) (entity.DICOMModality, error) {
	dicomModality, err := selector.SelectDICOMModalityByModalityID(ctx, tenantID, modalityID)
	if err != nil {
		log.Println(err)
		return entity.DICOMModality{}, errors.New(apiError.MissingRecord)
	}

	if dicomModality.TenantID != tenantID || dicomModality.ModalityID != modalityID {
		log.Printf("[orthanc] DICOM modality record %q does not match tenant %q modality %q", dicomModality.ID, tenantID, modalityID)
		return entity.DICOMModality{}, errors.New(apiError.MissingRecord)
	}

	return dicomModality, nil
}
