package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"slices"
	"sync"

	"github.com/segmentio/ksuid"
	"golang.org/x/sync/errgroup"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	dicomUtils "api-pacs/internal/dicom"
	apiError "api-pacs/internal/errors"
	elasticsearchApplication "api-pacs/module/elasticsearch/application"
	elasticsearchTypes "api-pacs/module/elasticsearch/infrastructure/service/types"
	"api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	"api-pacs/module/inference/infrastructure/service/types"
	tenantApplication "api-pacs/module/tenant/application"
	userApplication "api-pacs/module/user/application"
)

// InferenceCommandService handles the Inference command service logic
type InferenceCommandService struct {
	repository.InferenceCommandRepositoryInterface
	repository.InferenceQueryRepositoryInterface
	tenantApplication.TenantQueryServiceInterface
	userApplication.UserQueryServiceInterface
	elasticsearchApplication.ElasticsearchCommandServiceInterface
	dockerTypes.DockerSDKInterface
	orthancAPITypes.OrthancAPIInterface
	dockerInferenceTypes.DockerInferenceAPIInterface
}

// AddInferenceModel adds an inference model
func (service *InferenceCommandService) AddInferenceModel(ctx context.Context, data types.AddInferenceModel) error {
	// pull docker image
	err := service.DockerSDKInterface.PullImage(ctx, data.DockerImage)
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	// create container
	containerID, err := service.DockerSDKInterface.CreateContainer(ctx, dockerTypes.CreateContainer{
		Name:  data.Name,
		Image: data.DockerImage,
		Envs:  data.Envs,
	})
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	// start container
	err = service.StartInferenceModelContainer(ctx, containerID)
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	// TODO: check and compare output mode if supported

	// generate ID
	ID := generateID()

	err = service.InferenceCommandRepositoryInterface.InsertInferenceModel(ctx, repositoryTypes.AddInferenceModel{
		ID:                  ID,
		TenantID:            data.TenantID,
		ContainerID:         containerID,
		Name:                data.Name,
		DockerImage:         data.DockerImage,
		Envs:                data.Envs,
		DisallowedDICOMTags: []string{}, // initialize empty list
		OutputMode:          data.OutputMode,
	})
	if err != nil {
		return err
	}

	return nil
}

// DeleteInferenceModel deletes an inference model
func (service *InferenceCommandService) DeleteInferenceModel(ctx context.Context, ID string) error {
	// get inference model
	inferenceModel, err := service.InferenceQueryRepositoryInterface.SelectInferenceModelByID(ctx, ID)
	if err != nil {
		return err
	}

	// force remove container
	err = service.DockerSDKInterface.RemoveContainer(ctx, inferenceModel.ContainerID)
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	// delete inference model
	err = service.InferenceCommandRepositoryInterface.DeleteInferenceModel(ctx, ID)
	if err != nil {
		return err
	}

	return nil
}

// instanceInfo holds common data about a DICOM instance
type instanceInfo struct {
	seriesNumber      int
	sopInstanceUID    string
	sopInstanceNumber int
	isRTSTRUCT        bool
}

// extractInstanceInfo extracts common instance information
func extractInstanceInfo(instance map[string]interface{}) (instanceInfo, error) {
	info := instanceInfo{}

	// Get series number
	if seriesNumVal, ok := instance["00200011"].(map[string]interface{})["Value"].([]interface{}); ok && len(seriesNumVal) > 0 {
		info.seriesNumber = int(seriesNumVal[0].(float64))
	} else {
		return info, errors.New("missing series number")
	}

	// Get SOP Instance UID
	if sopVal, ok := instance["00080018"].(map[string]interface{})["Value"].([]interface{}); ok && len(sopVal) > 0 {
		info.sopInstanceUID = sopVal[0].(string)
	} else {
		return info, errors.New("missing SOP instance UID")
	}

	// Check if RTSTRUCT
	if sopClassVal, ok := instance["00080016"].(map[string]interface{})["Value"].([]interface{}); ok && len(sopClassVal) > 0 {
		info.isRTSTRUCT = sopClassVal[0].(string) == "1.2.840.10008.5.1.4.1.1.481.3"
	}

	// Get instance number
	if info.isRTSTRUCT {
		info.sopInstanceNumber = info.seriesNumber // in RTSTRUCT, sopInstanceUID = seriesInstanceUID
	} else {
		// if a study got series but no instance, error out
		if _, ok := instance["00200013"]; !ok {
			return info, errors.New("missing instance number")
		}
		info.sopInstanceNumber = int(instance["00200013"].(map[string]interface{})["Value"].([]interface{})[0].(float64))
	}

	return info, nil
}

