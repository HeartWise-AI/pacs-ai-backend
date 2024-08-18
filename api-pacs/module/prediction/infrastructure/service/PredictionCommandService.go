package service

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"

	inferenceAPITypes "api-pacs/infrastructures/providers/api/inference/types"
	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	dicomUtils "api-pacs/internal/dicoms"
	apiError "api-pacs/internal/errors"
	"api-pacs/internal/predictions"
	"api-pacs/module/prediction/infrastructure/service/types"
)

// PredictionCommandService handles the Prediction command service logic
type PredictionCommandService struct {
	inferenceAPITypes.InferenceAPIInterface
	orthancAPITypes.OrthancAPIInterface
}

// Predict get prediction results from inference
func (service *PredictionCommandService) Predict(ctx context.Context, queryID string) (types.PredictionResult, error) {
	// download DICOM file
	dicomBytes, err := service.OrthancAPIInterface.DownloadDICOM(ctx, queryID)
	if err != nil {
		return types.PredictionResult{}, err
	}

	// parse DICOM
	dataset, err := dicom.Parse(bytes.NewReader(dicomBytes), int64(len(dicomBytes)), nil)
	if err != nil {
		log.Println(err)
		return types.PredictionResult{}, errors.New(apiError.DICOMParseError)
	}

	// get age
	ageElement, _ := dataset.FindElementByTag(tag.PatientBirthDate)
	birthDateStr := ageElement.Value.String()
	age, err := calculateAge(birthDateStr)
	if err != nil {
		return types.PredictionResult{}, errors.New(apiError.DICOMParseError)
	}

	// convert DICOM to instances
	instances, err := dicomUtils.DICOMToInstances(&dataset)
	if err != nil {
		log.Println(err)
		return types.PredictionResult{}, errors.New(apiError.DICOMParseError)
	}

	// detect vessel
	vesselInferenceResult, err := service.InferenceAPIInterface.DetectVessel(ctx, inferenceAPITypes.Instances{
		Instances: instances,
	})
	if err != nil {
		log.Println(err)
		return types.PredictionResult{}, errors.New(apiError.TorchServeError)
	}

	if len(vesselInferenceResult) == 0 {
		log.Println("[prediction] vessel inference result is empty")
		return types.PredictionResult{}, errors.New(apiError.InferenceError)
	}

	vesselIndex := dicomUtils.FindMaxProbabilityIndex(vesselInferenceResult)
	detectedVessel := predictions.VesselTypes[vesselIndex]

	// detect LVEF if vessel is left coronary
	var detectedLVEF float64

	if detectedVessel == predictions.VesselTypes[5] {
		lvefInferenceResult, err := service.InferenceAPIInterface.DetectLVEF(ctx, inferenceAPITypes.Instances{
			Instances: instances,
		})
		if err != nil {
			log.Println(err)
			return types.PredictionResult{}, errors.New(apiError.TorchServeError)
		}

		if len(lvefInferenceResult) == 0 || len(lvefInferenceResult[0]) == 0 {
			log.Println("[prediction] lvef inference result is empty")
			return types.PredictionResult{}, errors.New(apiError.InferenceError)
		}

		detectedLVEF = lvefInferenceResult[0][0]
	}

	return types.PredictionResult{
		Vessel: detectedVessel,
		LVEF:   detectedLVEF,
		Age:    age,
	}, nil
}

func calculateAge(birthDateStr string) (int, error) {
	birthDateStr = strings.Trim(birthDateStr, "[] \t\n\r")
	birthDate, err := time.Parse("20060102", birthDateStr)
	if err != nil {
		log.Println(err)
		return 0, err
	}

	now := time.Now()
	age := now.Year() - birthDate.Year()

	// adjust age if birthday hasn't occurred this year
	if now.YearDay() < birthDate.YearDay() {
		age--
	}

	return age, nil
}
