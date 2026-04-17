package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
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
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	"api-pacs/module/inference/infrastructure/service/types"
	orthancApplication "api-pacs/module/orthanc/application"
	orthancServiceTypes "api-pacs/module/orthanc/infrastructure/service/types"
	tenantApplication "api-pacs/module/tenant/application"
	userApplication "api-pacs/module/user/application"
)

// InferenceCommandService handles the Inference command service logic
type InferenceCommandService struct {
	repository.InferenceCommandRepositoryInterface
	repository.InferenceQueryRepositoryInterface
	tenantApplication.TenantQueryServiceInterface
	userApplication.UserQueryServiceInterface
	orthancApplication.OrthancCommandServiceInterface
	orthancApplication.OrthancQueryServiceInterface
	elasticsearchApplication.ElasticsearchCommandServiceInterface
	dockerTypes.DockerSDKInterface
	orthancAPITypes.OrthancAPIInterface
	dockerInferenceTypes.DockerInferenceAPIInterface
}

const inferenceIngestionStabilityWindow = 10 * time.Minute
const inferenceIngestionRetrievalTimeout = 3 * time.Minute
const inferenceIngestionRetrievalPollInterval = 2 * time.Second

type candidateRetrievalOutcome string

const (
	candidateRetrievalOutcomeLocal   candidateRetrievalOutcome = "local"
	candidateRetrievalOutcomeSuccess candidateRetrievalOutcome = "success"
	candidateRetrievalOutcomeFailure candidateRetrievalOutcome = "failure"
	candidateRetrievalOutcomeTimeout candidateRetrievalOutcome = "timeout"
)