// processSeriesInstances processes all instances in a series with a given handler function
func (service *InferenceCommandService) processSeriesInstances(
	ctx context.Context,
	studyInstanceUID string,
	seriesInstanceUIDs []string,
	processInstanceFn func(context.Context, string, string, map[string]interface{}) error,
) error {
	var mutex = sync.Mutex{}
	eg, egCtx := errgroup.WithContext(ctx)

	// Set parallel execution limit
	eg.SetLimit(len(seriesInstanceUIDs))

	for _, seriesInstanceUID := range seriesInstanceUIDs {
		seriesUID := seriesInstanceUID // Create local copy for goroutine
		eg.Go(func() error {
			mutex.Lock()
			defer mutex.Unlock()

			// Get instances
			instances, err := service.OrthancAPIInterface.GetDICOMWebSeriesInstances(egCtx, studyInstanceUID, seriesUID)
			if err != nil {
				return err
			}

			// Check if this is an ultrasound (US) modality
			isUltrasound := false
			if len(instances) > 0 {
				// Get modality from first instance (tag 0008,0060)
				if modalityTag, ok := instances[0]["00080060"].(map[string]interface{}); ok {
					if modalityValues, ok := modalityTag["Value"].([]interface{}); ok && len(modalityValues) > 0 {
						modality, ok := modalityValues[0].(string)
						if ok && modality == "US" {
							isUltrasound = true
							log.Println("[dicom-web] Detected ultrasound (US) modality, using parallel instance processing")
						}
					}
				}
			}

			if isUltrasound {
				// Special handling for US modality - process all instances in parallel
				var instanceMutex = sync.Mutex{}
				egInstance, egInstanceCtx := errgroup.WithContext(egCtx)

				// Set parallel execution limit for instances
				egInstance.SetLimit(len(instances))

				for _, instance := range instances {
					instanceData := instance // Create local copy for goroutine
					egInstance.Go(func() error {
						// Process each instance concurrently
						// Lock only when writing to shared data structures
						err := processInstanceFn(egInstanceCtx, studyInstanceUID, seriesUID, instanceData)
						if err != nil {
							// Use mutex when logging errors to avoid interleaved log output
							instanceMutex.Lock()
							log.Printf("[ultrasound] Error processing instance: %v", err)
							instanceMutex.Unlock()
							return err
						}
						return nil
					})
				}

				// Wait for all instance processing to finish
				return egInstance.Wait()
			} else {
				// Original sequential instance processing for non-US modalities
				var instanceMutex = sync.Mutex{}
				egInstance, egInstanceCtx := errgroup.WithContext(egCtx)

				// Set parallel execution limit for instances
				egInstance.SetLimit(len(instances))

				for _, instance := range instances {
					instanceData := instance // Create local copy for goroutine
					egInstance.Go(func() error {
						instanceMutex.Lock()
						defer instanceMutex.Unlock()

						return processInstanceFn(egInstanceCtx, studyInstanceUID, seriesUID, instanceData)
					})
				}

				// Wait for all instance processing to finish
				return egInstance.Wait()
			}
		})
	}

	// Wait for all series processing to finish
	return eg.Wait()
}

