package service

import (
	"bytes"
	"context"
	"errors"
	"log"
	"time"

	"github.com/suyashkumar/dicom"

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
	// TODO: remove this
	downloadStartTime := time.Now()

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
	age, err := dicomUtils.ParseAge(dataset)
	if err != nil {
		log.Println(err)
		return types.PredictionResult{}, errors.New(apiError.DICOMParseError)
	}

	// convert DICOM to instances
	instances, err := dicomUtils.DICOMToInstances(dataset)
	if err != nil {
		log.Println(err)
		return types.PredictionResult{}, errors.New(apiError.DICOMParseError)
	}

	// TODO: remove this
	downloadEndTime := time.Since(downloadStartTime)
	log.Printf("[prediction] download DICOM file took %f seconds", downloadEndTime.Seconds())

	// TODO: remove this
	startInferenceTime := time.Now()

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

	// TODO: remove this
	inferenceEndTime := time.Since(startInferenceTime)
	log.Printf("[prediction] inference took %f seconds", inferenceEndTime.Seconds())

	return types.PredictionResult{
		Vessel: detectedVessel,
		LVEF:   detectedLVEF,
		Age:    age,
	}, nil
}