type candidateRetrievalResult struct {
	Outcome                  candidateRetrievalOutcome
	OrthancJobIDs            []string
	LastRetrievalState       *string
	LastRetrievalError       *string
	LastRetrievalErrorDetails *string
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

// AddOnboardingModelQuestionnaireAnswers adds an onboarding model questionnaire answers
func (service *InferenceCommandService) AddOnboardingModelQuestionnaireAnswers(ctx context.Context, data types.AddOnboardingModelQuestionnaireAnswer) error {
	// add onboarding model questionnaire answer
	for _, answer := range data.OnboardingModelQuestionnaireAnswers {
		err := service.InferenceCommandRepositoryInterface.InsertOnboardingModelQuestionnaireAnswer(ctx, repositoryTypes.AddOnboardingModelQuestionnaireAnswer{
			ID:                     generateID(),
			TenantID:               data.TenantID,
			UserID:                 data.UserID,
			ModelID:                data.ModelID,
			QuestionnaireID:        answer.QuestionnaireID,
			QuestionnaireQuestion:  answer.QuestionnaireQuestion,
			QuestionnaireAnswerIDs: answer.QuestionnaireAnswerIDs,
			QuestionnaireAnswers:   answer.QuestionnaireAnswers,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// CreateInferenceIngestionJob creates a new inference ingestion job
func (service *InferenceCommandService) CreateInferenceIngestionJob(ctx context.Context, data types.CreateInferenceIngestionJob) error {
	err := service.InferenceCommandRepositoryInterface.InsertInferenceIngestionJob(repositoryTypes.CreateInferenceIngestionJob{
		ID:                     generateID(),
		TenantID:               data.TenantID,
		DICOMModality:          data.DICOMModality,
		ContainerID:            data.ContainerID,
		ModelID:                data.ModelID,
		ModelName:              data.ModelName,
		ModelVersion:           data.ModelVersion,
		Modalities:             data.Modalities,
		IntervalInMinutes:      data.IntervalInMinutes,
		ScheduleStartTimestamp: time.Unix(int64(data.ScheduleStartTimestamp), 0),
		ScheduleEndTimestamp:   time.Unix(int64(data.ScheduleEndTimestamp), 0),
		Status:                 entity.InferenceIngestionJobStatusRunning, // default: RUNNING
	})
	if err != nil {
		return err
	}

	return nil
}

// ExecuteInferenceIngestionRunner execute inference ingestion runner
func (service *InferenceCommandService) ExecuteInferenceIngestionRunner(ctx context.Context) error {
	const missingPollsThreshold = 3

	/// step 1: get all inference ingestion jobs
	jobs, err := service.InferenceQueryRepositoryInterface.SelectInferenceIngestionJobs(nil) // all tenant
	if err != nil && err.Error() != apiError.MissingRecord {
		return err
	}

	/// step 2: for each job, run inference model to target dicom modality and persist results
	var m = sync.Mutex{}
	eg, egCtx := errgroup.WithContext(ctx)

	// set limit
	eg.SetLimit(len(jobs))

	for _, job := range jobs {
		func(job entity.InferenceIngestionJob) {
			eg.Go(func() error {
				m.Lock()
				defer m.Unlock()

				// if not running, skip
				if job.Status != entity.InferenceIngestionJobStatusRunning {
					return nil // skip
				}

				// if schedule start is set and not reached yet, skip
				if job.ScheduleStartTimestamp.Unix() != 0 && time.Now().Before(job.ScheduleStartTimestamp) {
					return nil // skip
				}

				// if schedule end is set and already passed, skip
				if job.ScheduleEndTimestamp.Unix() != 0 && time.Now().After(job.ScheduleEndTimestamp) {
					return nil // skip
				}

				// if interval hasn't elapsed since last execution, skip
				if job.LastExecutedAt != nil && time.Now().Before(job.LastExecutedAt.Add(time.Duration(job.IntervalInMinutes)*time.Minute)) {
					return nil // skip
				}

				existingCandidates, err := service.InferenceQueryRepositoryInterface.ListCandidatesByJob(job.ID)
				if err != nil && err.Error() != apiError.MissingRecord {
					log.Println("[inference ingestion] cannot list ingestion candidates:", err)
					return nil // skip
				}

				existingCandidateMap := map[string]entity.InferenceIngestionCandidate{}
				for _, candidate := range existingCandidates {
					existingCandidateMap[candidate.StudyInstanceUID] = candidate
				}

				/// step 3: get studies by dicom modality (with filter)
				studies, _, err := service.OrthancQueryServiceInterface.FindModalityStudies(egCtx, orthancServiceTypes.FindModalityStudies{
					TenantID:                      job.TenantID,
					ModalityID:                    job.DICOMModality,
					AccessionNumber:               "",
					InstitutionName:               "",
					ModalitiesInStudy:             strings.Join(job.Modalities, `\\`), // e.g XA\\US
					NumberOfStudyRelatedSeries:    "",
					NumberOfStudyRelatedInstances: "",
					PatientBirthDate:              "",
					PatientID:                     "",
					PatientName:                   "",
					PatientSex:                    "",
					ReferringPhysicianName:        "",
					RequestingPhysician:           "",
					StudyDate:                     "",
					StudyDescription:              "",
					StudyID:                       "",
					StudyInstanceUID:              "",
					StudyTime:                     "",
					UserID:                        nil,
				})
				if err != nil {
					log.Println("[inference ingestion] cannot pull studies:", err)
					return nil // skip
				}

				seenStudyUIDs := map[string]struct{}{}
				for _, study := range studies {
					select {
					case <-egCtx.Done():
						return nil // skip
					default:
					}

					seenStudyUIDs[study.StudyInstanceUID] = struct{}{}

					studyDate := nullableString(study.StudyDate)
					studyTime := nullableString(study.StudyTime)
					modalitiesInStudy := nullableString(study.ModalitiesInStudy)
					patientID := nullableString(study.PatientID)
					accessionNumber := nullableString(study.AccessionNumber)

					seriesCount, parseSeriesErr := parseNullableInt(study.NumberOfStudyRelatedSeries)
					if parseSeriesErr != nil {
						log.Printf("[inference ingestion] cannot parse series count for study %s: %v", study.StudyInstanceUID, parseSeriesErr)
					}

					instanceCount, parseInstanceErr := parseNullableInt(study.NumberOfStudyRelatedInstances)
					if parseInstanceErr != nil {
						log.Printf("[inference ingestion] cannot parse instance count for study %s: %v", study.StudyInstanceUID, parseInstanceErr)
					}

					priorCandidate, hasPriorCandidate := existingCandidateMap[study.StudyInstanceUID]

					err = service.InferenceCommandRepositoryInterface.UpsertIngestionCandidate(repositoryTypes.UpsertIngestionCandidate{
						ID:                generateID(),
						TenantID:          job.TenantID,
						IngestionJobID:    job.ID,
						StudyInstanceUID:  study.StudyInstanceUID,
						StudyDate:         studyDate,
						StudyTime:         studyTime,
						ModalitiesInStudy: modalitiesInStudy,
						PatientID:         patientID,
						AccessionNumber:   accessionNumber,
						SeriesCount:       seriesCount,
						InstanceCount:     instanceCount,
					})
					if err != nil {
						log.Println("[inference ingestion] cannot upsert ingestion candidate:", err)
						continue
					}

					if hasPriorCandidate &&
						candidateCountsChanged(priorCandidate.SeriesCount, seriesCount, priorCandidate.InstanceCount, instanceCount) &&
						shouldMarkCandidateGrowing(priorCandidate.Status) {
						err = service.InferenceCommandRepositoryInterface.UpdateCandidateStatus(priorCandidate.ID, entity.InferenceIngestionCandidateStatusGrowing)
						if err != nil {
							log.Println("[inference ingestion] cannot update ingestion candidate status to growing:", err)
						}
					}
				}

				for _, candidate := range existingCandidates {
					if _, seen := seenStudyUIDs[candidate.StudyInstanceUID]; seen {
						continue
					}

					if !shouldTrackCandidateMissing(candidate.Status) {
						continue
					}

					err = service.InferenceCommandRepositoryInterface.IncrementCandidateMissingPolls(candidate.ID)
					if err != nil {
						log.Println("[inference ingestion] cannot increment ingestion candidate missing polls:", err)
						continue
					}

					if candidate.MissingPolls+1 >= missingPollsThreshold {
						err = service.InferenceCommandRepositoryInterface.MarkCandidateDisappeared(candidate.ID)
						if err != nil {
							log.Println("[inference ingestion] cannot mark ingestion candidate disappeared:", err)
						}
					}
				}

				refreshedCandidates, err := service.InferenceQueryRepositoryInterface.ListCandidatesByJob(job.ID)
				if err != nil && err.Error() != apiError.MissingRecord {
					log.Println("[inference ingestion] cannot refresh ingestion candidates:", err)
					return nil // skip
				}

				stableBefore := time.Now().Add(-inferenceIngestionStabilityWindow)
				for _, candidate := range refreshedCandidates {
					if !shouldMarkCandidateStable(candidate.Status) {
						continue
					}

					if candidate.MissingPolls > 0 {
						continue
					}

					if candidate.LastChangedAt.After(stableBefore) {
						continue
					}

					err = service.InferenceCommandRepositoryInterface.UpdateCandidateStatus(candidate.ID, entity.InferenceIngestionCandidateStatusStable)
					if err != nil {
						log.Println("[inference ingestion] cannot update ingestion candidate status to stable:", err)
					}
				}

				readyCandidates, err := service.InferenceQueryRepositoryInterface.ListCandidatesReadyForRetrieval(job.ID, time.Now().Add(-inferenceIngestionStabilityWindow))
				if err != nil && err.Error() != apiError.MissingRecord {
					log.Println("[inference ingestion] cannot list stable candidates ready for retrieval:", err)
					return nil // skip
				}

				for _, candidate := range readyCandidates {
					isLocal, err := service.isStudyPresentLocally(egCtx, candidate.StudyInstanceUID)
					if err != nil {
						log.Println("[inference ingestion] cannot determine if stable ingestion candidate is already local:", err)
						continue
					}

					if isLocal {
						err = service.InferenceCommandRepositoryInterface.MarkCandidateRetrievedWithContext(repositoryTypes.UpdateCandidateRetrievalState{
							ID:                 candidate.ID,
							LastRetrievalState: stringPointer(string(candidateRetrievalOutcomeLocal)),
						})
						if err != nil {
							log.Println("[inference ingestion] cannot mark already-local ingestion candidate retrieved:", err)
						}
						continue
					}

					err = service.InferenceCommandRepositoryInterface.MarkCandidateRetrievalQueued(candidate.ID)
					if err != nil {
						log.Println("[inference ingestion] cannot mark ingestion candidate retrieval queued:", err)
						continue
					}

					retrievalResult, err := service.retrieveStableCandidate(egCtx, job, candidate)
					if err != nil {
						log.Println("[inference ingestion] cannot retrieve stable ingestion candidate:", err)
						markErr := service.InferenceCommandRepositoryInterface.MarkCandidateFailedWithContext(repositoryTypes.UpdateCandidateRetrievalState{
							ID:                 candidate.ID,
							LastRetrievalState: stringPointer("error"),
							LastRetrievalError: stringPointer(err.Error()),
						})
						if markErr != nil {
							log.Println("[inference ingestion] cannot persist retrieval error for ingestion candidate:", markErr)
						}
						continue
					}

					retrievalState := stringPointer(string(retrievalResult.Outcome))
					switch retrievalResult.Outcome {
					case candidateRetrievalOutcomeLocal, candidateRetrievalOutcomeSuccess:
						err = service.InferenceCommandRepositoryInterface.MarkCandidateRetrievedWithContext(repositoryTypes.UpdateCandidateRetrievalState{
							ID:                        candidate.ID,
							OrthancJobIDs:             retrievalResult.OrthancJobIDs,
							LastRetrievalState:        coalesceStringPointer(retrievalResult.LastRetrievalState, retrievalState),
							LastRetrievalError:        retrievalResult.LastRetrievalError,
							LastRetrievalErrorDetails: retrievalResult.LastRetrievalErrorDetails,
						})
						if err != nil {
							log.Println("[inference ingestion] cannot mark ingestion candidate retrieved:", err)
						}
					case candidateRetrievalOutcomeFailure:
						err = service.InferenceCommandRepositoryInterface.MarkCandidateFailedWithContext(repositoryTypes.UpdateCandidateRetrievalState{
							ID:                        candidate.ID,
							OrthancJobIDs:             retrievalResult.OrthancJobIDs,
							LastRetrievalState:        coalesceStringPointer(retrievalResult.LastRetrievalState, retrievalState),
							LastRetrievalError:        coalesceStringPointer(retrievalResult.LastRetrievalError, stringPointer("Orthanc retrieval failed")),
							LastRetrievalErrorDetails: retrievalResult.LastRetrievalErrorDetails,
						})
						if err != nil {
							log.Println("[inference ingestion] cannot mark ingestion candidate failed:", err)
						}
					case candidateRetrievalOutcomeTimeout:
						err = service.InferenceCommandRepositoryInterface.MarkCandidateFailedWithContext(repositoryTypes.UpdateCandidateRetrievalState{
							ID:                        candidate.ID,
							OrthancJobIDs:             retrievalResult.OrthancJobIDs,
							LastRetrievalState:        coalesceStringPointer(retrievalResult.LastRetrievalState, retrievalState),
							LastRetrievalError:        coalesceStringPointer(retrievalResult.LastRetrievalError, stringPointer("Orthanc retrieval timed out")),
							LastRetrievalErrorDetails: retrievalResult.LastRetrievalErrorDetails,
						})
						if err != nil {
							log.Println("[inference ingestion] cannot persist retrieval timeout for ingestion candidate:", err)
						}
					}
				}

				/// step 4: update job last execution time
				_ = service.InferenceCommandRepositoryInterface.UpdateInferenceIngestionJobLastExecutedAt(job.ID)

				return nil
			})
		}(job)
	}

	// wait for all goroutines to finish
	if err := eg.Wait(); err != nil {
		return err
	}

	return nil
}

func nullableString(value string) *string {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return nil
	}

	return &trimmedValue
}

func parseNullableInt(value string) (*int, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return nil, nil
	}

	parsedValue, err := strconv.Atoi(trimmedValue)
	if err != nil {
		return nil, err
	}

	return &parsedValue, nil
}

func candidateCountsChanged(previousSeriesCount, currentSeriesCount, previousInstanceCount, currentInstanceCount *int) bool {
	return nullableIntChanged(previousSeriesCount, currentSeriesCount) || nullableIntChanged(previousInstanceCount, currentInstanceCount)
}

func nullableIntChanged(previousValue, currentValue *int) bool {
	if previousValue == nil && currentValue == nil {
		return false
	}

	if previousValue == nil || currentValue == nil {
		return true
	}

	return *previousValue != *currentValue
}

func shouldMarkCandidateGrowing(status entity.InferenceIngestionCandidateStatus) bool {
	return status != entity.InferenceIngestionCandidateStatusRetrievalQueued &&
		status != entity.InferenceIngestionCandidateStatusRetrieved &&
		status != entity.InferenceIngestionCandidateStatusFailed
}

func shouldTrackCandidateMissing(status entity.InferenceIngestionCandidateStatus) bool {
	return status != entity.InferenceIngestionCandidateStatusDisappeared &&
		status != entity.InferenceIngestionCandidateStatusRetrievalQueued &&
		status != entity.InferenceIngestionCandidateStatusRetrieved &&
		status != entity.InferenceIngestionCandidateStatusFailed
}

func shouldMarkCandidateStable(status entity.InferenceIngestionCandidateStatus) bool {
	return status == entity.InferenceIngestionCandidateStatusDiscovered ||
		status == entity.InferenceIngestionCandidateStatusGrowing
}

func (service *InferenceCommandService) retrieveStableCandidate(ctx context.Context, job entity.InferenceIngestionJob, candidate entity.InferenceIngestionCandidate) (candidateRetrievalResult, error) {
	isLocal, err := service.isStudyPresentLocally(ctx, candidate.StudyInstanceUID)
	if err != nil {
		return candidateRetrievalResult{}, err
	}

	if isLocal {
		return candidateRetrievalResult{
			Outcome:            candidateRetrievalOutcomeLocal,
			LastRetrievalState: stringPointer(string(candidateRetrievalOutcomeLocal)),
		}, nil
	}

	retrieveResponses, err := service.OrthancCommandServiceInterface.RetrieveModalityStudyBySeries(ctx, orthancServiceTypes.RetrieveModalityStudyBySeries{
		TenantID:         job.TenantID,
		ModalityID:       job.DICOMModality,
		StudyInstanceUID: candidate.StudyInstanceUID,
		ModalityType:     inferCandidateModalityType(candidate.ModalitiesInStudy),
		UserID:           nil,
	})
	if err != nil {
		if err.Error() == apiError.DuplicateRecord {
			isLocal, localErr := service.isStudyPresentLocally(ctx, candidate.StudyInstanceUID)
			if localErr != nil {
				return candidateRetrievalResult{}, localErr
			}

			if isLocal {
				return candidateRetrievalResult{
					Outcome:            candidateRetrievalOutcomeLocal,
					LastRetrievalState: stringPointer(string(candidateRetrievalOutcomeLocal)),
				}, nil
			}
		}

		return candidateRetrievalResult{}, err
	}

	jobIDs := extractOrthancJobIDs(retrieveResponses)
	if len(jobIDs) == 0 {
		return candidateRetrievalResult{
			Outcome:            candidateRetrievalOutcomeFailure,
			LastRetrievalState: stringPointer(string(candidateRetrievalOutcomeFailure)),
			LastRetrievalError: stringPointer("Orthanc retrieve did not return any job IDs"),
		}, nil
	}

	err = service.InferenceCommandRepositoryInterface.SaveCandidateOrthancJobIDs(candidate.ID, jobIDs)
	if err != nil {
		return candidateRetrievalResult{}, err
	}

	return service.waitForCandidateRetrieval(ctx, candidate.StudyInstanceUID, jobIDs)
}

func (service *InferenceCommandService) waitForCandidateRetrieval(ctx context.Context, studyInstanceUID string, orthancJobIDs []string) (candidateRetrievalResult, error) {
	deadline := time.Now().Add(inferenceIngestionRetrievalTimeout)

	for {
		isLocal, err := service.isStudyPresentLocally(ctx, studyInstanceUID)
		if err != nil {
			return candidateRetrievalResult{}, err
		}

		if isLocal {
			return candidateRetrievalResult{
				Outcome:            candidateRetrievalOutcomeLocal,
				OrthancJobIDs:      orthancJobIDs,
				LastRetrievalState: stringPointer(string(candidateRetrievalOutcomeLocal)),
			}, nil
		}

		jobs, err := service.OrthancQueryServiceInterface.GetJobsInfo(ctx, orthancJobIDs)
		if err != nil {
			return candidateRetrievalResult{}, err
		}

		if hasOrthancFailure(jobs) {
			return candidateRetrievalResult{
				Outcome:                  candidateRetrievalOutcomeFailure,
				OrthancJobIDs:            orthancJobIDs,
				LastRetrievalState:       stringPointer(string(candidateRetrievalOutcomeFailure)),
				LastRetrievalError:       orthancFailureDescription(jobs),
				LastRetrievalErrorDetails: marshalOrthancJobs(failedOrthancJobs(jobs)),
			}, nil
		}

		if hasOrthancSuccess(jobs) {
			isLocalAfterSuccess, localErr := service.isStudyPresentLocally(ctx, studyInstanceUID)
			if localErr != nil {
				return candidateRetrievalResult{}, localErr
			}

			outcome := candidateRetrievalOutcomeSuccess
			if isLocalAfterSuccess {
				outcome = candidateRetrievalOutcomeLocal
			}

			return candidateRetrievalResult{
				Outcome:                  outcome,
				OrthancJobIDs:            orthancJobIDs,
				LastRetrievalState:       stringPointer(string(outcome)),
				LastRetrievalErrorDetails: marshalOrthancJobs(jobs),
			}, nil
		}

		if time.Now().After(deadline) {
			return candidateRetrievalResult{
				Outcome:                  candidateRetrievalOutcomeTimeout,
				OrthancJobIDs:            orthancJobIDs,
				LastRetrievalState:       stringPointer(string(candidateRetrievalOutcomeTimeout)),
				LastRetrievalError:       stringPointer("Orthanc retrieval timed out"),
				LastRetrievalErrorDetails: marshalOrthancJobs(jobs),
			}, nil
		}

		select {
		case <-ctx.Done():
			return candidateRetrievalResult{}, ctx.Err()
		case <-time.After(inferenceIngestionRetrievalPollInterval):
		}
	}
}

func (service *InferenceCommandService) isStudyPresentLocally(ctx context.Context, studyInstanceUID string) (bool, error) {
	localResources, err := service.OrthancAPIInterface.FindLocalResources(ctx, orthancAPITypes.QueryLocalResourceRequest{
		Level: "Study",
		Query: orthancAPITypes.QueryLocalResource{
			StudyInstanceUID: studyInstanceUID,
		},
		Expand: true,
	})
	if err != nil {
		if err.Error() == apiError.MissingRecord {
			return false, nil
		}

		return false, err
	}

	return len(localResources) > 0, nil
}

func extractOrthancJobIDs(responses []orthancAPITypes.QueryModalityResponse) []string {
	jobIDs := make([]string, 0, len(responses))
	seenJobIDs := make(map[string]struct{}, len(responses))

	for _, response := range responses {
		if strings.TrimSpace(response.ID) == "" {
			continue
		}

		if _, seen := seenJobIDs[response.ID]; seen {
			continue
		}

		seenJobIDs[response.ID] = struct{}{}
		jobIDs = append(jobIDs, response.ID)
	}

	return jobIDs
}

func inferCandidateModalityType(modalitiesInStudy *string) string {
	if modalitiesInStudy == nil {
		return ""
	}

	if strings.Contains(*modalitiesInStudy, "US") {
		return "US"
	}

	return "other"
}

func hasOrthancFailure(jobs []orthancAPITypes.GetJobResponse) bool {
	for _, job := range jobs {
		if job.State == string(orthancAPITypes.JobFailure) {
			return true
		}
	}

	return false
}

func hasOrthancSuccess(jobs []orthancAPITypes.GetJobResponse) bool {
	if len(jobs) == 0 {
		return false
	}

	for _, job := range jobs {
		if job.State != string(orthancAPITypes.JobSuccess) {
			return false
		}
	}

	return true
}

func failedOrthancJobs(jobs []orthancAPITypes.GetJobResponse) []orthancAPITypes.GetJobResponse {
	failedJobs := make([]orthancAPITypes.GetJobResponse, 0, len(jobs))

	for _, job := range jobs {
		if job.State == string(orthancAPITypes.JobFailure) {
			failedJobs = append(failedJobs, job)
		}
	}

	return failedJobs
}

func orthancFailureDescription(jobs []orthancAPITypes.GetJobResponse) *string {
	for _, job := range jobs {
		if job.State != string(orthancAPITypes.JobFailure) {
			continue
		}

		description := strings.TrimSpace(job.ErrorDescription)
		if description != "" {
			return &description
		}
	}

	return stringPointer("Orthanc retrieval failed")
}

func marshalOrthancJobs(jobs []orthancAPITypes.GetJobResponse) *string {
	if len(jobs) == 0 {
		return nil
	}

	jobBytes, err := json.Marshal(jobs)
	if err != nil {
		return nil
	}

	jobDetails := string(jobBytes)
	return &jobDetails
}

func coalesceStringPointer(value, fallback *string) *string {
	if value != nil {
		return value
	}

	return fallback
}

func stringPointer(value string) *string {
	return &value
}

// GenerateInferenceModelPredictRequest generates a predict request for inference model
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
		// purposely re-implementing the series ininstances loop because of potential refactors and differences vs metadata

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

								mInstance.Lock()
								seriesInstanceImages[seriesNumber][sopInstanceNumber] = base64.StdEncoding.EncodeToString(instanceFile) // convert to base64
								defer mInstance.Unlock()

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

	// create predict request
	predictRequest := dockerInferenceTypes.PredictRequest{
		SeriesInstanceImages:   seriesInstanceImages,
		SeriesInstanceMetadata: seriesInstanceMetadata,
		AdditionalMetadata:     data.AdditionalMetadata,
		OutputMode:             dockerInferenceTypes.OutputMode(inferenceModel.OutputMode),
	}

	// override OutputMode to JSON if ForceJSON is true
	if data.ForceJSON != nil && *data.ForceJSON {
		predictRequest.OutputMode = dockerInferenceTypes.OutputModeJSON
	}

	return predictRequest, containerName, nil
}

// ImportInferenceIngestionJobs imports inference ingestion jobs
func (service *InferenceCommandService) ImportInferenceIngestionJobs(ctx context.Context, data []types.CreateInferenceIngestionJob) error {
	// add inference ingestion jobs
	for _, job := range data {
		err := service.InferenceCommandRepositoryInterface.InsertInferenceIngestionJob(repositoryTypes.CreateInferenceIngestionJob{
			ID:                     generateID(),
			TenantID:               job.TenantID,
			DICOMModality:          job.DICOMModality,
			ContainerID:            job.ContainerID,
			ModelID:                job.ModelID,
			ModelName:              job.ModelName,
			ModelVersion:           job.ModelVersion,
			Modalities:             job.Modalities,
			IntervalInMinutes:      job.IntervalInMinutes,
			ScheduleStartTimestamp: time.Unix(int64(job.ScheduleStartTimestamp), 0),
			ScheduleEndTimestamp:   time.Unix(int64(job.ScheduleEndTimestamp), 0),
			Status:                 entity.InferenceIngestionJobStatusRunning, // default: RUNNING,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// PredictInferenceModel predicts an inference model
func (service *InferenceCommandService) PredictInferenceModel(ctx context.Context, tenantID, containerID string, userID *string, data types.PredictInferenceModel) (dockerInferenceTypes.PredictResponse, error) {
	// TODO: remove this
	predictionStartTime := time.Now()

	predictRequest, containerName, err := service.GenerateInferenceModelPredictRequest(ctx, tenantID, containerID, data)
	if err != nil {
		return dockerInferenceTypes.PredictResponse{}, err
	}

	// predict
	predictionResult, err := service.DockerInferenceAPIInterface.Predict(ctx, containerName, predictRequest)
	if err != nil {
		return dockerInferenceTypes.PredictResponse{}, err
	}

	// TODO: remove this
	predictionEndTime := time.Since(predictionStartTime)
	log.Printf("[prediction] predict call took %f seconds", predictionEndTime.Seconds())

	// log to elasticsearch
	if userID != nil {
		go func() {
			user, err := service.UserQueryServiceInterface.GetTenantUserByID(ctx, tenantID, *userID)
			if err != nil {
				log.Println(err)
				return
			}

			tenant, err := service.TenantQueryServiceInterface.GetTenantByID(ctx, tenantID)
			if err != nil {
				log.Println(err)
				return
			}

			// Get inference model data for logging
			inferenceModel, err := service.InferenceQueryRepositoryInterface.SelectInferenceModelByContainer(ctx, tenantID, containerID)
			if err != nil {
				log.Println(err)
				return
			}

			// get model info
			modelInfo, err := service.DockerInferenceAPIInterface.GetModelInfo(ctx, containerName)
			if err != nil {
				log.Println(err)
				return
			}

			modelID := modelInfo.Data.ModelID
			if len(modelID) == 0 {
				modelID = modelInfo.Data.ModelName
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
				Model:              fmt.Sprintf("%s-%s", modelID, modelInfo.Data.Version), // {modelID/modelName-version}
				StudyInstanceUID:   data.StudyInstanceUID,
				SeriesInstanceUIDs: data.SeriesInstanceUIDs,
				AdditionalMetadata: data.AdditionalMetadata,
			})
			if err != nil {
				log.Println(err)
				return
			}
		}()
	}

	return predictionResult, nil
}

// RemoveInferenceModel deletes an inference model
func (service *InferenceCommandService) RemoveInferenceModel(ctx context.Context, ID string) error {
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

	// delete inference ingestion jobs related
	err = service.InferenceCommandRepositoryInterface.DeleteInferenceIngestionJobByContainerID(inferenceModel.TenantID, inferenceModel.ContainerID)
	if err != nil {
		return err
	}

	return nil
}

// RemoveInferenceIngestionJob removes an inference ingestion job
func (service *InferenceCommandService) RemoveInferenceIngestionJob(ctx context.Context, ID string) error {
	err := service.InferenceCommandRepositoryInterface.DeleteInferenceIngestionJob(ID)
	if err != nil {
		return err
	}

	return nil
}

// RemoveOnboardingModelQuestionnaireAnswer removes an onboarding model questionnaire answer
func (service *InferenceCommandService) RemoveOnboardingModelQuestionnaireAnswer(ctx context.Context, ID string) error {
	err := service.InferenceCommandRepositoryInterface.DeleteOnboardingModelQuestionnaireAnswer(ctx, ID)
	if err != nil {
		return err
	}

	return nil
}

// RemoveModelFeedback removes model feedback
func (service *InferenceCommandService) RemoveModelFeedback(ctx context.Context, data types.RemoveModelFeedback) error {
	// get model feedback
	modelFeedback, err := service.InferenceQueryRepositoryInterface.SelectModelFeedbackByUserModelID(ctx, repositoryTypes.GetModelFeedbackByUserModelID{
		TenantID: data.TenantID,
		UserID:   data.UserID,
		ModelID:  data.ModelID,
	})
	if err != nil {
		return err
	}

	// delete model feedback
	err = service.InferenceCommandRepositoryInterface.DeleteModelFeedback(ctx, modelFeedback.ID)
	if err != nil {
		return err
	}

	// get model feedback answers
	modelFeedbackAnswers, err := service.InferenceQueryRepositoryInterface.SelectModelFeedbackAnswersByFeedbackID(ctx, modelFeedback.ID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return err
	}

	// delete model feedback answers
	for _, modelFeedbackAnswer := range modelFeedbackAnswers {
		err = service.InferenceCommandRepositoryInterface.DeleteModelFeedbackAnswer(ctx, modelFeedbackAnswer.ID)
		if err != nil {
			return err
		}
	}

	return nil
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

// StartInferenceIngestionJob starts an inference ingestion job
func (service *InferenceCommandService) StartInferenceIngestionJob(ctx context.Context, jobID string) error {
	err := service.InferenceCommandRepositoryInterface.UpdateInferenceIngestionJobStatus(jobID, entity.InferenceIngestionJobStatusRunning)
	if err != nil {
		return err
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

// StopInferenceIngestionJob stops an inference ingestion job
func (service *InferenceCommandService) StopInferenceIngestionJob(ctx context.Context, jobID string) error {
	err := service.InferenceCommandRepositoryInterface.UpdateInferenceIngestionJobStatus(jobID, entity.InferenceIngestionJobStatusStopped)
	if err != nil {
		return err
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

// UpdateInferenceIngestionJob updates an inference ingestion job
func (service *InferenceCommandService) UpdateInferenceIngestionJob(ctx context.Context, data types.UpdateInferenceIngestionJob) error {
	err := service.InferenceCommandRepositoryInterface.UpdateInferenceIngestionJob(repositoryTypes.UpdateInferenceIngestionJob{
		ID:                     data.ID,
		Modalities:             data.Modalities,
		IntervalInMinutes:      data.IntervalInMinutes,
		ScheduleStartTimestamp: time.Unix(int64(data.ScheduleStartTimestamp), 0),
		ScheduleEndTimestamp:   time.Unix(int64(data.ScheduleEndTimestamp), 0),
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

// UpdateModelFeedback updates model feedback
func (service *InferenceCommandService) UpdateModelFeedback(ctx context.Context, data types.UpdateModelFeedback) error {
	modelFeedbackID := data.ID

	if modelFeedbackID == nil {
		modelFeedbackIDStr := generateID()
		modelFeedbackID = &modelFeedbackIDStr
	}

	err := service.InferenceCommandRepositoryInterface.UpsertModelFeedback(ctx, repositoryTypes.UpsertModelFeedback{
		ID:               *modelFeedbackID,
		TenantID:         data.TenantID,
		InferenceModelID: data.InferenceModelID,
		UserID:           data.UserID,
		ModelID:          data.ModelID,
		FeedbackType:     data.FeedbackType,
	})
	if err != nil {
		return err
	}

	// delete exiting feedback answers
	// get model feedback answers
	modelFeedbackAnswers, err := service.InferenceQueryRepositoryInterface.SelectModelFeedbackAnswersByFeedbackID(ctx, *modelFeedbackID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return err
	}

	// delete model feedback answers
	for _, modelFeedbackAnswer := range modelFeedbackAnswers {
		err = service.InferenceCommandRepositoryInterface.DeleteModelFeedbackAnswer(ctx, modelFeedbackAnswer.ID)
		if err != nil {
			return err
		}
	}

	// add model feedback answers
	for _, answer := range data.ModelFeedbackAnswers {
		err = service.InferenceCommandRepositoryInterface.InsertModelFeedbackAnswer(ctx, repositoryTypes.AddModelFeedbackAnswer{
			ID:                     generateID(),
			ModelFeedbackID:        *modelFeedbackID,
			QuestionnaireID:        answer.QuestionnaireID,
			QuestionnaireQuestion:  answer.QuestionnaireQuestion,
			QuestionnaireAnswerIDs: answer.QuestionnaireAnswerIDs,
			QuestionnaireAnswers:   answer.QuestionnaireAnswers,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func generateID() string {
	return ksuid.New().String()
}