// PredictInferenceModel predicts an inference model
func (service *InferenceCommandService) PredictInferenceModel(ctx context.Context, tenantID, userID, containerID string, data types.PredictInferenceModel) (dockerInferenceTypes.PredictResponse, error) {
	// get inference model
	inferenceModel, err := service.InferenceQueryRepositoryInterface.SelectInferenceModelByContainer(ctx, tenantID, containerID)
	if err != nil {
		return dockerInferenceTypes.PredictResponse{}, err
	}

	// get container model info
	containerInfo, err := service.DockerSDKInterface.GetContainerInfo(ctx, containerID)
	if err != nil {
		return dockerInferenceTypes.PredictResponse{}, err
	}

	containerName := containerInfo.Name[1:] // remove "/" prefix

	modelInfo, err := service.DockerInferenceAPIInterface.GetModelInfo(ctx, containerName) // remove "/" prefix
	if err != nil {
		return dockerInferenceTypes.PredictResponse{}, errors.New(apiError.DockerInferenceError)
	}

	seriesInstanceImages := map[int]map[int]string{}
	seriesInstanceMetadata := map[int]map[int]interface{}{}

	// check payload type
	// if supportedDicomTags = ["*"]
	if len(modelInfo.Data.SupportedDicomTags) == 1 && modelInfo.Data.SupportedDicomTags[0] == "*" {
		/// Process DICOM images
		err = service.processSeriesInstances(ctx, data.StudyInstanceUID, data.SeriesInstanceUIDs,
			func(ctx context.Context, studyUID, seriesUID string, instance map[string]interface{}) error {
				info, err := extractInstanceInfo(instance)
				if err != nil {
					if err.Error() == "missing instance number" {
						log.Println("[dicom-web] skipping because instance number is missing")
						return nil // Skip this instance but continue processing
					}
					return err
				}

				// Initialize map for series if needed
				if _, ok := seriesInstanceImages[info.seriesNumber]; !ok {
					seriesInstanceImages[info.seriesNumber] = make(map[int]string)
				}

				// Get instance file
				instanceFile, err := service.OrthancAPIInterface.RetrieveDICOMWebInstanceFile(
					ctx, studyUID, seriesUID, info.sopInstanceUID)
				if err != nil {
					return err
				}

				// Store encoded file
				seriesInstanceImages[info.seriesNumber][info.sopInstanceNumber] = base64.StdEncoding.EncodeToString(instanceFile)

				return nil
			})

		if err != nil {
			return dockerInferenceTypes.PredictResponse{}, err
		}

		// Check if we got any images
		if len(seriesInstanceImages) == 0 {
			log.Println("[predict] empty series instance images")
			return dockerInferenceTypes.PredictResponse{}, errors.New(apiError.InferenceError)
		}
	} else {
		/// Process DICOM metadata
		// Get allowed DICOM tags
		allowedDICOMTags := getFilteredDICOMTags(modelInfo.Data.SupportedDicomTags, inferenceModel.DisallowedDICOMTags)

		err = service.processSeriesInstances(ctx, data.StudyInstanceUID, data.SeriesInstanceUIDs,
			func(ctx context.Context, studyUID, seriesUID string, instance map[string]interface{}) error {
				info, err := extractInstanceInfo(instance)
				if err != nil {
					if err.Error() == "missing instance number" {
						log.Println("[dicom-web] skipping because instance number is missing")
						return nil // Skip this instance but continue processing
					}
					return err
				}

				// Initialize map for series if needed
				if _, ok := seriesInstanceMetadata[info.seriesNumber]; !ok {
					seriesInstanceMetadata[info.seriesNumber] = make(map[int]interface{})
				}

				// Add necessary tags based on instance type
				var reservedTags []string
				if info.isRTSTRUCT {
					// Assign reserved tags required for RTSTRUCT
					reservedTags = []string{"30060020", "30060039", "30060080", "300E0002"}
				} else {
					// Assign reserved tags
					reservedTags = []string{"7FE00010"}
				}

				// Add reserved tags to allowed tags
				for _, tag := range reservedTags {
					if !slices.Contains(allowedDICOMTags, tag) {
						allowedDICOMTags = append(allowedDICOMTags, tag)
					}
				}

				// Get instance metadata
				instanceMetadata, err := service.OrthancAPIInterface.RetrieveDICOMWebInstanceMetadata(
					ctx, studyUID, seriesUID, info.sopInstanceUID)
				if err != nil {
					return err
				}

				// Filter metadata and process BulkDataURI
				allowedInstanceMetadata := filterAndProcessMetadata(instanceMetadata, allowedDICOMTags)

				// Store processed metadata
				seriesInstanceMetadata[info.seriesNumber][info.sopInstanceNumber] = allowedInstanceMetadata
				return nil
			})

		if err != nil {
			return dockerInferenceTypes.PredictResponse{}, err
		}

		// Check if we got any metadata
		if len(seriesInstanceMetadata) == 0 {
			log.Println("[predict] empty series instance metadata")
			return dockerInferenceTypes.PredictResponse{}, errors.New(apiError.InferenceError)
		}
	}

	// Create the predict request
	predictRequest := dockerInferenceTypes.PredictRequest{
		SeriesInstanceImages:   seriesInstanceImages,
		SeriesInstanceMetadata: seriesInstanceMetadata,
		AdditionalMetadata:     data.AdditionalMetadata,
		OutputMode:             dockerInferenceTypes.OutputMode(inferenceModel.OutputMode),
	}

	// Calculate and log the payload size
	payloadBytes, err := json.Marshal(predictRequest)
	if err != nil {
		log.Printf("[prediction] error marshaling predict request: %v", err)
	} else {
		payloadSizeMB := float64(len(payloadBytes)) / (1024 * 1024)
		log.Printf("[prediction] payload size: %.2f MB (%d bytes)", payloadSizeMB, len(payloadBytes))
	}

	predictionResult, err := service.DockerInferenceAPIInterface.Predict(ctx, containerName, predictRequest)
	if err != nil {
		return dockerInferenceTypes.PredictResponse{}, err
	}

	// TODO: remove this
	predictionEndTime := time.Since(predictionStartTime)
	log.Printf("[prediction] predict call took %f seconds", predictionEndTime.Seconds())

	// log to elasticsearch
	go func() {
		user, err := service.UserQueryServiceInterface.GetTenantUserByID(ctx, tenantID, userID)
		if err != nil {
			return
		}

		tenant, err := service.TenantQueryServiceInterface.GetTenantByID(ctx, tenantID)
		if err != nil {
			return
		}

		_, err = service.ElasticsearchCommandServiceInterface.CreatePredictInferenceModelLog(ctx, elasticsearchTypes.CreatePredictInferenceModelLog{
			TenantID:           tenant.ID,
			TenantName:         tenant.Name,
			UserID:             user.ID,
			Email:              user.Email,
			Name:               user.Name,
			ContainerID:        containerID,
			ContainerName:      containerName,
			InferenceModelID:   inferenceModel.ID,
			InferenceModelName: inferenceModel.Name,
			DockerImage:        inferenceModel.DockerImage,
			StudyInstanceUID:   data.StudyInstanceUID,
			SeriesInstanceUIDs: data.SeriesInstanceUIDs,
			AdditionalMetadata: data.AdditionalMetadata,
		})
		if err != nil {
			log.Println(err)
			return
		}
	}()

	return predictionResult, nil
}

