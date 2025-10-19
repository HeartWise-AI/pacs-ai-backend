package service

import (
	"context"
	"encoding/base64"
	"errors"
	"log"
	"slices"
	"sync"
	"time"

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

func (service *InferenceCommandService) GenerateInferenceModelPredictRequest(ctx context.Context, tenantID, containerID string, data types.PredictInferenceModel) (dockerInferenceTypes.PredictRequest, string, error) {
	// get inference model
	inferenceModel, err := service.InferenceQueryRepositoryInterface.SelectInferenceModelByContainer(ctx, tenantID, containerID)
	if err != nil {
		return dockerInferenceTypes.PredictRequest{}, "", err
	}

	// get container model info
	containerInfo, err := service.DockerSDKInterface.GetContainerInfo(ctx, containerID)
	if err != nil {
		return dockerInferenceTypes.PredictRequest{}, "", err
	}

	containerName := containerInfo.Name[1:] // remove "/" prefix

	modelInfo, err := service.DockerInferenceAPIInterface.GetModelInfo(ctx, containerName) // remove "/" prefix
	if err != nil {
		return dockerInferenceTypes.PredictRequest{}, "", errors.New(apiError.DockerInferenceError)
	}

	seriesInstanceImages := map[int]map[int]string{}
	seriesInstanceMetadata := map[int]map[int]interface{}{}

	// check payload type
	// if supportedDicomTags = ["*"]
	if len(modelInfo.Data.SupportedDicomTags) == 1 && modelInfo.Data.SupportedDicomTags[0] == "*" {
		/// ---------------------- for DICOM images
		// purposely re-implementing the series instances loop because of potential refactors and differences vs metadata

		// get series instances
		var m = sync.Mutex{}
		eg, egCtx := errgroup.WithContext(ctx)

		// set limit
		eg.SetLimit(len(data.SeriesInstanceUIDs))

		for _, seriesInstanceUID := range data.SeriesInstanceUIDs {
			func(seriesInstanceUID string) {
				eg.Go(func() error {
					m.Lock()
					defer m.Unlock()

					// get instances
					instances, err := service.OrthancAPIInterface.GetDICOMWebSeriesInstances(egCtx, data.StudyInstanceUID, seriesInstanceUID)
					if err != nil {
						return err
					}

					var mInstance = sync.Mutex{}
					egInstance, egInstanceCtx := errgroup.WithContext(egCtx)

					// set limit
					egInstance.SetLimit(len(instances))

					for _, instance := range instances {
						func(instance map[string]interface{}) {
							egInstance.Go(func() error {
								mInstance.Lock()
								defer mInstance.Unlock()

								seriesNumber := int(instance["00200011"].(map[string]interface{})["Value"].([]interface{})[0].(float64))
								sopInstanceUID := instance["00080018"].(map[string]interface{})["Value"].([]interface{})[0].(string)

								// assign by series number if doesnt exist yet
								if _, ok := seriesInstanceImages[seriesNumber]; !ok {
									seriesInstanceImages[seriesNumber] = make(map[int]string)
								}

								var sopInstanceNumber int

								// check for RTSTRUCT
								if instance["00080016"].(map[string]interface{})["Value"].([]interface{})[0].(string) == "1.2.840.10008.5.1.4.1.1.481.3" {
									sopInstanceNumber = seriesNumber // in RTSTRUCT, sopInstanceUID = seriesInstanceUID
								} else {
									// if a study got series but no instance, skip it
									if _, ok := instance["00200013"]; !ok {
										log.Println("[dicom-web] skipping because 00200013 is missing")
										return nil
									}

									sopInstanceNumber = int(instance["00200013"].(map[string]interface{})["Value"].([]interface{})[0].(float64))
								}

								// get instance file
								instanceFile, err := service.OrthancAPIInterface.RetrieveDICOMWebInstanceFile(egInstanceCtx, data.StudyInstanceUID, seriesInstanceUID, sopInstanceUID)
								if err != nil {
									return err
								}

								seriesInstanceImages[seriesNumber][sopInstanceNumber] = base64.StdEncoding.EncodeToString(instanceFile) // convert to base64

								return nil
							})
						}(instance)
					}

					// wait for all goroutines to finish
					if err := egInstance.Wait(); err != nil {
						return err
					}

					return nil
				})
			}(seriesInstanceUID)
		}

		// wait for all goroutines to finish
		if err := eg.Wait(); err != nil {
			return dockerInferenceTypes.PredictRequest{}, "", err
		}

		// check if SeriesInstanceImages is empty
		if len(seriesInstanceImages) == 0 {
			log.Println("[predict] empty series instance images")
			return dockerInferenceTypes.PredictRequest{}, "", errors.New(apiError.InferenceError)
		}
	} else {
		/// ---------------------- for DICOM metadata
		// get allowed dicom tags
		var allowedDICOMTags []string

		for _, supportedDICOMTag := range modelInfo.Data.SupportedDicomTags {
			isAllowed := true
			for _, disallowedTag := range inferenceModel.DisallowedDICOMTags {
				if supportedDICOMTag == disallowedTag {
					isAllowed = false
					break
				}
			}

			if isAllowed {
				allowedDICOMTags = append(allowedDICOMTags, supportedDICOMTag)
			}
		}

		// get series instances
		var m = sync.Mutex{}
		eg, egCtx := errgroup.WithContext(ctx)

		// set limit
		eg.SetLimit(len(data.SeriesInstanceUIDs))

		for _, seriesInstanceUID := range data.SeriesInstanceUIDs {
			func(seriesInstanceUID string) {
				eg.Go(func() error {
					m.Lock()
					defer m.Unlock()

					// get instances
					instances, err := service.OrthancAPIInterface.GetDICOMWebSeriesInstances(egCtx, data.StudyInstanceUID, seriesInstanceUID)
					if err != nil {
						return err
					}

					var mInstance = sync.Mutex{}
					egInstance, egInstanceCtx := errgroup.WithContext(egCtx)

					// set limit
					egInstance.SetLimit(len(instances))

					for _, instance := range instances {
						func(instance map[string]interface{}) {
							egInstance.Go(func() error {
								mInstance.Lock()
								defer mInstance.Unlock()

								seriesNumber := int(instance["00200011"].(map[string]interface{})["Value"].([]interface{})[0].(float64))
								sopInstanceUID := instance["00080018"].(map[string]interface{})["Value"].([]interface{})[0].(string)

								// assign by series number if doesnt exist yet
								if _, ok := seriesInstanceMetadata[seriesNumber]; !ok {
									seriesInstanceMetadata[seriesNumber] = make(map[int]interface{})
								}

								var sopInstanceNumber int
								var reservedTags []string

								// check for RTSTRUCT
								if instance["00080016"].(map[string]interface{})["Value"].([]interface{})[0].(string) == "1.2.840.10008.5.1.4.1.1.481.3" {
									sopInstanceNumber = seriesNumber // in RTSTRUCT, sopInstanceUID = seriesInstanceUID

									// assign reserved tags required for RTSTRUCT
									reservedTags = []string{"30060020", "30060039", "30060080", "300E0002"}
								} else {
									// if a study got series but no instance, skip it
									if _, ok := instance["00200013"]; !ok {
										log.Println("[dicom-web] skipping because 00200013 is missing")
										return nil
									}

									sopInstanceNumber = int(instance["00200013"].(map[string]interface{})["Value"].([]interface{})[0].(float64))

									// assign reserved tags
									reservedTags = []string{"7FE00010"}
								}

								// filter already added reserved tags (avoid duplicates)
								for _, tag := range reservedTags {
									if !slices.Contains(allowedDICOMTags, tag) {
										allowedDICOMTags = append(allowedDICOMTags, tag)
									}
								}

								// get instance metadata
								instanceMetadata, err := service.OrthancAPIInterface.RetrieveDICOMWebInstanceMetadata(egInstanceCtx, data.StudyInstanceUID, seriesInstanceUID, sopInstanceUID)
								if err != nil {
									return err
								}

								// prepare allowed instance metadata
								allowedInstanceMetadata := map[string]interface{}{}

								for _, instanceMetadataMap := range instanceMetadata {
									for _, allowedDICOMTag := range allowedDICOMTags {
										if _, ok := instanceMetadataMap[allowedDICOMTag]; ok {
											allowedInstanceMetadata[allowedDICOMTag] = instanceMetadataMap[allowedDICOMTag]
										}
									}

									// iterate all tags and convert BulkDataURI to InlineBinary
									for key := range allowedInstanceMetadata {
										if metadata, ok := allowedInstanceMetadata[key].(map[string]interface{}); ok {
											if bulkDataURI, hasBulkData := metadata["BulkDataURI"].(string); hasBulkData {
												// convert bulk data URI to inline binary
												inlineBinary, err := dicomUtils.ConvertBulkDataURIToInlineBinary(bulkDataURI)
												if err != nil {
													return err
												}

												metadata["InlineBinary"] = inlineBinary
												delete(metadata, "BulkDataURI")
											}
										}
									}
								}

								seriesInstanceMetadata[seriesNumber][sopInstanceNumber] = allowedInstanceMetadata
								return nil
							})
						}(instance)
					}

					// wait for all goroutines to finish
					if err := egInstance.Wait(); err != nil {
						return err
					}

					return nil
				})
			}(seriesInstanceUID)
		}

		// wait for all goroutines to finish
		if err := eg.Wait(); err != nil {
			return dockerInferenceTypes.PredictRequest{}, "", err
		}

		// check if SeriesInstanceMetadata is empty
		if len(seriesInstanceMetadata) == 0 {
			log.Println("[predict] empty series instance metadata")
			return dockerInferenceTypes.PredictRequest{}, "", errors.New(apiError.InferenceError)
		}
	}

	// Generate the request payload
	predictRequest := dockerInferenceTypes.PredictRequest{
		SeriesInstanceImages:   seriesInstanceImages,
		SeriesInstanceMetadata: seriesInstanceMetadata,
		AdditionalMetadata:     data.AdditionalMetadata,
		OutputMode:             dockerInferenceTypes.OutputMode(inferenceModel.OutputMode),
	}

	// Override OutputMode to JSON if ForceJSON is true
	if data.ForceJSON != nil && *data.ForceJSON {
		predictRequest.OutputMode = dockerInferenceTypes.OutputModeJSON
	}

	return predictRequest, containerName, nil
}

// PredictInferenceModel predicts an inference model
func (service *InferenceCommandService) PredictInferenceModel(ctx context.Context, tenantID, userID, containerID string, data types.PredictInferenceModel) (dockerInferenceTypes.PredictResponse, error) {
	// TODO: remove this
	predictionStartTime := time.Now()

	// Generate the request payload
	predictRequest, containerName, err := service.GenerateInferenceModelPredictRequest(ctx, tenantID, containerID, data)
	if err != nil {
		return dockerInferenceTypes.PredictResponse{}, err
	}

	// Send the prediction request
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

		// Get inference model data for logging
		inferenceModel, err := service.InferenceQueryRepositoryInterface.SelectInferenceModelByContainer(ctx, tenantID, containerID)
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