// getFilteredDICOMTags filters out disallowed DICOM tags from supported tags
func getFilteredDICOMTags(supportedTags, disallowedTags []string) []string {
	var allowedTags []string

	for _, supportedTag := range supportedTags {
		isAllowed := true
		for _, disallowedTag := range disallowedTags {
			if supportedTag == disallowedTag {
				isAllowed = false
				break
			}
		}

		if isAllowed {
			allowedTags = append(allowedTags, supportedTag)
		}
	}

	return allowedTags
}

// filterAndProcessMetadata filters metadata by allowed tags and converts BulkDataURI to InlineBinary
func filterAndProcessMetadata(instanceMetadata []map[string]interface{}, allowedDICOMTags []string) map[string]interface{} {
	allowedInstanceMetadata := map[string]interface{}{}

	for _, instanceMetadataMap := range instanceMetadata {
		for _, allowedDICOMTag := range allowedDICOMTags {
			if _, ok := instanceMetadataMap[allowedDICOMTag]; ok {
				allowedInstanceMetadata[allowedDICOMTag] = instanceMetadataMap[allowedDICOMTag]
			}
		}
	}

	// Iterate all tags and convert BulkDataURI to InlineBinary
	for key := range allowedInstanceMetadata {
		if metadata, ok := allowedInstanceMetadata[key].(map[string]interface{}); ok {
			if bulkDataURI, hasBulkData := metadata["BulkDataURI"].(string); hasBulkData {
				// Convert bulk data URI to inline binary
				inlineBinary, err := dicomUtils.ConvertBulkDataURIToInlineBinary(bulkDataURI)
				if err != nil {
					// Just log the error and continue, as we don't want to fail the whole processing
					log.Printf("Error converting BulkDataURI to InlineBinary: %v", err)
					continue
				}

				metadata["InlineBinary"] = inlineBinary
				delete(metadata, "BulkDataURI")
			}
		}
	}

	return allowedInstanceMetadata
}

// RestartInferenceModelContainer restarts an inference model container
func (service *InferenceCommandService) RestartInferenceModelContainer(ctx context.Context, containerID string) error {
	err := service.DockerSDKInterface.RestartContainer(ctx, containerID)
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	return nil
}

// StartInferenceModelContainer starts an inference model container
func (service *InferenceCommandService) StartInferenceModelContainer(ctx context.Context, containerID string) error {
	err := service.DockerSDKInterface.StartContainer(ctx, containerID)
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	return nil
}

// StopInferenceModelContainer stops an inference model container
func (service *InferenceCommandService) StopInferenceModelContainer(ctx context.Context, containerID string) error {
	err := service.DockerSDKInterface.StopContainer(ctx, containerID)
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	return nil
}

// UpdateInferenceModel updates an inference model
func (service *InferenceCommandService) UpdateInferenceModel(ctx context.Context, data types.UpdateInferenceModel) error {
	// TODO: check and compare output mode if supported

	err := service.InferenceCommandRepositoryInterface.UpdateInferenceModel(ctx, repositoryTypes.UpdateInferenceModel{
		ID:                  data.ID,
		DisallowedDICOMTags: data.DisallowedDICOMTags,
		OutputMode:          data.OutputMode,
	})
	if err != nil {
		return err
	}

	return nil
}

// TODO: check if needed
// UpdateInferenceModelContainerID updates the container ID of an inference model
func (service *InferenceCommandService) UpdateInferenceModelContainerID(ctx context.Context, ID, containerID string) error {
	err := service.InferenceCommandRepositoryInterface.UpdateInferenceModelContainerID(ctx, ID, containerID)
	if err != nil {
		return err
	}

	return nil
}

func generateID() string {
	return ksuid.New().String()
}
